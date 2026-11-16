package detector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Ollama checks messages with a local model served by Ollama (https://ollama.com).
type Ollama struct {
	url   string
	model string
	http  *http.Client
}

func NewOllama(url, model string) *Ollama {
	return &Ollama{url: strings.TrimRight(url, "/"), model: model, http: &http.Client{}}
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
	Format any `json:"format"`
	// Think=false turns off the reasoning phase of thinking models (Qwen3 etc.):
	// it is slow and not needed for a short score.
	Think   bool           `json:"think"`
	Options map[string]any `json:"options,omitempty"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Error   string        `json:"error"`
}

func (o *Ollama) Check(ctx context.Context, text string, history []string) (Verdict, error) {
	body, err := json.Marshal(ollamaRequest{
		Model: o.model,
		Messages: []ollamaMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildPrompt(text, history)},
		},
		Format:  verdictSchema,
		Options: map[string]any{"temperature": 0, "num_ctx": 8192},
	})
	if err != nil {
		return Verdict{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.http.Do(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("ollama request (is `ollama serve` running?): %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Verdict{}, fmt.Errorf("ollama read: %w", err)
	}

	var r ollamaResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return Verdict{}, fmt.Errorf("ollama %s: %s", resp.Status, raw)
	}
	if resp.StatusCode != http.StatusOK || r.Error != "" {
		return Verdict{}, fmt.Errorf("ollama %s: %s", resp.Status, r.Error)
	}
	return parseVerdict(r.Message.Content)
}
