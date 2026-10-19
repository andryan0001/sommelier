// Package llm talks to any hosted or local chat model through one of two wire
// formats: Anthropic's /v1/messages or the OpenAI-compatible
// /chat/completions. That covers OpenAI, OpenRouter, NVIDIA NIM, Groq,
// DeepSeek, Mistral, xAI, Together, Fireworks, vLLM, Ollama and LM Studio.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Preset is a known endpoint plus its default model and key variable.
type Preset struct {
	Kind    string
	BaseURL string
	KeyEnv  string
	Model   string
}

// Presets maps a provider name to everything needed to call it.
var Presets = map[string]Preset{
	"anthropic": {Kind: "anthropic", BaseURL: "https://api.anthropic.com", KeyEnv: "ANTHROPIC_API_KEY", Model: "claude-sonnet-4-5"},
	"openai":    {Kind: "openai", BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY", Model: "gpt-4o-mini"},
	"openrouter": {Kind: "openai", BaseURL: "https://openrouter.ai/api/v1", KeyEnv: "OPENROUTER_API_KEY",
		Model: "anthropic/claude-sonnet-4.5"},
	"nvidia":  {Kind: "openai", BaseURL: "https://integrate.api.nvidia.com/v1", KeyEnv: "NVIDIA_API_KEY", Model: "meta/llama-3.3-70b-instruct"},
	"groq":    {Kind: "openai", BaseURL: "https://api.groq.com/openai/v1", KeyEnv: "GROQ_API_KEY", Model: "llama-3.3-70b-versatile"},
	"deepseek": {Kind: "openai", BaseURL: "https://api.deepseek.com/v1", KeyEnv: "DEEPSEEK_API_KEY", Model: "deepseek-chat"},
	"mistral": {Kind: "openai", BaseURL: "https://api.mistral.ai/v1", KeyEnv: "MISTRAL_API_KEY", Model: "mistral-large-latest"},
	"xai":     {Kind: "openai", BaseURL: "https://api.x.ai/v1", KeyEnv: "XAI_API_KEY", Model: "grok-2-latest"},
	"together": {Kind: "openai", BaseURL: "https://api.together.xyz/v1", KeyEnv: "TOGETHER_API_KEY",
		Model: "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
	"ollama":   {Kind: "openai", BaseURL: "http://localhost:11434/v1", Model: "llama3.1"},
	"lmstudio": {Kind: "openai", BaseURL: "http://localhost:1234/v1", Model: "local-model"},
}

// ProviderNames lists presets in a stable order for help output.
var ProviderNames = []string{
	"anthropic", "openai", "openrouter", "nvidia", "groq",
	"deepseek", "mistral", "xai", "together", "ollama", "lmstudio",
}

// autodetect is the order tried when no provider is named: first key wins.
var autodetect = []string{
	"openrouter", "anthropic", "openai", "nvidia", "groq",
	"deepseek", "mistral", "xai", "together",
}

// Error is a provider failure with the upstream status and body.
type Error struct {
	Provider string
	Status   int
	Body     string
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Provider + ": " + e.Body
	}
	return fmt.Sprintf("%s: HTTP %d — %s", e.Provider, e.Status, e.Body)
}

// Options configures a Client.
type Options struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKey    string
	MaxTokens int
	Timeout   time.Duration
}

// Client is one configured model endpoint.
type Client struct {
	Provider  string
	Kind      string
	BaseURL   string
	Model     string
	Key       string
	MaxTokens int

	http *http.Client
}

// Detect returns the first provider whose key is present in the environment.
func Detect() (string, bool) {
	for _, name := range autodetect {
		if p, ok := Presets[name]; ok && p.KeyEnv != "" && os.Getenv(p.KeyEnv) != "" {
			return name, true
		}
	}
	return "", false
}

