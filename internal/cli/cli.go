// Package cli is the command surface.
package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/andryan0001/sommelier/internal/agent"
	"github.com/andryan0001/sommelier/internal/api"
	"github.com/andryan0001/sommelier/internal/config"
	"github.com/andryan0001/sommelier/internal/index"
	"github.com/andryan0001/sommelier/internal/llm"
	"github.com/andryan0001/sommelier/internal/retrieve"
)

// Version is stamped at build time with -ldflags.
var Version = "0.1.0"

const usage = `sommelier — describe what you want in plain language; get ranked doujinshi back

USAGE
  sommelier "incest hentai with illness female protagonist"
  sommelier index --english
  sommelier setup

QUERY FLAGS
  --provider NAME     anthropic, openai, openrouter, nvidia, groq, deepseek,
                      mistral, xai, together, ollama, lmstudio
  --model ID          model for that provider
  --base-url URL      any OpenAI- or Anthropic-shaped endpoint
  --api-key KEY       provider key (else the provider's env var, else config)
  --no-llm            skip the model; plan and rank with heuristics
  --pages N           live search pages to pull, 25 each (default 2)
  --shortlist N       candidates to enrich and rank (default 12)
  --reviews N         how many finalists get a written review (default 3)
  --sort NAME         popular, popular-today, popular-week, popular-month, date
  --exclude TAG       tag to drop, repeatable
  --min-pages N       drop galleries shorter than this
  --max-pages N       drop galleries longer than this
  --offline           use only the local index, no live search
  --json              machine-readable output
  --show-query        print the translated query and exit
  -q, --quiet         suppress progress on stderr

INDEX FLAGS
  --english           index the English catalog (default)
  --all               index every language
  --language NAME     index one language, e.g. japanese
  --max-pages N       stop after N pages
  --resume            continue an interrupted build
  --restart           discard progress and rebuild from page 1

Run "sommelier setup" on first use. Configuration lives in ~/.sommelier/config.toml.
`

// Run dispatches a command line and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "sommelier %s\n", Version)
		return 0
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 1
	}
	// A fresh install is guided rather than failing at the first API call.
	if !config.Exists() && !hasFlag(args, "--no-setup") {
		fmt.Fprintln(stdout, "No configuration yet — running first-time setup.")
		fmt.Fprintln(stdout)
		if code := runSetup(nil, stdout, stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "\nSetup written. Re-run your query when ready.")
		return 0
	}
	return runQuery(cfg, args, stdout, stderr)
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// ---- query ----------------------------------------------------------------

