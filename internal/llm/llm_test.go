package llm

import (
	"encoding/json"
	"testing"
)

func TestExtractJSONBareObject(t *testing.T) {
	v, err := ExtractJSON(`{"query": "a"}`)
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(map[string]any)
	if obj["query"] != "a" {
		t.Errorf("query = %v", obj["query"])
	}
}

func TestExtractJSONFencedArray(t *testing.T) {
	v, err := ExtractJSON("```json\n[{\"id\": 1}]\n```")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected a 1-element array, got %#v", v)
	}
}

func TestExtractJSONSurroundedByProse(t *testing.T) {
	v, err := ExtractJSON("Sure!\n{\"query\": \"x\"}\nHope that helps.")
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["query"] != "x" {
		t.Errorf("got %#v", v)
	}
}

func TestExtractJSONRejectsGarbage(t *testing.T) {
	if _, err := ExtractJSON("no json here at all"); err == nil {
		t.Error("expected an error")
	}
}

func TestExtractJSONPrefersFirstBracket(t *testing.T) {
	// A leading { inside prose must not swallow a later top-level array.
	v, err := ExtractJSON(`use {braces} rarely: [{"id": 7}]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.([]any); !ok {
		t.Fatalf("expected an array, got %T", v)
	}
}

func TestExtractJSONKeepsNumbersExact(t *testing.T) {
	v, err := ExtractJSON(`{"score": 87}`)
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(map[string]any)
	num, ok := obj["score"].(json.Number)
	if !ok {
		t.Fatalf("expected json.Number, got %T", obj["score"])
	}
	if num.String() != "87" {
		t.Errorf("score = %s", num)
	}
}

func TestNewUnknownProviderWithoutBaseURL(t *testing.T) {
	if _, err := New(Options{Provider: "nope"}); err == nil {
		t.Error("expected an error for an unknown provider with no base url")
	}
}

func TestNewCustomBaseURLInfersKind(t *testing.T) {
	c, err := New(Options{BaseURL: "http://localhost:8080/v1", Model: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "openai" {
		t.Errorf("kind = %q, want openai", c.Kind)
	}
	if c.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("base = %q", c.BaseURL)
	}

	c, err = New(Options{BaseURL: "https://proxy.example/v1/anthropic", Model: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "anthropic" {
		t.Errorf("kind = %q, want anthropic", c.Kind)
	}
}

func TestNewRequiresModel(t *testing.T) {
	// Detection reads the real environment; clear every candidate key so this
	// test asserts the code path rather than the developer's shell.
	for _, name := range autodetect {
		t.Setenv(Presets[name].KeyEnv, "")
	}
	if _, err := New(Options{Provider: "anthropic", APIKey: "x"}); err != nil {
		t.Fatalf("preset should supply a model: %v", err)
	}
	if _, err := New(Options{BaseURL: "http://x/v1", APIKey: "k"}); err == nil {
		t.Error("expected an error when no model can be derived")
	}
}

func TestDetectFindsEnvKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-test")
	name, ok := Detect()
	if !ok || name != "openrouter" {
		t.Errorf("Detect() = %q, %t", name, ok)
	}
}