// New resolves the provider, endpoint, model and key. It returns an error only
// when no usable endpoint can be derived, which lets the caller fall back to
// heuristics.
func New(opts Options) (*Client, error) {
	provider := opts.Provider
	// A custom endpoint defines its own wire format: do not let an unrelated
	// key in the environment override the kind inferred from the URL.
	if provider == "" && opts.BaseURL == "" {
		if detected, ok := Detect(); ok {
			provider = detected
		}
	}

	preset, known := Presets[provider]
	if !known && opts.BaseURL == "" {
		if provider == "" {
			return nil, &Error{Provider: "llm", Body: "no provider key found; set OPENROUTER_API_KEY, ANTHROPIC_API_KEY, OPENAI_API_KEY or pass --provider/--base-url"}
		}
		return nil, &Error{Provider: provider, Body: "unknown provider; known: " + strings.Join(ProviderNames, ", ")}
	}

	kind := preset.Kind
	if !known {
		if strings.Contains(strings.ToLower(opts.BaseURL), "anthropic") {
			kind = "anthropic"
		} else {
			kind = "openai"
		}
	}

	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = preset.BaseURL
	}

	model := opts.Model
	if model == "" {
		model = preset.Model
	}
	if model == "" {
		return nil, &Error{Provider: provider, Body: "no model given; pass --model"}
	}

	key := opts.APIKey
	if key == "" && preset.KeyEnv != "" {
		key = os.Getenv(preset.KeyEnv)
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1500
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if provider == "" {
		provider = kind
	}

	return &Client{
		Provider:  provider,
		Kind:      kind,
		BaseURL:   base,
		Model:     model,
		Key:       key,
		MaxTokens: maxTokens,
		http:      &http.Client{Timeout: timeout},
	}, nil
}

// Complete sends one system+user turn and returns the text response.
func (c *Client) Complete(system, prompt string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = c.MaxTokens
	}
	if c.Kind == "anthropic" {
		return c.anthropic(system, prompt, maxTokens)
	}
	return c.openai(system, prompt, maxTokens)
}

// CompleteJSON asks for a response and parses the first JSON value in it.
func (c *Client) CompleteJSON(system, prompt string, maxTokens int) (any, error) {
	text, err := c.Complete(system, prompt, maxTokens)
	if err != nil {
		return nil, err
	}
	return ExtractJSON(text)
}

func (c *Client) anthropic(system, prompt string, maxTokens int) (string, error) {
	body := map[string]any{
		"model":      c.Model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	headers := map[string]string{"anthropic-version": "2023-06-01"}
	if c.Key != "" {
		headers["x-api-key"] = c.Key
	}
	data, err := c.post(c.BaseURL+"/v1/messages", headers, body)
	if err != nil {
		return "", err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", &Error{Provider: c.Provider, Body: "unreadable response: " + snippet(data)}
	}
	var out strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}

func (c *Client) openai(system, prompt string, maxTokens int) (string, error) {
	body := map[string]any{
		"model":      c.Model,
		"max_tokens": maxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": prompt},
		},
	}
	headers := map[string]string{}
	if c.Key != "" {
		headers["Authorization"] = "Bearer " + c.Key
	}
	data, err := c.post(c.BaseURL+"/chat/completions", headers, body)
	if err != nil {
		return "", err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", &Error{Provider: c.Provider, Body: "unreadable response: " + snippet(data)}
	}
	if len(resp.Choices) == 0 {
		return "", &Error{Provider: c.Provider, Body: "no choices in response: " + snippet(data)}
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) post(url string, headers map[string]string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Provider: c.Provider, Body: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.http.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &Error{Provider: c.Provider, Body: err.Error()}
	}
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Provider: c.Provider, Body: err.Error()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &Error{Provider: c.Provider, Status: resp.StatusCode, Body: err.Error()}
	}
	if resp.StatusCode >= 400 {
		return nil, &Error{Provider: c.Provider, Status: resp.StatusCode, Body: snippet(data)}
	}
	return data, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// ExtractJSON pulls the first complete JSON value out of a model response.
// Models wrap output in prose or fences often enough that a bare Unmarshal is
// not sufficient; a streaming decoder stops at the end of the first value, so
// trailing commentary is ignored.
func ExtractJSON(text string) (any, error) {
	text = stripFences(text)
	for i := 0; i < len(text); i++ {
		if text[i] != '{' && text[i] != '[' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err == nil {
			return v, nil
		}
	}
	return nil, &Error{Provider: "llm", Body: "no JSON value in response: " + snippet([]byte(text))}
}

func stripFences(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if idx := strings.Index(trimmed, "\n"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	} else {
		return trimmed
	}
	if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimSpace(trimmed)
}