func runQuery(cfg *config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sommelier", flag.ContinueOnError)
	fs.SetOutput(stderr)

	provider := fs.String("provider", cfg.Provider, "model provider")
	model := fs.String("model", cfg.Model, "model id")
	baseURL := fs.String("base-url", cfg.BaseURL, "provider endpoint")
	apiKey := fs.String("api-key", cfg.LLMKey, "provider key")
	noLLM := fs.Bool("no-llm", cfg.NoLLM, "skip the model")
	pages := fs.Int("pages", cfg.Pages, "live search pages to pull")
	shortlist := fs.Int("shortlist", cfg.Shortlist, "candidates to enrich and rank")
	reviews := fs.Int("reviews", 3, "finalists that get a written review")
	sortBy := fs.String("sort", cfg.Sort, "search ordering")
	minPages := fs.Int("min-pages", cfg.MinPages, "minimum page count")
	maxPages := fs.Int("max-pages", cfg.MaxPages, "maximum page count")
	offline := fs.Bool("offline", false, "use only the local index")
	asJSON := fs.Bool("json", false, "machine-readable output")
	showQuery := fs.Bool("show-query", false, "print the translated query only")
	quietFlag := fs.Bool("quiet", false, "suppress progress")
	quietShort := fs.Bool("q", false, "suppress progress")
	fs.Bool("no-setup", false, "skip the first-run wizard")

	var excludes multiFlag
	fs.Var(&excludes, "exclude", "tag to drop (repeatable)")

	// Go's flag package stops at the first non-flag argument, but the natural
	// invocation puts the request first: sommelier "..." --pages 3. Consume one
	// positional at a time and re-parse the remainder so flags work either side.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Args()[0])
		rest = fs.Args()[1:]
	}

	quiet := *quietFlag || *quietShort
	if len(positional) == 0 {
		fmt.Fprintln(stderr, `no request given — try: sommelier "slow burn romance, english"`)
		return 2
	}
	request := strings.TrimSpace(strings.Join(positional, " "))
	if request == "" {
		fmt.Fprintln(stderr, "empty request")
		return 2
	}

	logf := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(stderr, format+"\n", a...)
		}
	}

	// Model first: a missing key must not waste API budget before we know we
	// are falling back to heuristics.
	var modelClient *llm.Client
	if !*noLLM {
		c, err := llm.New(llm.Options{
			Provider: *provider,
			Model:    *model,
			BaseURL:  *baseURL,
			APIKey:   *apiKey,
		})
		if err != nil {
			logf("! no model available (%v); using heuristics", err)
		} else {
			modelClient = c
			logf("provider %s · model %s", c.Provider, c.Model)
		}
	}

	client := api.New(api.Options{
		UserAgent: cfg.UserAgent,
		APIKey:    cfg.APIKey,
		CacheDir:  cfg.CacheDir,
	})
	defer client.Close()

	idx, err := index.Open(cfg.IndexPath)
	if err != nil {
		fmt.Fprintf(stderr, "index: %v\n", err)
		return 1
	}

	vocabulary := vocabularyFor(client, idx, modelClient != nil, !*offline)
	plan, planned := agent.PlanQuery(modelClient, request, vocabulary)
	if !planned {
		logf("! planner used heuristics")
	}

	if *showQuery {
		fmt.Fprintln(stdout, plan.Query)
		return 0
	}
	logf("query: %s", plan.Query)

	result, err := retrieve.Run(client, retrieve.Options{
		Query:     plan.Query,
		Pages:     *pages,
		Shortlist: *shortlist,
		Sort:      *sortBy,
		Exclude:   append(append([]string{}, cfg.Exclude...), excludes...),
		MinPages:  *minPages,
		MaxPages:  *maxPages,
		Enrich:    true,
		Live:      !*offline,
		Index:     idx,
	})
	if err != nil {
		fmt.Fprintf(stderr, "nhentai API error: %v\n", err)
		return 1
	}
	if len(result.Candidates) == 0 {
		fmt.Fprintln(stderr, "no candidates matched. try --pages 3, drop a tag, or build the index with: sommelier index --english")
		return 2
	}

	ranked, rankedByModel := agent.Rank(modelClient, request, result.Candidates)
	if modelClient != nil && !rankedByModel {
		logf("! ranker failed; using heuristic ranking")
	}
	critiqued := agent.Critique(modelClient, request, ranked, *shortlist)
	reviewed := agent.Review(modelClient, request, ranked, *reviews)

	stats := client.Stats()
	if *asJSON {
		return printJSON(stdout, request, plan, ranked, result, stats, rankedByModel, critiqued, reviewed)
	}
	printTable(stdout, request, plan, ranked, result, stats, rankedByModel, critiqued, reviewed, modelClient != nil)
	return 0
}

