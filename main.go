package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/pristupaanastasia/llm_check_bot/internal/config"
	"github.com/pristupaanastasia/llm_check_bot/internal/detector"
	"github.com/pristupaanastasia/llm_check_bot/internal/history"
	"github.com/pristupaanastasia/llm_check_bot/internal/mencount"
)

// supportCooldown: at most one support reply per person per chat in this window.
const supportCooldown = time.Hour

// supportText is deliberately written in the familiar "assistant" style, but the
// numbers are real Russian hotlines: the person may mean it.
const supportText = `%s, похоже, вам сейчас по-настоящему непросто 💙

Важно помнить: вы не одни, и ваши чувства имеют значение. Тяжёлые периоды случаются, и обращаться за поддержкой — это не слабость, а проявление силы.

Если вам тяжело, пожалуйста, поговорите с кем-то, кому доверяете, или позвоните — там выслушают бесплатно и анонимно:
📞 8-800-2000-122 — телефон доверия (бесплатно по России)
📞 8-495-989-50-50 — экстренная психологическая помощь МЧС, круглосуточно
🚑 112 — если есть опасность прямо сейчас

Берегите себя. Мы рядом ❤️`

// checkTimeout bounds one LLM request; shutdownWait is how long we let
// in-flight checks finish after SIGINT/SIGTERM.
const (
	checkTimeout = 2 * time.Minute
	shutdownWait = 30 * time.Second
)

type app struct {
	bot    *tgbotapi.BotAPI
	det    detector.Checker
	hist   history.Store
	men    mencount.Store
	cfg    config.Config
	redact func(string) string

	sem chan struct{} // limits parallel LLM requests

	// imageMu guards imageID: after the first upload Telegram returns a file_id,
	// and later replies reuse it instead of uploading the file again.
	imageMu sync.Mutex
	imageID string

	// userLocks serialises checks per (chat, user) so the style history is
	// updated in message order.
	mu        sync.Mutex
	userLocks map[[2]int64]*sync.Mutex
}

func main() {
	// Read .env if present. Variables already set in the environment take precedence.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Fatalf(".env: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	client := newRedactingClient(cfg.TelegramToken)
	bot, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramToken, tgbotapi.APIEndpoint, client)
	if err != nil {
		log.Fatalf("telegram: %s", client.redact(err.Error()))
	}
	log.Printf("authorized as @%s, %s model %s, threshold %d, love counter %v, think %v", bot.Self.UserName, cfg.Provider, cfg.Model, cfg.Threshold, cfg.LoveCounter, cfg.OllamaThink)
	if cfg.SlopImage != "" {
		if _, err := os.Stat(cfg.SlopImage); err != nil {
			log.Printf("slop image %q not found, replying with text only", cfg.SlopImage)
			cfg.SlopImage = ""
		}
	}

	hist, men := openStores(cfg)

	var det detector.Checker
	if cfg.Provider == "claude" {
		det = detector.NewClaude(cfg.Model)
	} else {
		o := detector.NewOllama(cfg.OllamaURL, cfg.Model, cfg.OllamaKeepAlive)
		o.NumThread = cfg.OllamaNumThread
		o.Think = cfg.OllamaThink
		det = o
	}

	a := &app{
		bot:       bot,
		det:       det,
		hist:      hist,
		men:       men,
		cfg:       cfg,
		redact:    client.redact,
		sem:       make(chan struct{}, cfg.MaxConcurrent),
		userLocks: map[[2]int64]*sync.Mutex{},
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Handlers get a context that survives the signal, so in-flight checks can
	// finish during shutdownWait instead of being dropped.
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	var wg sync.WaitGroup
	for {
		select {
		case <-sigCtx.Done():
			bot.StopReceivingUpdates()
			log.Println("shutting down, waiting for in-flight checks")
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(shutdownWait):
				log.Println("shutdown timeout, cancelling remaining checks")
				cancelWork()
			}
			return
		case upd := <-updates:
			msg, edited := upd.Message, false
			if msg == nil {
				msg, edited = upd.EditedMessage, true
			}
			if msg == nil || msg.From == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.handle(workCtx, msg, edited)
			}()
		}
	}
}

func (a *app) userLock(chatID, userID int64) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := [2]int64{chatID, userID}
	l, ok := a.userLocks[k]
	if !ok {
		l = &sync.Mutex{}
		a.userLocks[k] = l
	}
	return l
}

