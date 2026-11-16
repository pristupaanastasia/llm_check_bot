package detector

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs against a real Ollama: OLLAMA_LIVE=1 go test ./internal/detector -run Live -v
func TestOllamaLive(t *testing.T) {
	if os.Getenv("OLLAMA_LIVE") == "" {
		t.Skip("set OLLAMA_LIVE=1 to run against a local Ollama")
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "qwen3:14b"
	}
	o := NewOllama("http://localhost:11434", model)
	cases := map[string]string{
		"slop":          "Отличный вопрос! Давайте разберёмся. Важно отметить, что выбор фреймворка — это не просто технический вопрос, а стратегическое решение, которое влияет на масштабируемость, поддерживаемость и производительность. В заключение: всё зависит от ваших целей.",
		"human":         "да хз, я вчера пробовал на го переписать, вроде норм работает но с горутинами напортачил немного, завтра гляну ещё",
		"short-slop":    "Отличная работа, проверка на слоп это не про контроль - это про уважение к пользователям",
		"human-polite":  "Спасибо, что помог вчера с переездом, без тебя бы до ночи таскали коробки",
		"risk-hint":     "короче я всем раздала свои вещи, спасибо что были рядом, дальше без меня как-нибудь",
		"risk-joke":     "убейте меня, опять понедельник и созвон в 9 утра, я этого не переживу ахах",
		"human-opinion": "мне кажется это не про деньги вообще, он просто обиделся что его не позвали",
	}
	for name, text := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		start := time.Now()
		v, err := o.Check(ctx, text, nil)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: score %d, risk %d (%s) — %s", name, v.Score, v.Risk, time.Since(start).Round(100*time.Millisecond), v.Reason)
	}
}