// vocabularyFor grounds the planner in real tag names. The index knows tags the
// popular list never shows, so it is preferred when it is large enough. In
// offline mode the local vocabulary is the only source: no network call.
func vocabularyFor(client *api.Client, idx *index.Index, useModel, live bool) []string {
	if !useModel {
		return nil
	}
	if idx.Len() >= 500 {
		if vocab := idx.Vocabulary(150); len(vocab) > 40 {
			return vocab
		}
	}
	if !live {
		return idx.Vocabulary(100)
	}
	tags, err := client.TagsByType("tag", "popular", 100, 1)
	if err != nil {
		return idx.Vocabulary(100)
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

func printTable(
	stdout io.Writer,
	request string,
	plan agent.Plan,
	ranked []agent.Ranked,
	result *retrieve.Result,
	stats api.Stats,
	rankedByModel, critiqued, reviewed, useModel bool,
) {
	fmt.Fprintf(stdout, "\n  request  %s\n", request)
	fmt.Fprintf(stdout, "  query    %s\n", plan.Query)
	if plan.Notes != "" {
		fmt.Fprintf(stdout, "  planner  %s\n", plan.Notes)
	}
	fmt.Fprintf(stdout, "  corpus   %s\n", result.Summary())
	fmt.Fprintf(stdout, "  calls    %d live, %d cached, throttle %s, keyed=%t\n",
		stats.Calls, stats.CacheHits, (stats.Throttle).Round(time.Millisecond), stats.Keyed)
	fmt.Fprintf(stdout, "  passes   plan=%s rank=%s critique=%s reviews=%s\n",
		modelLabel(useModel), modelLabel(rankedByModel), modelLabel(critiqued), modelLabel(reviewed))
	fmt.Fprintln(stdout)

	for i, row := range ranked {
		c := row.Candidate
		fmt.Fprintf(stdout, "%2d. %5.1f  %s\n", i+1, row.Score, c.Title)
		fmt.Fprintf(stdout, "    %s\n", c.URL())
		fmt.Fprintf(stdout, "    %dp · %d favs · %s · %s\n",
			c.Pages, c.Favorites, orDash(c.Language), orDash(c.Scanlator))
		if row.Why != "" {
			fmt.Fprintf(stdout, "    why: %s\n", row.Why)
		}
		if row.Critique != "" {
			fmt.Fprintf(stdout, "    read: %s\n", row.Critique)
		}
		if row.Review != "" {
			fmt.Fprintf(stdout, "\n    %s\n", wrap(row.Review, 88, "    "))
		}
		if len(c.Tags) > 0 {
			fmt.Fprintf(stdout, "    tags: %s\n", strings.Join(limit(c.Tags, 12), ", "))
		}
		fmt.Fprintln(stdout)
	}
}

func printJSON(
	stdout io.Writer,
	request string,
	plan agent.Plan,
	ranked []agent.Ranked,
	result *retrieve.Result,
	stats api.Stats,
	rankedByModel, critiqued, reviewed bool,
) int {
	rows := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		row := r.Candidate.Dossier()
		row["score"] = r.Score
		row["why"] = r.Why
		row["url"] = r.Candidate.URL()
		row["source"] = r.Candidate.Source
		if r.Critique != "" {
			row["critique"] = r.Critique
		}
		if r.Review != "" {
			row["review"] = r.Review
		}
		rows = append(rows, row)
	}
	payload := map[string]any{
		"request": request,
		"query":   plan.Query,
		"planner": plan.Notes,
		"passes": map[string]bool{
			"ranked_by_model": rankedByModel,
			"critiqued":       critiqued,
			"reviewed":        reviewed,
		},
		"stats": map[string]any{
			"calls":       stats.Calls,
			"cache_hits":  stats.CacheHits,
			"throttle_ms": stats.ThrottleMS,
			"keyed":       stats.Keyed,
		},
		"corpus": map[string]any{
			"seen":       result.Seen,
			"from_index": result.FromIndex,
			"from_live":  result.FromLive,
			"dropped":    result.Dropped,
		},
		"results": rows,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		return 1
	}
	return 0
}

// ---- index ----------------------------------------------------------------

func runIndex(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 1
	}

	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	english := fs.Bool("english", false, "index the English catalog (default)")
	all := fs.Bool("all", false, "index every language")
	language := fs.String("language", "", "index one language")
	maxPages := fs.Int("max-pages", 0, "stop after N pages")
	resume := fs.Bool("resume", false, "continue an interrupted build")
	restart := fs.Bool("restart", false, "rebuild from page 1")
	quiet := fs.Bool("quiet", false, "suppress progress")
	fs.Bool("q", false, "suppress progress")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// --english is the documented default: the corpus is 148,948 English
	// galleries out of 5,958 pages, which is the useful subset for requests
	// written in English.
	scopeName := "english"
	switch {
	case *all:
		scopeName = "all"
	case *language != "":
		scopeName = strings.ToLower(strings.TrimSpace(*language))
	}
	// A bare --resume repeats the walk that was interrupted, not the default.
	if *resume && !*restart && !*english && !*all && *language == "" {
		if saved := index.LoadCursor(cfg.IndexPath); saved.Scope != "" {
			scopeName = saved.Scope
		}
	}
	query, stamp := "", ""
	if scopeName != "all" {
		query, stamp = "language:"+scopeName, scopeName
	}

	client := api.New(api.Options{
		UserAgent: cfg.UserAgent,
		APIKey:    cfg.APIKey,
		CacheDir:  cfg.CacheDir,
	})
	defer client.Close()

	idx, err := index.Open(cfg.IndexPath)
	if err != nil {
		fmt.Fprintf(stderr, "index: %v\n", err)
		return 1
	}
	tags, err := index.OpenTags(cfg.TagsPath)
	if err != nil {
		fmt.Fprintf(stderr, "tags: %v\n", err)
		return 1
	}

	if *restart {
		if err := os.Remove(cfg.IndexPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "restart: %v\n", err)
			return 1
		}
		idx, err = index.Open(cfg.IndexPath)
		if err != nil {
			fmt.Fprintf(stderr, "index: %v\n", err)
			return 1
		}
	}
	cursor := index.LoadCursor(cfg.IndexPath)
	if *resume && !*restart && cursor.Page == 0 && idx.Len() > 0 {
		fmt.Fprintf(stdout, "nothing to resume: %d galleries already indexed at %s\n", idx.Len(), cfg.IndexPath)
		return 0
	}
	if idx.Len() > 0 && !*resume && !*restart {
		fmt.Fprintf(stdout, "index already holds %d galleries at %s\n", idx.Len(), cfg.IndexPath)
		fmt.Fprintln(stdout, "  --resume   continue where the last build stopped")
		fmt.Fprintln(stdout, "  --restart  discard and rebuild")
		return 0
	}

	scope := "all languages"
	if stamp != "" {
		scope = stamp
	}
	fmt.Fprintf(stdout, "building %s index → %s\n", scope, cfg.IndexPath)
	if *resume {
		if cursor.Page > 0 {
			fmt.Fprintf(stdout, "resuming after page %d (%d galleries)\n", cursor.Page, cursor.Entries)
		}
	}

	last := time.Now()
	progress, err := index.Build(client, idx, tags, index.BuildOptions{
		Query:    query,
		Language: stamp,
		Sort:     "date",
		MaxPages: *maxPages,
		Resume:   *resume,
		Restart:  *restart,
		Scope:    scopeName,
		Progress: func(p index.Progress) {
			if *quiet || time.Since(last) < 2*time.Second {
				return
			}
			last = time.Now()
			total := p.Pages
			if total == 0 {
				total = p.Page
			}
			fmt.Fprintf(stdout, "  page %d/%d · %d galleries · %d tags · %d calls · %s elapsed · ~%s left\n",
				p.Page, total, p.Entries, p.Tags, p.Calls,
				index.FormatDuration(p.Elapsed), index.FormatDuration(p.Remaining))
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "\nindex build failed: %v\n", err)
		fmt.Fprintf(stderr, "progress is saved; re-run with --resume\n")
		return 1
	}

	stats := client.Stats()
	fmt.Fprintf(stdout, "\ndone: %d galleries, %d tags, %d API calls (%d cached), %s\n",
		idx.Len(), tags.Len(), stats.Calls, stats.CacheHits, index.FormatDuration(progress.Elapsed))
	return 0
}

