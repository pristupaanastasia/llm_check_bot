package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	TelegramToken string
	// Provider is "ollama" (local model) or "claude".
	Provider  string
	Model     string
	OllamaURL string
	// WatchAll checks every human in the chat (WATCHED_USERS empty or "*").
	WatchAll bool
	// Watched users: numeric Telegram IDs and/or usernames (lowercase, without @).
	WatchedIDs       map[int64]bool
	WatchedUsernames map[string]bool
	// Messages scoring at or above Threshold get a reply.
	Threshold int
	// RiskThreshold: a model risk score at or above it triggers a support reply.
	RiskThreshold int
	// Messages shorter than MinLength runes are skipped: too little text to judge.
	MinLength int
	// HistorySize is how many recent messages per user are kept as a style sample.
	HistorySize int
	// MaxConcurrent limits parallel LLM requests.
	MaxConcurrent int
	// SlopImage is a picture sent with the verdict; empty means text only.
	SlopImage string
	// Debug logs every received message (metadata only) and why it was skipped.
	Debug bool
}

func Load() (Config, error) {
	c := Config{
		TelegramToken:    os.Getenv("TELEGRAM_BOT_TOKEN"),
		Provider:         strings.ToLower(envOr("LLM_PROVIDER", "ollama")),
		OllamaURL:        envOr("OLLAMA_URL", "http://localhost:11434"),
		SlopImage:        envOr("SLOP_IMAGE", "assets/sticker.webp"),
		Debug:            os.Getenv("DEBUG") != "",
		WatchedIDs:       map[int64]bool{},
		WatchedUsernames: map[string]bool{},
	}
	if c.TelegramToken == "" {
		return c, errors.New("TELEGRAM_BOT_TOKEN is not set")
	}
	switch c.Provider {
	case "ollama":
		c.Model = envOr("OLLAMA_MODEL", "qwen3:14b")
	case "claude":
		c.Model = envOr("CLAUDE_MODEL", "claude-opus-5-5")
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return c, errors.New("ANTHROPIC_API_KEY is not set")
		}
	default:
		return c, fmt.Errorf("LLM_PROVIDER must be ollama or claude, got %q", c.Provider)
	}

	for _, u := range strings.Split(os.Getenv("WATCHED_USERS"), ",") {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if u == "*" {
			c.WatchAll = true
			continue
		}
		if id, err := strconv.ParseInt(u, 10, 64); err == nil {
			c.WatchedIDs[id] = true
		} else {
			c.WatchedUsernames[strings.ToLower(strings.TrimPrefix(u, "@"))] = true
		}
	}
	if len(c.WatchedIDs)+len(c.WatchedUsernames) == 0 {
		c.WatchAll = true
	}

	var err error
	if c.Threshold, err = envInt("SLOP_THRESHOLD", 30); err != nil {
		return c, err
	}
	if c.MinLength, err = envInt("MIN_MESSAGE_LENGTH", 30); err != nil {
		return c, err
	}
	if c.RiskThreshold, err = envInt("SELF_HARM_THRESHOLD", 60); err != nil {
		return c, err
	}
	if c.HistorySize, err = envInt("STYLE_HISTORY", 20); err != nil {
		return c, err
	}
	if c.MaxConcurrent, err = envInt("MAX_CONCURRENT_CHECKS", 4); err != nil {
		return c, err
	}
	return c, c.validate()
}

func (c Config) validate() error {
	switch {
	case c.Threshold < 1 || c.Threshold > 100:
		return fmt.Errorf("SLOP_THRESHOLD must be 1..100, got %d", c.Threshold)
	case c.RiskThreshold < 1 || c.RiskThreshold > 100:
		return fmt.Errorf("SELF_HARM_THRESHOLD must be 1..100, got %d", c.RiskThreshold)
	case c.MinLength < 1:
		return fmt.Errorf("MIN_MESSAGE_LENGTH must be >= 1, got %d", c.MinLength)
	case c.HistorySize < 0 || c.HistorySize > 100:
		return fmt.Errorf("STYLE_HISTORY must be 0..100, got %d", c.HistorySize)
	case c.MaxConcurrent < 1 || c.MaxConcurrent > 64:
		return fmt.Errorf("MAX_CONCURRENT_CHECKS must be 1..64, got %d", c.MaxConcurrent)
	}
	return nil
}

func (c Config) IsWatched(id int64, username string) bool {
	if c.WatchAll {
		return true
	}
	return c.WatchedIDs[id] || (username != "" && c.WatchedUsernames[strings.ToLower(username)])
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