// debugf logs only when DEBUG is set; it never logs message text.
func (a *app) debugf(format string, args ...any) {
	if a.cfg.Debug {
		log.Printf("debug: "+format, args...)
	}
}

func isForward(m *tgbotapi.Message) bool {
	return m.ForwardDate != 0 || m.ForwardFrom != nil || m.ForwardFromChat != nil ||
		m.ForwardSenderName != "" || m.IsAutomaticForward
}

func (a *app) handle(ctx context.Context, msg *tgbotapi.Message, edited bool) {
	if msg.Chat.IsPrivate() && msg.IsCommand() && msg.Command() == "start" {
		a.reply(msg, fmt.Sprintf("Привет! Твой Telegram ID: %d. Добавь меня в группу, и я буду проверять сообщения отслеживаемых пользователей на нейрослоп.", msg.From.ID))
		return
	}
	a.debugf("update: chat %d (%s), user %d (@%s), msg %d, edited=%v, %d chars",
		msg.Chat.ID, msg.Chat.Type, msg.From.ID, msg.From.UserName, msg.MessageID, edited,
		utf8.RuneCountInString(msg.Text+msg.Caption))
	if msg.From.IsBot || msg.SenderChat != nil {
		a.debugf("skip msg %d: sent by a bot or on behalf of a channel", msg.MessageID)
		return
	}
	if msg.IsCommand() && msg.Command() == "men" {
		if a.cfg.LoveCounter {
			a.replyMenTop(msg)
		}
		return
	}
	// Self-harm phrases are checked for everyone and regardless of length.
	if t := msg.Text + " " + msg.Caption; !isForward(msg) && detector.MentionsSelfHarm(t) {
		log.Printf("chat %d, user %d (@%s): self-harm phrase", msg.Chat.ID, msg.From.ID, msg.From.UserName)
		a.replySupport(msg)
		return
	}
	if isForward(msg) || msg.IsCommand() {
		a.debugf("skip msg %d: forward or command", msg.MessageID)
		return
	}
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		a.debugf("skip msg %d: no text", msg.MessageID)
		return
	}
	chatID, userID := msg.Chat.ID, msg.From.ID

	l := a.userLock(chatID, userID)
	l.Lock()
	defer l.Unlock()

	// People named in a love/together context are counted for everyone.
	// Edits are ignored so one message can't be counted twice.
	wantPeople := a.cfg.LoveCounter && !edited && detector.MaybeRomance(text)

	// Decide whether this message also gets the slop check. If it does, one
	// combined request answers both; otherwise a small people-only request
	// runs when needed. Either way: at most one model request per message.
	slopReason := ""
	switch {
	case !a.cfg.IsWatched(userID, msg.From.UserName):
		slopReason = "user is not in WATCHED_USERS"
	case utf8.RuneCountInString(text) < a.cfg.MinLength:
		// Short replies are almost always typed by hand: the best style sample.
		if !edited {
			a.hist.Add(chatID, userID, text)
		}
		slopReason = fmt.Sprintf("shorter than MIN_MESSAGE_LENGTH=%d, saved as style sample", a.cfg.MinLength)
	case edited && a.hist.IsFlagged(chatID, msg.MessageID):
		slopReason = "edit of already flagged message"
	}
	if slopReason != "" {
		a.debugf("no slop check for msg %d: %s", msg.MessageID, slopReason)
		if wantPeople {
			a.countRomance(ctx, msg, text)
		}
		return
	}
	a.debugf("checking msg %d with %s (people=%v)", msg.MessageID, a.cfg.Model, wantPeople)

	release, ok := a.acquireModel(ctx)
	if !ok {
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	v, err := a.det.Check(ctx, text, a.hist.Recent(chatID, userID), wantPeople)
	if err != nil {
		if errors.Is(err, detector.ErrRefused) {
			log.Printf("check message %d from %d: model refused", msg.MessageID, userID)
		} else {
			log.Printf("check message %d from %d: %v", msg.MessageID, userID, err)
		}
		return
	}
	// Suicidal posts are never judged as slop or counted: only the support reply.
	if v.Risk >= a.cfg.RiskThreshold {
		log.Printf("chat %d, user %d (@%s): self-harm risk %d", chatID, userID, msg.From.UserName, v.Risk)
		a.replySupport(msg)
		return
	}
	log.Printf("chat %d, user %d (@%s): score %d, risk %d, people %d — %s", chatID, userID, msg.From.UserName, v.Score, v.Risk, len(v.People), v.Reason)

	if wantPeople {
		a.countPeople(msg, v.People)
	}

	if v.Score < a.cfg.Threshold {
		// Only clearly human messages become the style baseline, so near-threshold
		// slop can't gradually shift what counts as "normal" for this user.
		if !edited && v.Score < a.cfg.Threshold/2 {
			a.hist.Add(chatID, userID, text)
		}
		return
	}
	if a.hist.MarkFlagged(chatID, msg.MessageID) {
		a.replyWithImage(msg, fmt.Sprintf("🚨 Нейрослоп детектед 🚨\nОценка: %d%%\n%s", v.Score, v.Reason))
	}
}