// ---- setup ----------------------------------------------------------------

func runSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "overwrite an existing config")
	nonInteractive := fs.Bool("yes", false, "write defaults without asking")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if config.Exists() && !*force {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(stderr, "config: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "config already exists: %s\n", cfg.Path)
		fmt.Fprintln(stdout, "  sommelier setup --force   rewrite it")
		return 0
	}

	reader := bufio.NewReader(os.Stdin)
	provider := ""
	model := ""
	key := ""

	if !*nonInteractive {
		fmt.Fprintln(stdout, "sommelier setup")
		fmt.Fprintln(stdout, "Press enter to accept the default in brackets.")
		fmt.Fprintln(stdout)

		fmt.Fprintf(stdout, "Provider (%s) [openrouter]: ", strings.Join(llm.ProviderNames, ", "))
		provider = ask(reader)
		if provider == "" {
			provider = "openrouter"
		}
		if _, ok := llm.Presets[provider]; !ok {
			fmt.Fprintf(stderr, "\nunknown provider %q; writing defaults instead\n", provider)
			provider = ""
		}

		if p, ok := llm.Presets[provider]; ok {
			fmt.Fprintf(stdout, "Model [%s]: ", p.Model)
			model = ask(reader)

			env := p.KeyEnv
			if env == "" {
				env = "SOMMELIER_API_KEY"
			}
			fmt.Fprintf(stdout, "API key (stored in the config file, chmod 600) [from $%s]: ", env)
			key = ask(reader)
			if key == "" {
				key = os.Getenv(env)
			}
		}
	}

	path, err := config.Write(provider, model, key)
	if err != nil {
		fmt.Fprintf(stderr, "write config: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\nwrote %s\n", path)
	if key == "" {
		fmt.Fprintln(stdout, "no key stored — set one in the file, or export the provider's env var")
	}
	fmt.Fprintln(stdout, "\nnext:")
	fmt.Fprintln(stdout, "  sommelier index --english        build the local catalog (~5-6 h, one time)")
	fmt.Fprintln(stdout, `  sommelier "slow burn romance, english"`)
	return 0
}

func ask(r *bufio.Reader) string {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimSpace(line)
}

// ---- config ---------------------------------------------------------------

func runConfig(args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 1
	}
	if len(args) > 0 && args[0] == "path" {
		fmt.Fprintln(stdout, cfg.Path)
		return 0
	}

	fmt.Fprintf(stdout, "config   %s\n", cfg.Path)
	fmt.Fprintf(stdout, "cache    %s\n", cfg.CacheDir)
	fmt.Fprintf(stdout, "index    %s\n", cfg.IndexPath)
	fmt.Fprintf(stdout, "tags     %s\n", cfg.TagsPath)
	fmt.Fprintf(stdout, "nhentai  key=%t  ua=%s\n", cfg.APIKey != "", cfg.UserAgent)
	fmt.Fprintf(stdout, "llm      provider=%s model=%s key=%t\n",
		orDash(cfg.Provider), orDash(cfg.Model), cfg.LLMKey != "")
	fmt.Fprintf(stdout, "search   pages=%d shortlist=%d sort=%s no_llm=%t\n",
		cfg.Pages, cfg.Shortlist, cfg.Sort, cfg.NoLLM)
	if cfg.MinPages > 0 || cfg.MaxPages > 0 {
		fmt.Fprintf(stdout, "pages    min=%d max=%d\n", cfg.MinPages, cfg.MaxPages)
	}
	if len(cfg.Exclude) > 0 {
		fmt.Fprintf(stdout, "exclude  %s\n", strings.Join(cfg.Exclude, ", "))
	}

	idx, err := index.Open(cfg.IndexPath)
	if err == nil {
		fmt.Fprintf(stdout, "indexed  %d galleries\n", idx.Len())
	}
	if tags, err := index.OpenTags(cfg.TagsPath); err == nil {
		fmt.Fprintf(stdout, "tagdict  %d tags\n", tags.Len())
	}
	return 0
}

// ---- helpers --------------------------------------------------------------

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func modelLabel(ok bool) string {
	if ok {
		return "model"
	}
	return "heuristic"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func limit(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

// wrap breaks text at width on word boundaries, prefixing every line.
func wrap(text string, width int, prefix string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return prefix
	}
	var out strings.Builder
	line := 0
	for i, w := range words {
		if line == 0 {
			out.WriteString(prefix)
		} else if line+1+len(w) > width {
			out.WriteString("\n")
			out.WriteString(prefix)
			line = 0
		} else {
			out.WriteString(" ")
			line++
		}
		out.WriteString(w)
		line += len(w)
		if i == len(words)-1 {
			out.WriteString("\n")
		}
	}
	return strings.TrimSuffix(out.String(), "\n")
}
