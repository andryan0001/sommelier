# sommelier

Describe what you want in plain language; get ranked doujinshi back.

```console
$ sommelier "incest hentai with illness female protagonist"

  request  incest hentai with illness female protagonist
  query    tag:"incest" tag:"illness" language:english
  planner  maps the sick-girl premise onto the illness tag
  corpus   seen 74, index 61, live 25, dropped 4 (too short)
  calls    2 live, 58 cached, throttle 0s, keyed=true
  passes   plan=model rank=model critique=model reviews=model

 1.  91.0  The Shrine Maiden's Long Winter
    https://nhentai.net/g/412345/
    84p · 31,402 favs · english · Project Valvrein
    why: incest + chronic illness, single POV, complete arc
    read: promises a slow decline and a caretaker romance; weakest is a
          rushed final chapter.

    A bedridden older sister and the brother who stops leaving the house.
    ...
```

A model reads your sentence, turns it into [nhentai](https://nhentai.net) query
syntax, ranks what comes back on story fit, critiques the shortlist, and writes
a blurb for each finalist. Every pass has a heuristic fallback, so it still
works with no key at all.

## Install

Requires Go 1.22 or newer to build. The binary has no runtime dependencies.

```console
$ git clone https://github.com/andryan0001/sommelier
$ cd sommelier
$ go build -o sommelier .
$ ./sommelier setup
```

`setup` asks for a provider and a key, then writes `~/.sommelier/config.toml`
(mode `600`). On a fresh machine the first query runs the same wizard and exits;
run the query again once it has written the file.

Prebuilt binaries for Linux, macOS, Windows and FreeBSD are attached to every
tagged release — `go build` is only needed if you want to compile it yourself.

## Build the local catalog first

One command makes every later query fast and cheap:

```console
$ sommelier index --english
building english index → ~/.sommelier/index.jsonl
  page 120/5958 · 3,000 galleries · 2,310 tags · 181 calls · 6m40s elapsed · ~5h24m left
```

The English catalog is 148,948 galleries over 5,958 search pages. The build is
resumable — interrupt it and `--resume` continues from the saved cursor instead
of re-walking pages you already paid for. Tag ids are cached separately in
`tags.jsonl`, so after the first few hundred galleries most pages need no
`/tags/ids` call at all.

Without an index, each query pulls live search results and enriches them, which
costs `1 search + tag batches + shortlist details`. The token bucket, not
latency, sets the pace: a keyed cold query is well under a minute, an anonymous
one roughly twice that. With `--offline`, repeat queries hit the disk and never
touch nhentai; an online run merges the index with the live search.

## Configure

`~/.sommelier/config.toml`, written by `sommelier setup` and commented
throughout. Environment variables override the file; flags override both.

| Variable | Purpose |
|---|---|
| `NHENTAI_API_KEY` | optional; raises search 10→20/min and detail 20→45/min |
| `SOMMELIER_USER_AGENT` | sent on every request; nhentai asks for `App/version (contact)` |
| `SOMMELIER_CACHE` | API response cache, default `~/.sommelier/cache` |
| `SOMMELIER_INDEX` | catalog path, default `~/.sommelier/index.jsonl` |
| `SOMMELIER_TAGS` | tag dictionary, default `~/.sommelier/tags.jsonl` |
| `SOMMELIER_EXCLUDE` | comma-separated tags to always drop |
| `SOMMELIER_PROVIDER` | `anthropic`, `openai`, `openrouter`, `nvidia`, … |
| `SOMMELIER_MODEL` | model id for that provider |
| `SOMMELIER_BASE_URL` | any OpenAI- or Anthropic-shaped endpoint |
| `SOMMELIER_API_KEY` | overrides the provider's own key variable |
| `SOMMELIER_PAGES`, `SOMMELIER_SHORTLIST`, `SOMMELIER_SORT` | retrieval defaults |
| `SOMMELIER_NO_LLM` | `1` skips the model and uses heuristics |

`sommelier config` prints the effective values and where they came from.

## Providers

Two wire formats cover everything: Anthropic `/v1/messages` and OpenAI
`/chat/completions`. Presets ship for `anthropic`, `openai`, `openrouter`,
`nvidia`, `groq`, `deepseek`, `mistral`, `xai`, `together`, `ollama` and
`lmstudio`.

```console
$ sommelier "..." --provider openrouter --model anthropic/claude-sonnet-4.5
$ sommelier "..." --provider nvidia
$ sommelier "..." --base-url http://localhost:8080/v1 --model local-model
```

With no `--provider`, it uses the first provider key it finds in the
environment. With no key at all it degrades to heuristics instead of failing.

## Usage

```console
$ sommelier "slow burn romance, english, completed" --pages 3 --shortlist 20
$ sommelier "..." --exclude lolicon --exclude guro
$ sommelier "..." --offline                  # index only, no nhentai calls
$ sommelier "..." --show-query               # just the translated query
$ sommelier "..." --no-llm                   # no model, no provider key
$ sommelier "..." --json                     # machine-readable, includes reviews

$ sommelier index --english                  # default catalog
$ sommelier index --language japanese        # one language
$ sommelier index --all --max-pages 500      # everything, bounded
$ sommelier index --resume                   # continue after an interruption
$ sommelier index --restart                  # discard and rebuild
```

## Rate limits

nhentai publishes its ceilings in prose and returns **no** `X-RateLimit-*` or
`Retry-After` headers, so the client throttles itself blind: a token bucket per
endpoint class at 90% of the published limit, full-jitter exponential backoff on
429 and 5xx, and a disk cache with per-endpoint TTLs (gallery metadata 7 days,
searches 1 hour, tags 30 days). Requests carry a descriptive `User-Agent`, and
the client only reads — it never touches the write, auth or moderation
endpoints, so the proof-of-work and captcha paths never apply.

## Build

`.github/workflows/build.yml` runs `gofmt`, `go vet` and `go test` on every
push, then cross-compiles eight targets with `CGO_ENABLED=0`. Pushing a tag
attaches them all to a GitHub release.

| Artifact | GOOS/GOARCH |
|---|---|
| `sommelier-linux-amd64` | linux/amd64 |
| `sommelier-linux-arm64` | linux/arm64 |
| `sommelier-linux-armv7` | linux/arm |
| `sommelier-darwin-amd64` | darwin/amd64 |
| `sommelier-darwin-arm64` | darwin/arm64 |
| `sommelier-windows-amd64.exe` | windows/amd64 |
| `sommelier-windows-arm64.exe` | windows/arm64 |
| `sommelier-freebsd-amd64` | freebsd/amd64 |

```console
$ git tag v0.1.0 && git push --tags     # builds all eight and cuts a release
```

## Tests

```console
$ go test ./...
```

## Licence

MIT
