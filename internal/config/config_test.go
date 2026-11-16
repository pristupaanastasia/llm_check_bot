package config

import "testing"

func TestLoadValidates(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("WATCHED_USERS", "1,@Bob")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsWatched(1, "") || !c.IsWatched(2, "bob") || c.IsWatched(3, "alice") {
		t.Fatal("IsWatched mismatch")
	}

	for _, kv := range [][2]string{
		{"SLOP_THRESHOLD", "700"},
		{"SLOP_THRESHOLD", "0"},
		{"MIN_MESSAGE_LENGTH", "0"},
		{"STYLE_HISTORY", "-1"},
		{"MAX_CONCURRENT_CHECKS", "0"},
	} {
		t.Run(kv[0]+"="+kv[1], func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			if _, err := Load(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadRequiresAnthropicKey(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("WATCHED_USERS", "1")
	t.Setenv("LLM_PROVIDER", "claude")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestOllamaNeedsNoAnthropicKey(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("WATCHED_USERS", "1")
	t.Setenv("LLM_PROVIDER", "ollama")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "gemma4:e2b-mlx" {
		t.Fatalf("model = %q", c.Model)
	}
}

func TestWatchAll(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("LLM_PROVIDER", "ollama")
	for _, v := range []string{"", "*", " * "} {
		t.Setenv("WATCHED_USERS", v)
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !c.WatchAll || !c.IsWatched(42, "anyone") {
			t.Fatalf("WATCHED_USERS=%q should watch everyone", v)
		}
	}
}

func TestLoveCounterFlag(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("LLM_PROVIDER", "ollama")
	for v, want := range map[string]bool{"": true, "off": false, "0": false, "true": true, "ON": true} {
		t.Setenv("LOVE_COUNTER", v)
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if c.LoveCounter != want {
			t.Errorf("LOVE_COUNTER=%q → %v, want %v", v, c.LoveCounter, want)
		}
	}
}
