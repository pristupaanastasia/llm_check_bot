package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/pristupaanastasia/llm_check_bot/internal/config"
	"github.com/pristupaanastasia/llm_check_bot/internal/detector"
	"github.com/pristupaanastasia/llm_check_bot/internal/history"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatalf("telegram: %v", err)
	}
	log.Printf("authorized as @%s, model %s, threshold %d", bot.Self.UserName, cfg.Model, cfg.Threshold)

	det := detector.New(cfg.Model)
	hist := history.New(cfg.HistorySize)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			log.Println("shutting down")
			return
		case upd := <-updates:
			msg, edited := upd.Message, false
			if msg == nil {
				msg, edited = upd.EditedMessage, true
			}
			if msg == nil || msg.From == nil {
				continue
			}
			go handle(ctx, bot, det, hist, cfg, msg, edited)
		}
	}
}

func handle(ctx context.Context, bot *tgbotapi.BotAPI, det *detector.Detector, hist *history.Store, cfg config.Config, msg *tgbotapi.Message, edited bool) {
	if msg.Chat.IsPrivate() && msg.IsCommand() && msg.Command() == "start" {
		reply(bot, msg, fmt.Sprintf("Привет! Твой Telegram ID: %d. Добавь меня в группу, и я буду проверять сообщения отслеживаемых пользователей на нейрослоп.", msg.From.ID))
		return
	}
	if !cfg.IsWatched(msg.From.ID, msg.From.UserName) {
		return
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" || msg.IsCommand() {
		return
	}
	if utf8.RuneCountInString(text) < cfg.MinLength {
		// Short replies are almost always typed by hand: the best style sample.
		if !edited {
			hist.Add(msg.Chat.ID, msg.From.ID, text)
		}
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	v, err := det.Check(ctx, text, hist.Recent(msg.Chat.ID, msg.From.ID))
	if err != nil {
		if !errors.Is(err, detector.ErrRefused) {
			log.Printf("check message %d from %d: %v", msg.MessageID, msg.From.ID, err)
		}
		return
	}
	log.Printf("chat %d, user %d (@%s): score %d — %s", msg.Chat.ID, msg.From.ID, msg.From.UserName, v.Score, v.Reason)

	if v.Score < cfg.Threshold {
		if !edited {
			hist.Add(msg.Chat.ID, msg.From.ID, text)
		}
		return
	}
	if hist.MarkFlagged(msg.Chat.ID, msg.MessageID) {
		reply(bot, msg, fmt.Sprintf("🤖 Похоже на нейрослоп: %d%%\n%s", v.Score, v.Reason))
	}
}

func reply(bot *tgbotapi.BotAPI, to *tgbotapi.Message, text string) {
	m := tgbotapi.NewMessage(to.Chat.ID, text)
	m.ReplyToMessageID = to.MessageID
	if _, err := bot.Send(m); err != nil {
		log.Printf("send reply: %v", err)
	}
}
