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

// checkTimeout bounds one Claude request; shutdownWait is how long we let
// in-flight checks finish after SIGINT/SIGTERM.
const (
	checkTimeout = 2 * time.Minute
	shutdownWait = 30 * time.Second
)

type app struct {
	bot    *tgbotapi.BotAPI
	det    *detector.Detector
	hist   *history.Store
	cfg    config.Config
	redact func(string) string

	sem chan struct{} // limits parallel Claude requests

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
	log.Printf("authorized as @%s, model %s, threshold %d", bot.Self.UserName, cfg.Model, cfg.Threshold)
	if cfg.SlopImage != "" {
		if _, err := os.Stat(cfg.SlopImage); err != nil {
			log.Printf("slop image %q not found, replying with text only", cfg.SlopImage)
			cfg.SlopImage = ""
		}
	}

	a := &app{
		bot:       bot,
		det:       detector.New(cfg.Model),
		hist:      history.New(cfg.HistorySize),
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

func isForward(m *tgbotapi.Message) bool {
	return m.ForwardDate != 0 || m.ForwardFrom != nil || m.ForwardFromChat != nil ||
		m.ForwardSenderName != "" || m.IsAutomaticForward
}

func (a *app) handle(ctx context.Context, msg *tgbotapi.Message, edited bool) {
	if msg.Chat.IsPrivate() && msg.IsCommand() && msg.Command() == "start" {
		a.reply(msg, fmt.Sprintf("Привет! Твой Telegram ID: %d. Добавь меня в группу, и я буду проверять сообщения отслеживаемых пользователей на нейрослоп.", msg.From.ID))
		return
	}
	if !a.cfg.IsWatched(msg.From.ID, msg.From.UserName) {
		return
	}
	// Forwarded text was written by someone else: neither judge it nor learn from it.
	if isForward(msg) || msg.IsCommand() {
		return
	}
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
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
		return
	}
	// Already replied to this message: re-checking an edit would only cost money.
	if edited && a.hist.IsFlagged(chatID, msg.MessageID) {
		return
	}

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
		if !errors.Is(err, detector.ErrRefused) {
			log.Printf("check message %d from %d: %v", msg.MessageID, userID, err)
		}
		return
	}
	log.Printf("chat %d, user %d (@%s): score %d — %s", chatID, userID, msg.From.UserName, v.Score, v.Reason)

	if v.Score < a.cfg.Threshold {
		// Only clearly human messages become the style baseline, so near-threshold
		// slop can't gradually shift what counts as "normal" for this user.
		if !edited && v.Score < a.cfg.Threshold/2 {
			a.hist.Add(chatID, userID, text)
		}
		return
	}
	if a.hist.MarkFlagged(chatID, msg.MessageID) {
		a.replySlop(msg, fmt.Sprintf("🤖 Похоже на нейрослоп: %d%%\n%s", v.Score, v.Reason))
	}
}

// replySlop answers with the configured picture and the verdict. A .webp file
// is sent as a sticker (keeps transparency; stickers have no caption, so the
// verdict follows as a reply to the sticker), anything else as a photo with the
// verdict as caption. Falls back to plain text if there is no picture or it fails.
func (a *app) replySlop(to *tgbotapi.Message, caption string) {
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
		p.Caption = caption // reason is capped at 300 runes, well under the 1024 caption limit
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
