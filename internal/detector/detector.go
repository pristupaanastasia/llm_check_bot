// Package detector asks Claude whether a message looks like AI-generated "slop".
package detector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

const systemPrompt = `Ты — детектор «нейрослопа» в сообщениях из Telegram-чата.
Нейрослоп — текст, который человек сгенерировал нейросетью и вставил в чат почти без правок, вместо того чтобы написать сам.

Типичные признаки:
- канцелярский «ассистентский» тон, вежливые вступления и выводы («Отличный вопрос!», «В заключение», «Важно отметить»);
- маркированные списки и заголовки там, где люди в чате пишут обычным текстом;
- шаблонные обороты: «не просто X, а Y», «давайте разберёмся», «это не только..., но и...», тройки однородных прилагательных;
- обилие длинных тире, эмодзи-маркеров, жирного шрифта;
- общие слова без конкретики, личного опыта, опечаток и разговорных сокращений;
- ответ звучит как справка или эссе, а не как реплика в переписке.

Если даны прошлые сообщения этого же автора — это образец его обычного стиля. Резкое отличие от него (вдруг идеальная пунктуация, списки, «ассистентский» тон у человека, который обычно пишет коротко и разговорно) — сильный признак слопа. Если автор всегда так пишет, это его стиль, а не слоп.

Не считай слопом: короткие живые реплики, мат и сленг, опечатки, ссылки, цитаты, код, пересланные новости, тексты явно человеческого стиля.
Сомневаешься — ставь умеренный балл, не завышай.

Оцени сообщение по шкале 0–100 (100 — точно сгенерировано нейросетью) и кратко (1–2 предложения, по-русски) объясни, по каким признакам.`

// Verdict is the classifier's answer.
type Verdict struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

var verdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"score":  map[string]any{"type": "integer", "description": "0-100, вероятность что текст сгенерирован нейросетью"},
		"reason": map[string]any{"type": "string", "description": "Короткое объяснение на русском"},
	},
	"required":             []string{"score", "reason"},
	"additionalProperties": false,
}

// ErrRefused is returned when the model declines to classify the message.
var ErrRefused = errors.New("model refused to classify the message")

type Detector struct {
	client anthropic.Client
	model  string
}

// New creates a detector. The client reads ANTHROPIC_API_KEY from the environment.
func New(model string) *Detector {
	return &Detector{client: anthropic.NewClient(), model: model}
}

// Check classifies text. history holds the author's earlier messages (oldest first)
// and may be empty.
func (d *Detector) Check(ctx context.Context, text string, history []string) (Verdict, error) {
	resp, err := d.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     d.model,
		MaxTokens: 2048,
		System: []anthropic.BetaTextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(buildPrompt(text, history))),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: verdictSchema},
		},
		// If a safety classifier declines the request, the server retries it on a fallback model.
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("claude request: %w", err)
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return Verdict{}, ErrRefused
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(t.Text)
		}
	}

	var v Verdict
	if err := json.Unmarshal([]byte(sb.String()), &v); err != nil {
		return Verdict{}, fmt.Errorf("parse verdict %q: %w", sb.String(), err)
	}
	v.Score = max(0, min(100, v.Score))
	return v, nil
}

func buildPrompt(text string, history []string) string {
	var sb strings.Builder
	if len(history) > 0 {
		sb.WriteString("Прошлые сообщения этого автора (образец его стиля):\n<history>\n")
		for _, h := range history {
			sb.WriteString("<m>")
			sb.WriteString(h)
			sb.WriteString("</m>\n")
		}
		sb.WriteString("</history>\n\n")
	}
	sb.WriteString("Сообщение для проверки:\n<message>\n")
	sb.WriteString(text)
	sb.WriteString("\n</message>")
	return sb.String()
}