// openStores picks the storage: Redis when REDIS_URL is set (state survives
// restarts and works in Docker), otherwise memory plus a JSON file.
func openStores(cfg config.Config) (history.Store, mencount.Store) {
	if cfg.RedisURL == "" {
		men, err := mencount.OpenFile(cfg.MenCounterFile)
		if err != nil {
			log.Fatalf("men counter %s: %v", cfg.MenCounterFile, err)
		}
		log.Printf("storage: memory + %s (set REDIS_URL to use Redis)", cfg.MenCounterFile)
		return history.NewMemory(cfg.HistorySize), men
	}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("REDIS_URL: %v", err)
	}
	rc := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rc.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis %s: %v", opts.Addr, err)
	}
	log.Printf("storage: redis %s", opts.Addr)
	return history.NewRedis(rc, cfg.HistorySize), mencount.NewRedis(rc)
}

// acquireModel waits for a free model slot. The returned release keeps the
// slot busy for a cooldown proportional to how long the request took, so the
// model runs at most LLM_MAX_LOAD percent of the time: e.g. at 50% a 3-second
// check is followed by a 3-second pause. This caps sustained CPU/GPU load (and
// heat) without dropping messages — they just wait their turn.
func (a *app) acquireModel(ctx context.Context) (release func(), ok bool) {
	if a.hist.ModelPaused() {
		a.debugf("model paused by thermal guard, skipping check")
		return nil, false
	}
	select {
	case a.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, false
	}
	start := time.Now()
	return func() {
		busy := time.Since(start)
		pause := time.Duration(float64(busy) * (100/float64(a.cfg.MaxLoad) - 1))
		go func() {
			time.Sleep(pause)
			<-a.sem
		}()
	}, true
}

// countRomance runs a people-only request (for messages that don't get the
// slop check) and counts the result.
func (a *app) countRomance(ctx context.Context, msg *tgbotapi.Message, text string) {
	release, ok := a.acquireModel(ctx)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	v, err := a.det.Romance(ctx, text)
	if err != nil {
		log.Printf("romance check message %d: %v", msg.MessageID, err)
		return
	}
	a.debugf("romance msg %d: %d people — %s", msg.MessageID, len(v.People), v.Reason)
	a.countPeople(msg, v.People)
}

// countPeople bumps the author's per-person counters and announces them.
func (a *app) countPeople(msg *tgbotapi.Message, people []detector.Person) {
	if len(people) == 0 {
		return
	}
	author := displayName(msg.From)
	var lines []string
	var e mencount.Entry
	for _, p := range people {
		var n int
		var err error
		e, n, err = a.men.Add(msg.Chat.ID, msg.From.ID, author, p.Name, p.Gender)
		if err != nil {
			log.Printf("men counter save: %v", err)
		}
		icon := "👨"
		if p.Gender == "f" {
			icon = "👩"
		}
		lines = append(lines, fmt.Sprintf("%s %s — %d %s", icon, p.Name, n, times(n)))
		log.Printf("chat %d, user %d (@%s): mention of %s (%s, %s) #%d",
			msg.Chat.ID, msg.From.ID, msg.From.UserName, p.Name, p.Gender, p.Context, n)
	}
	a.reply(msg, fmt.Sprintf("💘 Упоминание засчитано!\n%s\n\nВсе упоминания %s:\n%s\n\nРейтинг чата: /men",
		strings.Join(lines, "\n"), author, namesByGender(e, 10)))
}

