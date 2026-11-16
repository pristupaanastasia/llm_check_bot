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

	"github.com/pristupaanastasia/llm_check_bot/internal/config"
	"github.com/pristupaanastasia/llm_check_bot/internal/detector"
	"github.com/pristupaanastasia/llm_check_bot/internal/history"
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
	hist   *history.Store
	cfg    config.Config
	redact func(string) string

	sem chan struct{} // limits parallel LLM requests

	// imageMu guards imageID: after the first upload Telegram returns a file_id,
	// and later replies reuse it instead of uploading the file again.
	imageMu sync.Mutex
	imageID string

	// supportMu guards lastSupport: when each (chat, user) last got a support reply.
	supportMu   sync.Mutex
	lastSupport map[[2]int64]time.Time

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
	log.Printf("authorized as @%s, %s model %s, threshold %d", bot.Self.UserName, cfg.Provider, cfg.Model, cfg.Threshold)
	if cfg.SlopImage != "" {
		if _, err := os.Stat(cfg.SlopImage); err != nil {
			log.Printf("slop image %q not found, replying with text only", cfg.SlopImage)
			cfg.SlopImage = ""
		}
	}

	var det detector.Checker
	if cfg.Provider == "claude" {
		det = detector.NewClaude(cfg.Model)
	} else {
		det = detector.NewOllama(cfg.OllamaURL, cfg.Model)
	}

	a := &app{
		bot:       bot,
		det:       det,
		hist:      history.New(cfg.HistorySize),
		cfg:       cfg,
		redact:    client.redact,
		sem:       make(chan struct{}, cfg.MaxConcurrent),
		userLocks: map[[2]int64]*sync.Mutex{},

		lastSupport: map[[2]int64]time.Time{},
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
	// Self-harm phrases are checked for everyone and regardless of length.
	if t := msg.Text + " " + msg.Caption; !isForward(msg) && detector.MentionsSelfHarm(t) {
		log.Printf("chat %d, user %d (@%s): self-harm phrase", msg.Chat.ID, msg.From.ID, msg.From.UserName)
		a.replySupport(msg)
		return
	}
	if !a.cfg.IsWatched(msg.From.ID, msg.From.UserName) {
		a.debugf("skip msg %d: user %d (@%s) is not in WATCHED_USERS", msg.MessageID, msg.From.ID, msg.From.UserName)
		return
	}
	// Forwarded text was written by someone else: neither judge it nor learn from it.
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

	if utf8.RuneCountInString(text) < a.cfg.MinLength {
		// Short replies are almost always typed by hand: the best style sample.
		if !edited {
			a.hist.Add(chatID, userID, text)
		}
		a.debugf("skip msg %d: shorter than MIN_MESSAGE_LENGTH=%d, saved as style sample", msg.MessageID, a.cfg.MinLength)
		return
	}
	// Already replied to this message: re-checking an edit would only cost money.
	if edited && a.hist.IsFlagged(chatID, msg.MessageID) {
		a.debugf("skip msg %d: edit of already flagged message", msg.MessageID)
		return
	}
	a.debugf("checking msg %d with %s", msg.MessageID, a.cfg.Model)

	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	v, err := a.det.Check(ctx, text, a.hist.Recent(chatID, userID))
	if err != nil {
		if errors.Is(err, detector.ErrRefused) {
			log.Printf("check message %d from %d: model refused", msg.MessageID, userID)
		} else {
			log.Printf("check message %d from %d: %v", msg.MessageID, userID, err)
		}
		return
	}
	// Suicidal posts are never judged as slop: only the support reply.
	if v.Risk >= a.cfg.RiskThreshold {
		log.Printf("chat %d, user %d (@%s): self-harm risk %d", chatID, userID, msg.From.UserName, v.Risk)
		a.replySupport(msg)
		return
	}
	log.Printf("chat %d, user %d (@%s): score %d, risk %d — %s", chatID, userID, msg.From.UserName, v.Score, v.Risk, v.Reason)

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

// replySupport sends the support message, at most once per supportCooldown per
// person in a chat, so repeated messages or edits don't turn into spam.
func (a *app) replySupport(to *tgbotapi.Message) {
	k := [2]int64{to.Chat.ID, to.From.ID}
	a.supportMu.Lock()
	if time.Since(a.lastSupport[k]) < supportCooldown {
		a.supportMu.Unlock()
		a.debugf("support for msg %d skipped: cooldown", to.MessageID)
		return
	}
	a.lastSupport[k] = time.Now()
	a.supportMu.Unlock()

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
