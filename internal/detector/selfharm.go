package detector

import (
	"regexp"
	"strings"
)

// selfHarmRe matches explicit statements of suicidal intent, including the chat
// shorthand «я у с» («я убью себя»). Hyperbole like «убейте меня» or «умру от
// смеха» is intentionally not included.
var selfHarmRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:` + strings.Join([]string{
	`я\s+у\s+с`,
	`уб(?:ью|ить|иваю)\s+себя`,
	`поконч(?:у|ить|ил[аи]?)\s+с\s+собой`,
	`хочу\s+умереть`,
	`не\s+хочу\s+(?:больше\s+)?жить`,
	`жить\s+не\s+хочу`,
	`жить\s+не\s+хочется`,
	`суицид\p{L}*`,
	`самоубийств\p{L}*`,
	`выпил(?:юсь|иться)`,
	`повешусь`,
	`вскроюсь`,
	`лучше\s+бы\s+меня\s+не\s+было`,
}, "|") + `)(?:$|[^\p{L}])`)

// MentionsSelfHarm reports whether text contains an explicit phrase about
// suicide or self-harm.
func MentionsSelfHarm(text string) bool {
	return selfHarmRe.MatchString(strings.ReplaceAll(text, "ё", "е"))
}
