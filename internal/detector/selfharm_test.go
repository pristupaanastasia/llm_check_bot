package detector

import "testing"

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
