// Package config loads settings from $HOME/.sommelier/config.toml.
//
// Environment variables override the file, and the file overrides defaults.
//
// The TOML reader is a deliberately small subset: sections, `key = value`,
// strings, integers, booleans and single-line string arrays. That covers every
// key sommelier writes itself, and keeping it hand-rolled means the binary has
// no dependencies at all — so it cross-compiles for every target with a plain
// `go build` and no module downloads.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds every tunable the CLI reads.
type Config struct {
	Path string

	// [nhentai]
	APIKey    string
	UserAgent string
	CacheDir  string
	Exclude   []string

	// [llm]
	Provider string
	Model    string
	BaseURL  string
	LLMKey   string

	// [search]
	Pages     int
	Shortlist int
	Sort      string
	MinPages  int
	MaxPages  int
	NoLLM     bool

	// [index]
	IndexPath string
	TagsPath  string
}

// Dir returns $HOME/.sommelier, creating nothing.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".sommelier")
}

// Default returns the built-in configuration, with paths under Dir().
func Default() *Config {
	dir := Dir()
	return &Config{
		Path:      filepath.Join(dir, "config.toml"),
		UserAgent: "sommelier/0.1.0 (+https://github.com/andryan0001/sommelier)",
		CacheDir:  filepath.Join(dir, "cache"),
		IndexPath: filepath.Join(dir, "index.jsonl"),
		TagsPath:  filepath.Join(dir, "tags.jsonl"),
		Pages:     2,
		Shortlist: 12,
		Sort:      "popular",
	}
}

// Exists reports whether a config file has been written yet.
func Exists() bool {
	_, err := os.Stat(filepath.Join(Dir(), "config.toml"))
	return err == nil
}

// Load reads the config file if present, then applies environment overrides.
func Load() (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(cfg.Path)
	switch {
	case err == nil:
		parsed, perr := parseTOML(string(data))
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", cfg.Path, perr)
		}
		cfg.applyFile(parsed)
	case !os.IsNotExist(err):
		return nil, err
	}
	cfg.applyEnv()
	cfg.expandPaths()
	return cfg, nil
}

func (c *Config) expandPaths() {
	c.CacheDir = Expand(c.CacheDir)
	c.IndexPath = Expand(c.IndexPath)
	c.TagsPath = Expand(c.TagsPath)
}