// replyMenTop shows the chat's leaderboard with each member's top names.
func (a *app) replyMenTop(msg *tgbotapi.Message) {
	top, err := a.men.Top(msg.Chat.ID)
	if err != nil {
		log.Printf("men counter read: %v", err)
		return
	}
	if len(top) == 0 {
		a.reply(msg, "💘 В этом чате пока никто никого не упоминал по имени в личном контексте.")
		return
	}
	var sb strings.Builder
	sb.WriteString("💘 Счётчик крашей — кто кого сколько раз упоминал:\n")
	medals := []string{"🥇", "🥈", "🥉"}
	for i, e := range top {
		if i >= 15 {
			break
		}
		place := fmt.Sprintf("%d.", i+1)
		if i < len(medals) {
			place = medals[i]
		}
		fmt.Fprintf(&sb, "\n\n%s %s\n%s", place, e.Name, namesByGender(e, 5))
	}
	a.reply(msg, sb.String())
}

// namesByGender lists a member's people with counts, one line per gender:
// "👨 Дима 3, Петя 1 (всего 4)\n👩 Маша 2 (всего 2)".
func namesByGender(e mencount.Entry, limit int) string {
	var lines []string
	for _, g := range []struct{ code, icon string }{{"m", "👨"}, {"f", "👩"}} {
		ranked := e.Ranked(g.code)
		if len(ranked) == 0 {
			continue
		}
		var parts []string
		total := 0
		for i, n := range ranked {
			total += n.Count
			if i < limit {
				parts = append(parts, fmt.Sprintf("%s %d", n.Name, n.Count))
			}
		}
		if len(ranked) > limit {
			parts = append(parts, fmt.Sprintf("и ещё %d", len(ranked)-limit))
		}
		lines = append(lines, fmt.Sprintf("%s %s (всего %d)", g.icon, strings.Join(parts, ", "), total))
	}
	return strings.Join(lines, "\n")
}

// times picks the Russian plural form of «раз» for n.
func times(n int) string {
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14) {
		return "раза"
	}
	return "раз"
}

func displayName(u *tgbotapi.User) string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" && u.UserName != "" {
		name = "@" + u.UserName
	}
	if name == "" {
		name = fmt.Sprint(u.ID)
	}
	return name
}

// replySupport sends the support message, at most once per supportCooldown per
// person in a chat, so repeated messages or edits don't turn into spam.
func (a *app) replySupport(to *tgbotapi.Message) {
	if !a.hist.Cooldown("support", to.Chat.ID, to.From.ID, supportCooldown) {
		a.debugf("support for msg %d skipped: cooldown", to.MessageID)
		return
	}

	name := to.From.FirstName
	if name == "" {
		name = "Привет"
	}
	a.replyWithImage(to, fmt.Sprintf(supportText, name))
}

// replyWithImage answers with the configured picture and the text (used for both
// the slop verdict and the support message). A .webp file is sent as a sticker
// (keeps transparency; stickers have no caption, so the text follows as a reply
// to the sticker), anything else as a photo with the text as caption. Falls back to plain text if there is no picture or it fails.
func (a *app) replyWithImage(to *tgbotapi.Message, caption string) {
	if a.cfg.SlopImage == "" {
		a.reply(to, caption)
		return
	}
	a.imageMu.Lock()
	defer a.imageMu.Unlock()

	var file tgbotapi.RequestFileData = tgbotapi.FilePath(a.cfg.SlopImage)
	if a.imageID != "" {
		file = tgbotapi.FileID(a.imageID)
	}
	sticker := strings.EqualFold(filepath.Ext(a.cfg.SlopImage), ".webp")

	var c tgbotapi.Chattable
	if sticker {
		st := tgbotapi.NewSticker(to.Chat.ID, file)
		st.ReplyToMessageID = to.MessageID
		c = st
	} else {
		p := tgbotapi.NewPhoto(to.Chat.ID, file)
		p.Caption = caption // both texts stay well under the 1024-rune caption limit
		p.ReplyToMessageID = to.MessageID
		c = p
	}
	sent, err := a.bot.Send(c)
	if err != nil {
		log.Printf("send image: %s", a.redact(err.Error()))
		a.reply(to, caption)
		return
	}
	if a.imageID == "" {
		switch {
		case sent.Sticker != nil:
			a.imageID = sent.Sticker.FileID
		case len(sent.Photo) > 0:
			a.imageID = sent.Photo[len(sent.Photo)-1].FileID
		}
	}
	if sticker {
		a.reply(&sent, caption)
	}
}

func (a *app) reply(to *tgbotapi.Message, text string) {
	m := tgbotapi.NewMessage(to.Chat.ID, text)
	m.ReplyToMessageID = to.MessageID
	if _, err := a.bot.Send(m); err != nil {
		log.Printf("send reply: %s", a.redact(err.Error()))
	}
}
