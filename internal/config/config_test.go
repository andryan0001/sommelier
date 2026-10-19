package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTOMLSubset(t *testing.T) {
	text := `
# comment
[nhentai]
api_key = "abc"   # trailing comment
user_agent = 'sommelier/1.0'
exclude = ["lolicon", "guro"]

[search]
pages = 3
no_llm = true
`
	p, err := parseTOML(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := p.str("nhentai.api_key", ""); got != "abc" {
		t.Errorf("api_key = %q, want abc", got)
	}
	if got := p.str("nhentai.user_agent", ""); got != "sommelier/1.0" {
		t.Errorf("user_agent = %q", got)
	}
	if got := p.arrays["nhentai.exclude"]; len(got) != 2 || got[0] != "lolicon" {
		t.Errorf("exclude = %v", got)
	}
	if got := p.int("search.pages", 0); got != 3 {
		t.Errorf("pages = %d, want 3", got)
	}
	if !p.boolean("search.no_llm", false) {
		t.Error("no_llm should be true")
	}
}

func TestParseTOMLRejectsGarbage(t *testing.T) {
	if _, err := parseTOML("[search]\npages 3\n"); err == nil {
		t.Error("expected an error for a line without =")
	}
}

func TestStripCommentRespectsQuotes(t *testing.T) {
	if got := stripComment(`"a # b"`); got != `"a # b"` {
		t.Errorf("stripComment mangled a quoted hash: %q", got)
	}
	if got := stripComment(`value # note`); got != "value " {
		t.Errorf("stripComment = %q", got)
	}
}

func TestLoadAppliesFileThenEnv(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("SOMMELIER_PAGES", "9")
	t.Setenv("SOMMELIER_EXCLUDE", "guro, scat")

	dir := filepath.Join(home, ".sommelier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[search]\npages = 4\nshortlist = 7\n[nhentai]\nuser_agent = \"ua/1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Pages != 9 {
		t.Errorf("env should win: pages = %d, want 9", cfg.Pages)
	}
	if cfg.Shortlist != 7 {
		t.Errorf("file value lost: shortlist = %d, want 7", cfg.Shortlist)
	}
	if cfg.UserAgent != "ua/1" {
		t.Errorf("user agent = %q", cfg.UserAgent)
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != "guro" {
		t.Errorf("exclude = %v", cfg.Exclude)
	}
	if cfg.IndexPath != filepath.Join(dir, "index.jsonl") {
		t.Errorf("index path = %q", cfg.IndexPath)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	setHome(t, t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Pages != 2 || cfg.Shortlist != 12 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestExpand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if got := Expand("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("Expand(~/x) = %q", got)
	}
	if got := Expand(""); got != "" {
		t.Errorf("Expand(\"\") = %q", got)
	}
}

func TestWriteEscapesAndRestrictsMode(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	path, err := Write("openrouter", `model"x`, `key\with"quote`)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config mode = %o, want 600", mode)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseTOML(string(data))
	if err != nil {
		t.Fatalf("written file does not parse: %v", err)
	}
	if got := p.str("llm.provider", ""); got != "openrouter" {
		t.Errorf("provider = %q", got)
	}
	if got := p.str("llm.model", ""); got != `model"x` {
		t.Errorf("model = %q", got)
	}
	if got := p.str("llm.api_key", ""); got != `key\with"quote` {
		t.Errorf("api key = %q", got)
	}
}

func TestTemplateParses(t *testing.T) {
	if _, err := parseTOML(Template()); err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
}

// setHome points os.UserHomeDir at a temporary directory. On Unix that means
// HOME; the tests are platform-neutral so they also run under the Windows
// builder, where Go reads USERPROFILE first.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