// Expand resolves a leading ~ and any $VARS in a path.
func Expand(p string) string {
	if p == "" {
		return p
	}
	p = os.ExpandEnv(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func (c *Config) applyFile(p *parsed) {
	c.APIKey = p.str("nhentai.api_key", c.APIKey)
	c.UserAgent = p.str("nhentai.user_agent", c.UserAgent)
	c.CacheDir = p.str("nhentai.cache_dir", c.CacheDir)
	if v, ok := p.arrays["nhentai.exclude"]; ok {
		c.Exclude = v
	}

	c.Provider = p.str("llm.provider", c.Provider)
	c.Model = p.str("llm.model", c.Model)
	c.BaseURL = p.str("llm.base_url", c.BaseURL)
	c.LLMKey = p.str("llm.api_key", c.LLMKey)

	c.Pages = p.int("search.pages", c.Pages)
	c.Shortlist = p.int("search.shortlist", c.Shortlist)
	c.Sort = p.str("search.sort", c.Sort)
	c.MinPages = p.int("search.min_pages", c.MinPages)
	c.MaxPages = p.int("search.max_pages", c.MaxPages)
	c.NoLLM = p.boolean("search.no_llm", c.NoLLM)

	c.IndexPath = p.str("index.path", c.IndexPath)
	c.TagsPath = p.str("index.tags_path", c.TagsPath)
}

func (c *Config) applyEnv() {
	c.APIKey = env("NHENTAI_API_KEY", c.APIKey)
	c.UserAgent = env("SOMMELIER_USER_AGENT", c.UserAgent)
	c.CacheDir = env("SOMMELIER_CACHE", c.CacheDir)

	c.Provider = env("SOMMELIER_PROVIDER", c.Provider)
	c.Model = env("SOMMELIER_MODEL", c.Model)
	c.BaseURL = env("SOMMELIER_BASE_URL", c.BaseURL)
	c.LLMKey = env("SOMMELIER_API_KEY", c.LLMKey)

	c.IndexPath = env("SOMMELIER_INDEX", c.IndexPath)
	c.TagsPath = env("SOMMELIER_TAGS", c.TagsPath)

	if v := os.Getenv("SOMMELIER_EXCLUDE"); v != "" {
		c.Exclude = splitList(v)
	}
	c.Pages = envInt("SOMMELIER_PAGES", c.Pages)
	c.Shortlist = envInt("SOMMELIER_SHORTLIST", c.Shortlist)
	c.Sort = env("SOMMELIER_SORT", c.Sort)
	c.MinPages = envInt("SOMMELIER_MIN_PAGES", c.MinPages)
	c.MaxPages = envInt("SOMMELIER_MAX_PAGES", c.MaxPages)
	if v := os.Getenv("SOMMELIER_NO_LLM"); v != "" {
		c.NoLLM = truthy(v)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Template is the commented file written by `sommelier setup`.
func Template() string {
	return `# sommelier configuration
# Environment variables override anything set here.

[nhentai]
# Optional. Anonymous access works; a key raises search 10->20/min and
# gallery detail 20->45/min. Generate one in nhentai account settings.
api_key = ""

# Sent on every request. nhentai asks for: AppName/version (contact or URL)
user_agent = "sommelier/0.1.0 (+https://github.com/andryan0001/sommelier)"

# Where API responses are cached.
cache_dir = "~/.sommelier/cache"

# Tags to always drop, e.g. ["lolicon", "guro"]
exclude = []

[llm]
# anthropic openai openrouter nvidia groq deepseek mistral xai together
# ollama lmstudio — empty means auto-detect from the environment.
provider = ""

# Empty uses the provider preset's default model.
model = ""

# Override the endpoint for any OpenAI- or Anthropic-shaped API.
base_url = ""

# Override the key; empty falls back to the provider's own variable,
# e.g. OPENROUTER_API_KEY or ANTHROPIC_API_KEY.
api_key = ""

[search]
pages = 2
shortlist = 12
sort = "popular"
min_pages = 0
max_pages = 0
no_llm = false

[index]
path = "~/.sommelier/index.jsonl"
tags_path = "~/.sommelier/tags.jsonl"
`
}

// Write renders the template with the setup answers filled in.
//
// Values are written as TOML basic strings. A key containing a quote or a
// backslash would produce an unparseable file, so both are escaped rather than
// rejected — the user's key is the one thing here that cannot be guessed.
//
// The template carries two api_key entries, so the model key is written into
// the [llm] section specifically rather than to the first match.
func Write(provider, model, apiKey string) (string, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	text := Template()
	if provider != "" {
		text = strings.Replace(text, `provider = ""`, `provider = "`+escapeTOML(provider)+`"`, 1)
	}
	if model != "" {
		text = strings.Replace(text, `model = ""`, `model = "`+escapeTOML(model)+`"`, 1)
	}
	if apiKey != "" {
		if idx := strings.Index(text, "[llm]"); idx >= 0 {
			head, tail := text[:idx], text[idx:]
			tail = strings.Replace(tail, `api_key = ""`, `api_key = "`+escapeTOML(apiKey)+`"`, 1)
			text = head + tail
		}
	}

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	// The file holds a credential; make sure a pre-existing file is tightened
	// too, since WriteFile only applies the mode on creation.
	if err := os.Chmod(path, 0o600); err != nil {
		return path, err
	}
	return path, nil
}

func escapeTOML(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

// ---- minimal TOML subset -------------------------------------------------

type parsed struct {
	scalars map[string]string
	arrays  map[string][]string
}

func (p *parsed) str(key, fallback string) string {
	if v, ok := p.scalars[key]; ok && v != "" {
		return v
	}
	return fallback
}

func (p *parsed) int(key string, fallback int) int {
	if v, ok := p.scalars[key]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func (p *parsed) boolean(key string, fallback bool) bool {
	if v, ok := p.scalars[key]; ok && v != "" {
		return truthy(v)
	}
	return fallback
}

func parseTOML(text string) (*parsed, error) {
	p := &parsed{scalars: map[string]string{}, arrays: map[string][]string{}}
	section := ""

	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(stripComment(line[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}
		full := key
		if section != "" {
			full = section + "." + key
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			inner := strings.TrimSpace(value[1 : len(value)-1])
			items := []string{}
			if inner != "" {
				for _, part := range strings.Split(inner, ",") {
					if part = strings.TrimSpace(part); part != "" {
						items = append(items, unquote(part))
					}
				}
			}
			p.arrays[full] = items
			continue
		}
		p.scalars[full] = unquote(value)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// stripComment removes a trailing # comment that is not inside a quoted string.
func stripComment(v string) string {
	var quote byte
	escaped := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && quote == '"':
			escaped = true
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return v[:i]
		}
	}
	return v
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			return unescape(v[1 : len(v)-1])
		}
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			// TOML literal strings take no escapes.
			return v[1 : len(v)-1]
		}
	}
	return v
}

// unescape handles the escapes Write can emit, plus the common control ones.
func unescape(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
