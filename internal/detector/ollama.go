package detector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Ollama checks messages with a local model served by Ollama (https://ollama.com).
type Ollama struct {
	// Think lets thinking models (Gemma 4, Qwen3) reason before answering:
	// more accurate, but several times slower and more load.
	Think bool

	// NumThread caps the CPU threads Ollama uses for this model (0 = Ollama's
	// default, which is all performance cores).
	NumThread int

	// noFormat is set once the model turns out not to support structured output.
	noFormat atomic.Bool

	url       string
	model     string
	keepAlive string
	http      *http.Client
}

// NewOllama creates an Ollama checker. keepAlive (e.g. "2m") is how long Ollama
// keeps the model in memory after a request; empty means Ollama's default.
func NewOllama(url, model, keepAlive string) *Ollama {
	return &Ollama{url: strings.TrimRight(url, "/"), model: model, keepAlive: keepAlive, http: &http.Client{}}
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	// Format constrains the output to the verdict JSON schema.
	Format any `json:"format,omitempty"`
	// Think=false turns off the reasoning phase of thinking models (Qwen3 etc.):
	// it is slow and not needed for a short score.
	Think bool `json:"think"`
	// KeepAlive unloads the model after this much idle time, freeing memory.
	KeepAlive string         `json:"keep_alive,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Error   string        `json:"error"`
}

func (o *Ollama) Check(ctx context.Context, text string, history []string, people bool) (Verdict, error) {
	return check(ctx, o, text, history, people)
}

func (o *Ollama) Romance(ctx context.Context, text string) (RomanceVerdict, error) {
	return romance(ctx, o, text)
}

func (o *Ollama) options() map[string]any {
	opts := map[string]any{"temperature": 0, "num_ctx": 8192}
	if o.NumThread > 0 {
		opts["num_thread"] = o.NumThread
	}
	return opts
}

// ask sends one system+user prompt and returns the model's JSON answer.
// Some engines (e.g. Ollama's MLX models) don't support structured output; for
// them the schema goes into the prompt instead and the JSON is cut out of the
// reply. The first 501 switches this client to that mode for good.
func (o *Ollama) ask(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	if !o.noFormat.Load() {
		out, err := o.chat(ctx, system, user, schema)
		if !errors.Is(err, errNoStructuredOutput) {
			return out, err
		}
		o.noFormat.Store(true)
	}
	system += "\n\nОтветь ТОЛЬКО одним JSON-объектом (не схемой!), без пояснений и без ```. Внутри текстовых значений не используй двойные кавычки — только «ёлочки». Пример формата ответа:\n" + exampleFor(schema)
	out, err := o.chat(ctx, system, user, nil)
	if err != nil {
		return "", err
	}
	js, err := extractJSON(out)
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(js)) {
		js = repairQuotes(js)
	}
	return js, nil
}

// repairQuotes escapes stray double quotes inside JSON strings, e.g.
// {"reason":"оборот ("не про X")"} — a common small-model mistake. A quote in
// a string counts as closing only if the next non-space char ends a value.
func repairQuotes(s string) string {
	var b strings.Builder
	in := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && in && i+1 < len(rs):
			b.WriteRune(c)
			i++
			b.WriteRune(rs[i])
			continue
		case c == '"' && !in:
			in = true
		case c == '"' && in:
			j := i + 1
			for j < len(rs) && (rs[j] == ' ' || rs[j] == '\n' || rs[j] == '\t' || rs[j] == '\r') {
				j++
			}
			if j < len(rs) && !strings.ContainsRune(",}]:", rs[j]) {
				b.WriteString(`\"`)
				continue
			}
			in = false
		}
		b.WriteRune(c)
	}
	return b.String()
}

var errNoStructuredOutput = errors.New("structured output is unavailable for this model")

func (o *Ollama) chat(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	req := ollamaRequest{
		Model: o.model,
		Messages: []ollamaMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		KeepAlive: o.keepAlive,
		Options:   o.options(),
		Think:     o.Think,
	}
	if schema != nil {
		req.Format = schema
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := o.http.Do(hreq)
	if err != nil {
		return "", fmt.Errorf("ollama request (is `ollama serve` running?): %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ollama read: %w", err)
	}

	var r ollamaResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("ollama %s: %s", resp.Status, raw)
	}
	if resp.StatusCode == http.StatusNotImplemented && strings.Contains(r.Error, "structured output") {
		return "", errNoStructuredOutput
	}
	if resp.StatusCode != http.StatusOK || r.Error != "" {
		return "", fmt.Errorf("ollama %s: %s", resp.Status, r.Error)
	}
	return r.Message.Content, nil
}

// exampleFor builds a sample answer from a schema — small models follow an
// example far better than a JSON Schema (they tend to echo the schema back).
func exampleFor(schema map[string]any) string {
	b, _ := json.Marshal(sampleValue(schema))
	return string(b)
}

func sampleValue(s map[string]any) any {
	switch s["type"] {
	case "object":
		obj := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		for k, v := range props {
			if sub, ok := v.(map[string]any); ok {
				obj[k] = sampleValue(sub)
			}
		}
		return obj
	case "array":
		if items, ok := s["items"].(map[string]any); ok {
			return []any{sampleValue(items)}
		}
		return []any{}
	case "integer":
		return 0
	case "boolean":
		return false
	default: // string, possibly an enum
		if enum, ok := s["enum"].([]string); ok && len(enum) > 0 {
			return strings.Join(enum, "|")
		}
		return "..."
	}
}

// extractJSON returns the outermost {...} object from a free-form reply
// (models sometimes wrap it in ```json fences or add a sentence).
func extractJSON(s string) (string, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("no JSON object in model reply %q", s)
	}
	return s[start : end+1], nil
}
