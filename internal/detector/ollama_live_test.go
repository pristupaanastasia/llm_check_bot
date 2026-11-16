package detector

import (
	"context"
	"os"
	"sort"
	"strings"
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
	o := liveOllama(model)
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
		v, err := o.Check(ctx, text, nil, false)
		cancel()
		coolDown(start)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: score %d, risk %d (%s) — %s", name, v.Score, v.Risk, time.Since(start).Round(100*time.Millisecond), v.Reason)
	}
}

func TestOllamaRomanceLive(t *testing.T) {
	if os.Getenv("OLLAMA_LIVE") == "" {
		t.Skip("set OLLAMA_LIVE=1 to run against a local Ollama")
	}
	o := liveOllama(liveModel())
	cases := map[string]string{ // text -> expected "name/g" list
		"кажется я влюбилась в Диму из соседнего отдела":  "Дима/m",
		"бывший опять написал, скучаю по нему":            "",
		"Дима починил кран наконец-то":                    "",
		"влюбился в Машу с курсов, зову на свидание":      "Маша/f",
		"моя девушка опять ревнует":                       "",
		"Катя сказала, что я ей нравлюсь, а Петя ревнует": "Катя/f Петя/m",
		"Аня из бухгалтерии принесла отчёт":               "",
		"мы с Димой идём в кино":                          "Дима/m",
		"с ваней набила бы хоть на лбу":                   "Ваня/m",
		"лежим с Сашей, смотрим сериал":                   "Саша/m",
		"созвон с Олегом в 10 по проекту":                 "",
		"еду с братом Ваней к родителям":                  "",
		"скучаю по Диме, а Серёжа зовёт на свидание":      "Дима/m Серёжа/m",
	}
	for text, want := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		start := time.Now()
		v, err := o.Romance(ctx, text)
		cancel()
		coolDown(start)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		var got []string
		for _, p := range v.People {
			got = append(got, p.Name+"/"+p.Gender)
		}
		sort.Strings(got)
		g := strings.Join(got, " ")
		mark := "ok "
		if g != want {
			mark = "MISS"
		}
		t.Logf("%s got %q want %q — %q — %s", mark, g, want, text, v.Reason)
	}
}

func liveModel() string {
	if m := os.Getenv("OLLAMA_MODEL"); m != "" {
		return m
	}
	return "qwen3:14b"
}

// Combined slop+people request: OLLAMA_LIVE=1 go test ./internal/detector -run CombinedLive -v
func TestOllamaCombinedLive(t *testing.T) {
	if os.Getenv("OLLAMA_LIVE") == "" {
		t.Skip("set OLLAMA_LIVE=1 to run against a local Ollama")
	}
	o := liveOllama(liveModel())
	cases := []string{
		"Отличный вопрос! Давайте разберёмся: отношения с Димой — это не просто романтика, а про доверие и уважение.",
		"короче мы с Сашей вчера лежали смотрели сериал до трёх ночи, он такой милый, а потом он написал что скучает",
		"созвон с Олегом перенесли на завтра, отчёт пока не готов, Аня обещала доделать к вечеру",
	}
	for _, text := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		start := time.Now()
		v, err := o.Check(ctx, text, nil, true)
		cancel()
		coolDown(start)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		var ppl []string
		for _, p := range v.People {
			ppl = append(ppl, p.Name+"/"+p.Gender+"/"+p.Context)
		}
		t.Logf("score %d, risk %d, people %v (%s) — %q", v.Score, v.Risk, ppl, time.Since(start).Round(100*time.Millisecond), text)
	}
}

// liveOllama is a client limited like the bot: 4 CPU threads.
func liveOllama(model string) *Ollama {
	o := NewOllama("http://localhost:11434", model, "2m")
	o.NumThread = 4
	o.Think = os.Getenv("OLLAMA_THINK") != ""
	return o
}

// coolDown pauses as long as the last request took (50% load), so live tests
// don't keep the machine at full load.
func coolDown(start time.Time) {
	time.Sleep(time.Since(start))
}
