package detector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMentionsSelfHarm(t *testing.T) {
	hits := []string{
		"я у с",
		"всё, я у с.",
		"Я У С",
		"иногда думаю что убью себя",
		"не хочу больше жить",
		"хочу умереть честно",
		"мысли о суициде",
	}
	misses := []string{
		"я у стены стою",
		"убейте меня, понедельник",
		"я умру от смеха",
	}
	for _, s := range hits {
		if !MentionsSelfHarm(s) {
			t.Errorf("expected hit: %q", s)
		}
	}
	for _, s := range misses {
		if MentionsSelfHarm(s) {
			t.Errorf("unexpected hit: %q", s)
		}
	}
}

func TestMaybeRomance(t *testing.T) {
	hits := []string{
		"мы с Димой идём в кино",
		"лежим с сашей смотрим сериал",
		"вчера Петя позвал гулять",
		"кажется влюбилась",
		"бывший опять написал",
		"Дима такой милый",
		"Костя ❤️",
		"кажется я запала на Олега",
	}
	misses := []string{
		"ну ок",
		"мы идём в магазин",  // no name
		"я написала отчёт",   // no name
		"Завтра созвон в 10", // capital only at sentence start
	}
	for _, s := range hits {
		if !MaybeRomance(s) {
			t.Errorf("expected prefilter hit: %q", s)
		}
	}
	for _, s := range misses {
		if MaybeRomance(s) {
			t.Errorf("unexpected prefilter hit: %q", s)
		}
	}
}

func TestDropRelatives(t *testing.T) {
	people := []Person{{"Ваня", "m", "together"}, {"Дима", "m", "together"}}
	got := dropRelatives("еду с братом Ваней и Димой", people)
	if len(got) != 1 || got[0].Name != "Дима" {
		t.Fatalf("got %+v", got)
	}
}

func TestExtractJSON(t *testing.T) {
	got, err := extractJSON("вот ответ:\n```json\n{\"score\": 5, \"reason\": \"ok\"}\n```")
	if err != nil || got != `{"score": 5, "reason": "ok"}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := extractJSON("нет json"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCleanPeopleDropsCommonNouns(t *testing.T) {
	got := cleanPeople([]Person{{"Бывший", "m", "love"}, {"девушка", "f", "love"}, {"Дима", "m", "love"}})
	if len(got) != 1 || got[0].Name != "Дима" {
		t.Fatalf("got %+v", got)
	}
}

func TestRepairQuotes(t *testing.T) {
	bad := `{"reason":"оборот ("это не просто X, а Y") и похвала","score":95}`
	got := repairQuotes(bad)
	var v struct {
		Reason string `json:"reason"`
		Score  int    `json:"score"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil || v.Score != 95 || !strings.Contains(v.Reason, `"это не просто`) {
		t.Fatalf("repair failed: %q → %+v, %v", got, v, err)
	}
}

func TestMentionsName(t *testing.T) {
	for _, s := range []string{"с ваней набила бы хоть на лбу", "сегодня видела Диму", "Настя опять опоздала", "написал Сергею", "с Игорем гуляли"} {
		if !MentionsName(s) || !MaybeRomance(s) {
			t.Errorf("expected name hit: %q", s)
		}
	}
	for _, s := range []string{"налила ванну", "сколько лет", "люди странные", "вали отсюда", "в поле", "любой вариант", "никак"} {
		if MentionsName(s) {
			t.Errorf("unexpected name hit: %q", s)
		}
	}
}
