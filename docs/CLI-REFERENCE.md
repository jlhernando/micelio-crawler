# Micelio CLI Reference

This reference matches the commands and flags in the current source tree. Run
`micelio <command> --help` to confirm the exact defaults in an installed release.

## Command map

| Command | Purpose |
|---|---|
| `crawl <url>` | Discover a site from a seed URL and run the full analysis pipeline |
| `list <file>` | Crawl a newline-delimited list of URLs, with the same analysis flags as `crawl` |
| `sitemap <url...>` | Resolve XML sitemaps and crawl exactly their listed URLs, with the same analysis flags as `crawl` |
| `head <file>` | Make lightweight HEAD requests for status and header checks |
| `robots-test <url>` | Test robots.txt rules against major crawler user agents |
| `verify-bot <ip...>` | Check IPs against Google's official crawler ranges |
| `diff <old.jsonl> <new.jsonl>` | Compare two crawl exports |
| `generate-sitemap <input.jsonl>` | Build an XML sitemap from eligible crawl results |
| `import <file>` | Add a crawl made elsewhere to the dashboard's History and analyse it |
| `logs <file...>` | Analyze and merge server access logs |
| `schedule [url]` | Run crawls on a cron schedule, with prechecks and reports |
| `schedules` | List registered schedules and their state |
| `gsc-auth` | Manage the local Search Console OAuth token |
| `ui` | Start the embedded web dashboard |
| `mcp` | Let AI agents (Claude Code, Claude Desktop...) read crawls over MCP; see [MCP.md](MCP.md) |
| `build` | Rebuild and install from the source directory embedded in the binary |

Cobra also provides `completion` for shell-completion scripts.

## Global options

```text
--memlimit <size>   Soft memory limit, for example 512MiB or 2GiB
-v, --version       Print the version
-h, --help          Show help
```

The memory limit is global and can be combined with any subcommand.

## `crawl`: Spider crawl and full analysis

```bash
micelio crawl <url> [flags]
micelio crawl --config crawl.json [flags]
```

`crawl`, `list` and `sitemap` share every flag below, so the CLI has every option
of the dashboard's New Crawl page. Only `--depth` and `--max-pages` are
`crawl`-only; `list` and `sitemap` keep their own defaults (see their sections).

### Config file

| Flag | Default | Meaning |
|---|---:|---|
| `--config <file>` | | Crawl config JSON in the dashboard format: the body of `POST /api/crawl/start` or a schedule's `crawlConfig` |

The file uses the dashboard keys (`url`, `limit`, `depth`, `delay`, `urlList`,
`sitemapUrls`, `retries`, `pathOnlyFilters`, ...). Flags you set on the command
line override the file; flags left at their default never do. The URL argument
overrides `url` (`crawl`), the file argument overrides `urlList` (`list`) and
the URL arguments override `sitemapUrls` (`sitemap`). Each command keeps its own
mode whatever the file says. Without an argument the target must come from the
file, otherwise the command fails.

### Discovery, limits, and output

| Flag | Default | Meaning |
|---|---:|---|
| `-d, --depth <n>` | `10` | Maximum link depth from the seed |
| `--max-pages <n>` | `1000` | Maximum number of pages |
| `-c, --concurrency <n>` | `5` | Concurrent requests |
| `--delay <ms>` | `200` | Minimum request delay |
| `--max-errors <n>` | `0` | Stop after N errors; 0 is unlimited |
| `--timeout <seconds>` | `0` | Stop after N seconds; 0 is unlimited |
| `--allowed-domains <domain>` | | Add an allowed domain; repeat the flag for more |
| `--include <regex>` | | Include matching URLs; repeatable |
| `--exclude <regex>` | | Exclude matching URLs; repeatable |
| `--path-only-filters` | off | Match `--include`/`--exclude` against the URL path only, without the query string |
| `--retries <n>` | `2` | Retries per failed URL (network error, timeout, 408, 429, 5xx); `0` disables them |
| `--discover-sitemaps` | off | Add sitemap-listed URLs to a Spider crawl |
| `-o, --output <path>` | | Page-data output path |
| `-f, --format <format>` | | `jsonl` or `csv` |
| `--html` | off | Generate an interactive HTML report |
| `--html-open` | off | Open the generated HTML report |
| `--sitemap-out` | off | Generate an XML sitemap from indexable pages |
| `--db <path>` | auto | Explicit SQLite crawl database path |
| `--no-db` | off | Disable automatic database creation |
| `--resume` | off | Resume an interrupted crawl from `--db` |
| `--full-anchors` | off | Preserve full anchor detail in page output |
| `--save-html` | off | Store each page's raw HTML in the crawl database (zstd compressed) |
| `--save-rendered` | off | Store each page's rendered HTML in the crawl database (with `--js`) |
| `--no-analysis` | off | Only crawl and save; skip post-crawl analysis (run it later with `micelio import`) |
| `--db-only` | off | Write only the crawl database, no JSONL/CSV output |
| `--no-soft404-probe` | off | Skip the one request for a URL that cannot exist (soft-404 check) |

When no database path is supplied, the CLI derives one from the output path or
seed domain. `--resume` cannot be combined with `--no-db`.

`--max-errors` exists on the CLI only. Dashboard crawls retry failed URLs instead
(New Crawl > Retries per URL, `--retries` on the CLI) and never stop because of
failures.

### HTTP, robots, rendering, and safety

| Flag | Default | Meaning |
|---|---:|---|
| `-u, --user-agent <value>` | `Micelio/1.0` | Request user agent; overrides the `--device` user agent |
| `--device <id>` | none | Crawl as `desktop`, `laptop`, `tablet`, `mobile`, `iphone`, `googlebot-smartphone`, `googlebot-desktop`, `bingbot` or `custom`: its user agent and client hints, and with `--js` its screen |
| `--viewport <WxH>` | device size | Screen for `--js`, e.g. `412x915` (without `--device` it means `custom`) |
| `--dpr <ratio>` | device ratio | Device pixel ratio for `--js`, e.g. `2.625` |
| `--mobile` | off | Mobile viewport and touch for `--js` |
| `--landscape` | off | Turn a phone or tablet `--device` sideways |
| `--compare-device <id>` | none | Also fetch (and with `--js` render) every page as this device and store what differs in each page's `deviceCompare`; about doubles the requests |
| `-H, --header "Name: Value"` | | Add a custom request header; repeatable |
| `--cookies <value>` | | Initial Cookie header |
| `--proxy <url>` | | HTTP, HTTPS, or SOCKS5 proxy; `tor` selects Tor |
| `--no-robots` | off | Ignore robots.txt restrictions |
| `--show-blocked-internal` | off | Include internally discovered URLs blocked by robots.txt |
| `--adaptive-rate` | off | Adjust request rate from server feedback |
| `--delay-factor <n>` | `0` | Adaptive gap as a multiple of average response time |
| `--stealth` | off | Mimic a Chrome TLS/HTTP fingerprint |
| `--tor-control-port <host:port>` | `127.0.0.1:9051` when used | Tor control endpoint |
| `--tor-password <value>` | | Tor control password |
| `--tor-rotate <n>` | `50` when used | Rotate the Tor circuit every N requests |
| `--js` | off | Render pages in headless Chrome |
| `--render-block-resources` | on | Block images, fonts, and analytics while rendering |
| `--render-timeout <seconds>` | `30` | JS-render timeout |

### Technical, content, and link analysis

| Flag | Default | Meaning |
|---|---:|---|
| `--check-external` | off | Check external links for broken destinations |
| `--page-weight` | off | Fetch resource sizes with HEAD requests |
| `--full-page-weight` | off | Keep per-resource page-weight details |
| `--ngrams` | off | Run n-gram analysis |
| `--language <code>` | `en` | Stopword language for n-grams |
| `--embeddings` | off | Run semantic similarity analysis |
| `--embedding-provider <name>` | `openai` | `openai` or `ollama` |
| `--embedding-model <name>` | provider default | Embedding model |
| `--embedding-key <key>` | environment | Embedding API key |
| `--similarity-threshold <n>` | `0.9` | Similarity threshold from 0 to 1 |
| `--link-intelligence` | off | Compute graph metrics, orphans, and suggestions |
| `--li-no-centrality` | off | Skip expensive centrality calculations |
| `--li-max-suggestions <n>` | `20` | Maximum internal-link suggestions |
| `--segment <name:pattern>` | | Add a named URL segment; repeatable |

### Extraction and AI analysis

| Flag | Meaning |
|---|---|
| `--extract <name:selector>` | Store text selected by CSS; repeatable |
| `--search <text-or-/regex/>` | Search page source; repeatable |
| `--snippet <path.js>` | Evaluate a raw JavaScript expression after rendering; repeatable and requires `--js` |
| `--ai-prompt <prompt>` | Enable prompt-based per-page analysis |
| `--ai-provider <name>` | `openai`, `anthropic`, or `ollama`; default `openai` |
| `--ai-model <name>` | Override the provider model |
| `--ai-key <key>` | Provider key; environment fallback is supported |

### Custom snippets

`--snippet` runs your own JavaScript on each page in headless Chrome. It needs
`--js`. Snippet files hold **raw JavaScript expressions**, not ES modules. The
return value is stored in the crawl results under `snippetResults`, keyed by
filename. The `snippets/` folder of the repository has ready-made examples
(accessibility, analytics, console errors, cookie consent, performance, tech
stack).

```bash
micelio crawl https://example.com --js \
  --snippet snippets/tech-stack.js --snippet ./my-snippet.js
```

A single value:

```javascript
// heading.js
document.querySelector('h1')?.textContent
```

An object with several values:

```javascript
// hero-data.js
({
  heading: document.querySelector('h1')?.textContent,
  heroImage: document.querySelector('.hero img')?.src,
  articleDate: document.querySelector('time')?.getAttribute('datetime'),
  wordCount: document.body.innerText.split(/\s+/).length
})
```

Quick audit checks:

```javascript
// audit-checks.js
({
  hasLazyImages: document.querySelectorAll('img[loading="lazy"]').length,
  hasCookieBanner: !!document.querySelector('[class*="cookie"], [id*="cookie"]'),
  externalScripts: [...document.querySelectorAll('script[src]')]
    .filter(s => !s.src.includes(location.hostname))
    .map(s => s.src)
})
```

### External data integrations

| Flag | Meaning |
|---|---|
| `--psi`, `--psi-key <key>` | Enable PageSpeed Insights and optionally supply its key |
| `--crux`, `--crux-key <key>` | Enable Chrome UX Report data |
| `--crux-form-factor <value>` | `PHONE`, `DESKTOP`, or `ALL`; default `ALL` |
| `--gsc` | Enable Search Console API enrichment |
| `--gsc-property <value>` | URL-prefix or domain property |
| `--gsc-key-file <path>` | Service-account JSON file |
| `--gsc-days <n>` | Lookback window; default 90 |
| `--gsc-bq <project.dataset>` | Use Search Console BigQuery bulk export |
| `--ga4` | Enable Google Analytics 4 enrichment |
| `--ga4-property <id>` | GA4 property ID |
| `--ga4-key-file <path>` | Service-account JSON file |
| `--ga4-days <n>` | Lookback window; default 90 |
| `--plausible` | Enable Plausible Analytics enrichment |
| `--plausible-site-id <domain>` | Plausible site ID |
| `--plausible-api-key <key>` | Plausible API key |
| `--plausible-days <n>` | Lookback window; default 30 |
| `--plausible-host <url>` | Override the Plausible host for self-hosted instances |

If both `--gsc` and `--gsc-bq` are supplied, the Search Console API takes
precedence.

### Crawl examples

```bash
# Fast local report
micelio crawl https://example.com -d 1 --max-pages 100 --html

# Technical audit with rendered-page comparison
micelio crawl https://example.com --js --check-external --page-weight --html

# Link architecture plus sitemap-only orphan discovery
micelio crawl https://example.com \
  --discover-sitemaps --link-intelligence --embeddings --ngrams

# Adaptive, persistent crawl that can be resumed
micelio crawl https://example.com \
  --db example.db --adaptive-rate --delay-factor 5 --max-pages 100000

# Custom extraction, a literal search and a source regex
micelio crawl https://example.com \
  --extract 'author:.author-name' \
  --search 'out of stock' \
  --search '/price:\s*\$[\d,]+/'

# Full audit with the main analyses
micelio crawl https://example.com --link-intelligence --ngrams --page-weight --check-external --html

# JS rendering with a Chrome user agent
micelio crawl https://example.com --js --max-pages 200 \
  -u "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

# Multi-domain crawl
micelio crawl https://example.com --allowed-domains blog.example.com

# Resume an interrupted crawl
micelio crawl https://example.com --db crawl.db
micelio crawl https://example.com --db crawl.db --resume

# Stop after 5 minutes or 50 errors
micelio crawl https://example.com --timeout 300 --max-errors 50

# Search Console data with a service account
micelio crawl https://example.com --gsc --gsc-property "https://example.com/" --gsc-key-file sa.json

# CSV output
micelio crawl https://example.com -f csv -o crawl.csv

# Lean crawl for a small cloud server (analyse later with micelio import)
micelio crawl https://example.com --max-pages 50000 --no-analysis --db-only --db example.db

# Dashboard config, with a smaller page limit for a quick test
micelio crawl --config crawl.json --max-pages 200

# Keep the raw HTML and retry each failed URL up to 5 times
micelio crawl https://example.com --save-html --retries 5
```

## `list`: Crawl a URL file

```bash
micelio list urls.txt [flags]
micelio list --config crawl.json [flags]
```

Input is one URL per line. This mode does not spider links from downloaded pages.
It takes every `crawl` flag except `--depth` and `--max-pages` (the page limit is
the number of URLs) and runs the same post-crawl analysis, with a crawl database,
unless you pass `--no-analysis` or `--no-db`. Only these defaults differ:

| Flag | Default |
|---|---:|
| `-c, --concurrency` | `3` |
| `--delay` | `100` ms |
| `-o, --output` | `output.jsonl` |

Without `-o` or `--db`, the database is named after the first URL's host and the
mode, for example `example.com-list.db`.

```bash
micelio list migration-urls.txt -c 10 --delay 100 -o migration.jsonl
micelio list migration-urls.txt --js --link-intelligence --html
```

## `sitemap`: Crawl listed sitemap URLs

```bash
micelio sitemap <url...> [flags]
```

Micelio supports standard sitemap files, nested `<sitemapindex>` documents, gzip
compression, and news/video/image extensions. The sitemap host may differ from
the listed page hosts. This mode crawls the listed pages and does not spider their
links. It takes every `crawl` flag except `--depth` and `--max-pages` and runs the
same post-crawl analysis, with a crawl database, unless you pass `--no-analysis`
or `--no-db`. Only these flags and defaults differ:

| Flag | Default |
|---|---:|
| `-l, --limit` | `0` (100,000 pages) |
| `-c, --concurrency` | `3` |
| `--delay` | `200` ms |
| `-o, --output` | `output.jsonl` |

Without `-o` or `--db`, the database is named after the first sitemap's host and
the mode, for example `example.com-sitemap.db`. With `--config` and no `--limit`,
the file's `limit` applies.

```bash
micelio sitemap https://example.com/sitemap.xml
micelio sitemap https://example.com/sitemap.xml https://example.com/sitemap-news.xml -l 5000
# Sitemap hosted on a CDN or S3, pages on the main domain:
micelio sitemap https://cdn.example.com/sitemaps/index.xml -l 50000
```

## `head`: Status and header checks

```bash
micelio head urls.txt [flags]
```

This command avoids downloading response bodies. It reports status codes,
redirect chains, and response headers.

| Flag | Default |
|---|---:|
| `-c, --concurrency` | `5` |
| `--delay` | `0` ms |
| `-u, --user-agent` | `Micelio/1.0` |
| `-o, --output` | `head-output.jsonl` |
| `--csv` | off |
| `--proxy`, `--cookies`, `-H, --header` | unset |

```bash
micelio head urls.txt -c 20 -o status-check.jsonl
micelio head redirect-list.txt --csv -o headers.csv
```

## `robots-test`: Compare crawler access

```bash
micelio robots-test https://example.com [flags]
```

Without `--agents`, Micelio tests 21 major bot signatures, including Googlebot,
Google-Agent, Google-Extended, Bingbot and GPTBot. It reports an allow/block
matrix and any Crawl-delay. Without `--url`, it tests a built-in set of common
paths. `--url` is repeatable.

| Flag | Meaning |
|---|---|
| `--agents <list>` | Comma-separated user agents |
| `--url <path>` | Path or URL to test; repeatable |
| `--json` | Print JSON |

```bash
micelio robots-test https://example.com \
  --agents 'Googlebot,Bingbot,GPTBot' \
  --url / --url /private --url /api --json
```

## `verify-bot`: Verify Google crawler IPs

```bash
micelio verify-bot 66.249.79.1 192.0.2.1 [--json]
```

The command checks Google's official Googlebot, special-crawler, and
user-triggered-fetcher ranges (the last includes Google-Agent). Range data is
cached locally for 24 hours.

## `diff`: Compare crawl exports

```bash
micelio diff old.jsonl new.jsonl [flags]
```

| Flag | Meaning |
|---|---|
| `--full` | Compute the complete comparison report and statistics |
| `--html <path>` | Write an interactive HTML comparison |
| `--json <path>` | Write the complete comparison as JSON |

```bash
micelio diff before.jsonl after.jsonl --full \
  --html migration-diff.html --json migration-diff.json
```

Use equivalent settings for both source crawls so the diff reflects site changes
rather than different limits, filters, rendering, or integrations.

## `generate-sitemap`: Create XML from crawl data

```bash
micelio generate-sitemap crawl.jsonl [flags]
```

Only eligible, indexable 200-status pages are included.

| Flag | Default |
|---|---:|
| `-o, --output` | `sitemap.xml` |
| `--changefreq` | unset |
| `--priority` | unset |
| `--max-urls` | `50000` |

```bash
micelio generate-sitemap crawl.jsonl -o sitemap.xml \
  --changefreq weekly --priority 0.8
```

## `import`: Open a crawl made elsewhere

```bash
micelio import <crawl.db | crawl.jsonl[.gz|.zst]> [flags]
```

Adds a crawl made with `micelio crawl` (for example on a small cloud server) to
the dashboard's History and runs the dashboard's analysis on this computer:
stats, issues, PageRank, Universe and the directory tree. It sends the file to a
running dashboard, or imports it straight into `~/.micelio` when none is running.
JSONL input is converted to a crawl database first.

| Flag | Default | Meaning |
|---|---:|---|
| `--server <url>` | `http://127.0.0.1:3100` | Running dashboard to send the crawl to |
| `--auth-token <token>` | | Dashboard token, if it was started with `--auth-token` |
| `--link-intelligence` | off | Run link analysis (centrality, suggestions) even if the crawl did not |
| `--local` | off | Import into `~/.micelio` directly, even if a dashboard is running |

```bash
# On the server: crawl only, one output file
micelio crawl https://example.com --max-pages 50000 --no-analysis --db-only --db example.db

# On your computer
scp server:example.db . && micelio import example.db --link-intelligence
```

Anchor stats need `--full-anchors` at crawl time. N-grams, page weight and
integrations are not rebuilt by an import. The dashboard has the same action
under **History > Import crawl…**. See [Crawl in the cloud](CLOUD-CRAWLS.md).

## `logs`: Analyze server access logs

```bash
micelio logs access.log [access-2.log ...] [flags]
```

Files are processed in parallel and merged into one analysis. Format is normally
auto-detected.

| Flag | Meaning |
|---|---|
| `-f, --format <name>` | Hint: `apache_combined`, `apache_clf`, `nginx_combined`, `cloudfront`, `cloudflare`, `alb`, or `w3c` |
| `-w, --workers <n>` | Parallel workers; 0 uses `GOMAXPROCS` |
| `--json` | Print JSON statistics to stdout |
| `-o, --output <path>` | Write JSON statistics to a file |

```bash
micelio logs access-2026-07-*.log --output bot-analysis.json
```

The dashboard adds interactive URL, directory, bot, trend, heatmap, waste, and
log-to-crawl merge views; see the [user guide](USER-GUIDE.md#5-analyze-server-logs).

## `schedule` and `schedules`: Recurring crawls

```bash
micelio schedule https://example.com --cron '0 2 * * *' [flags]
micelio schedule --config site.json --cron '0 2 * * *' [flags]
micelio schedules
```

Scheduling persists the definition and can notify a webhook after each run.
`micelio schedules` lists every schedule and its state.

| Flag | Default |
|---|---:|
| `--cron` | `@daily` |
| `--max-runs` | `0` (unlimited) |
| `--output-dir` | `scheduled-crawls` |
| `--webhook` | unset |
| `-c, --concurrency` | `5` |
| `--max-pages` | `1000` |
| `-d, --depth` | `10` |
| `--delay` | `200` ms |
| `-u, --user-agent` | `Micelio/1.0` |
| `-f, --format` | `jsonl` |

Scheduled analysis flags are `--ngrams`, `--language`, `--link-intelligence`,
`--html`, `--check-external`, `--page-weight`, `--sitemap-out`, `--extract`, and
`--search`.

Cron supports five-field expressions and aliases such as `@daily` and `@weekly`.

### Config file, prechecks and reports

| Flag | Default | Meaning |
|---|---:|---|
| `--config <file.json>` | | Crawl config in the dashboard's JSON format (the body of `POST /api/crawl/start`); overrides the crawl flags, so a schedule can use sitemap or list mode, filters, adaptive rate and every other dashboard option |
| `--name <name>` | | Schedule name, so one URL can have several schedules |
| `--precheck-egress <CIDR>` | | Only run when the public IP is in this range (CIDR or IP); repeatable |
| `--precheck-cmd <command>` | | Only run when this shell command exits 0 |
| `--precheck-interval <s>` | `60` | Seconds between checks during a crawl |
| `--check-only` | off | Run the checks once, print the result and exit (1 if they fail) |
| `--email-to <list>` | Settings | Email each run to these addresses (comma separated) |
| `--smtp-host`, `--smtp-port`, `--smtp-user`, `--smtp-from` | Settings | SMTP account for `--email-to`; password in `MICELIO_SMTP_PASSWORD` |
| `--smtp-security` | `starttls` | `starttls`, `tls` or `none` (local relay only) |
| `--telegram-chat <id>` | Settings | Send each run to this Telegram chat; bot token from Settings or `MICELIO_TELEGRAM_TOKEN` |

A run is skipped when a precheck fails. The checks repeat during the crawl and
stop it if they fail (the CLI cannot pause; dashboard schedules pause instead).

Without `--email-to` or `--telegram-chat`, the CLI uses the email, Telegram and
Slack channels saved in the dashboard's Settings (or `~/.micelio/micelio.env`).
It reports skipped and failed runs, and completed runs when "Send a report after
every crawl" is on. The override flags report every run.

```bash
# Daily crawl at 2 AM
micelio schedule https://example.com --cron "0 2 * * *" --link-intelligence --ngrams

# Weekly crawl with a Slack webhook
micelio schedule https://example.com --cron "@weekly" --webhook https://hooks.slack.com/...

# Seven runs, then stop
micelio schedule https://example.com --cron "@daily" --max-runs 7

# Dashboard config, only from an allowed network
micelio schedule --config site.json --cron "0 2 * * *" --precheck-egress 203.0.113.0/24

# Test the checks once
micelio schedule --config site.json --precheck-egress 203.0.113.0/24 --check-only
```

## `gsc-auth`: Search Console OAuth

```bash
micelio gsc-auth --client-id <id> --client-secret <secret>
micelio gsc-auth --revoke
```

The interactive flow starts a local callback listener and stores the token at
`~/.micelio/gsc-token.json`. For CI or servers, prefer a service account with
`crawl --gsc-key-file`.

The OAuth redirect URI is `http://localhost:9549/oauth2callback`.

## `ui`: Start the dashboard

```bash
micelio ui [flags]
```

| Flag | Default | Meaning |
|---|---:|---|
| `-p, --port` | `3100` | HTTP port |
| `--host` | `127.0.0.1` | Bind address. Any address that is not loopback needs a token |
| `--auth-token` | `MICELIO_AUTH_TOKEN` | Require a token on `/api/*`, `/ws` and `/debug/pprof/` |
| `--insecure-no-auth` | off | Allow a non-loopback `--host` without a token (only behind a proxy that authenticates) |
| `--allowed-host <name>` | `MICELIO_ALLOWED_HOSTS` (comma-separated) | Public host name of a reverse proxy or tunnel in front of the dashboard, or an exact origin such as `http://localhost:5175`. Repeatable. It passes the `Host` check and counts as the dashboard's own origin on the port it names (the scheme's default port, 80 or 443, when it names none) |
| `--pprof` | off | Serve `/debug/pprof/` on a non-loopback `--host` too (always on for loopback) |

```bash
micelio ui                     # 127.0.0.1:3100
micelio ui --port 8080         # custom port
MICELIO_AUTH_TOKEN="$(openssl rand -hex 32)" micelio ui --host 0.0.0.0
```

`micelio ui --host 0.0.0.0` (or any address that is not loopback) refuses to
start without a token. Prefer the `MICELIO_AUTH_TOKEN` environment variable to
the flag: a flag value shows in the process list. TLS and network access
control should be provided by a trusted reverse proxy or host firewall.

With a token, CLI and MCP clients send `Authorization: Bearer <token>`. In a
browser, open the dashboard once as `http://host:3100/?token=<token>`: Micelio
sets an HttpOnly, SameSite=Strict cookie and removes the token from the address
bar. The cookie holds a value derived from the token (HMAC-SHA256), not the
token itself, and its name includes the port (`micelio_session_3100`), so two
dashboards on one host keep separate logins. Changing the token logs browsers
out.

The dashboard also refuses requests a web page could send through your browser:
API calls from another site's origin (`Origin` or `Sec-Fetch-Site: cross-site`)
get 403, and a tokenless loopback dashboard only answers a `Host` header that
names this computer (DNS rebinding). An origin must match the dashboard's
scheme, host and port: a page on another port of the same host (another local
app on `localhost:8000`, another service on `example.com:8443`) is refused,
because browsers send the login cookie to every port of a host. Clients that
send no `Origin` (curl, the CLI, `micelio mcp`) and requests with a valid
`Authorization: Bearer` token pass (a web page cannot add that header without
the token).

The Vite dev server (`npm --prefix dashboard run dev`) works without flags:
its proxy keeps the browser's `Host` (`changeOrigin: false` in
`dashboard/vite.config.ts`), so the request counts as the dashboard's own
origin:

```bash
go run ./cmd/micelio ui --port 3101
VITE_API_PROXY_TARGET=http://localhost:3101 npm --prefix dashboard run dev -- --host 127.0.0.1 --port 5175
```

A dev server or proxy that rewrites `Host` to the dashboard's needs its
exact origin allowed: `micelio ui --allowed-host http://127.0.0.1:5175`.

### Behind a reverse proxy or tunnel

The browser sends `Origin: https://<public name>`. Micelio accepts it when the
public name matches the `Host` header it receives, the first
`X-Forwarded-Host`, or an `--allowed-host`. Otherwise every POST, PUT, DELETE
and `/ws` from the browser gets 403. Do one of these:

- Keep the public `Host`: nginx `proxy_set_header Host $host;` (Caddy and
  cloudflared keep it by default).
- Send `X-Forwarded-Host`: nginx `proxy_set_header X-Forwarded-Host $host;`.
- Name the public host: `micelio ui --allowed-host micelio.example.com`
  (add the port when the public URL has one:
  `--allowed-host micelio.example.com:8443`).

A tokenless dashboard on loopback answers only `Host` headers that name this
computer, so a local proxy or tunnel that keeps the public `Host` gets 421
until you pass `--allowed-host <name>`. Use it only when the proxy itself
authenticates users: everyone who reaches the proxy controls the dashboard.
The proxy also needs WebSocket upgrades on `/ws` (nginx:
`proxy_http_version 1.1;`, `proxy_set_header Upgrade $http_upgrade;`,
`proxy_set_header Connection "upgrade";`).

```bash
MICELIO_AUTH_TOKEN="$(openssl rand -hex 32)" micelio ui --allowed-host micelio.example.com
```

## `mcp`: Let AI agents read your crawls

```bash
micelio mcp [flags]
```

Runs an MCP (Model Context Protocol) server on stdin/stdout. An AI agent such
as Claude Code can then list crawls, read a crawl summary, query pages and ask
why a URL is or is not indexable. Every call goes to the dashboard; if none
answers on this computer, `micelio ui` is started in the background. Full
guide: [MCP.md](MCP.md).

| Flag | Default | Meaning |
|---|---:|---|
| `--server <url>` | `http://127.0.0.1:3100` | Dashboard to read crawls from |
| `--auth-token <token>` | `MICELIO_AUTH_TOKEN` | Dashboard token, if it was started with `--auth-token`. Sent only over `https://` or to this computer |
| `--allow-crawl` | off | Also let the agent start, wait for and stop crawls (capped) |
| `--no-autostart` | off | Fail with a clear error instead of starting the dashboard |

```bash
claude mcp add micelio -- micelio mcp
```

## `build`: Rebuild an installed source build

```bash
micelio build [--skip-dashboard]
```

This command works when the binary was built with its source directory embedded,
as the Makefile does. It runs the appropriate Make targets and installs to
`$GOPATH/bin`. `--skip-dashboard` rebuilds only the Go binary.

For an explicit source build:

```bash
make build-with-dashboard
make install
```

## Environment variables

Explicit flags take precedence over environment variables.

| Variable | Used by |
|---|---|
| `MICELIO_AUTH_TOKEN` | `ui --auth-token` fallback |
| `MICELIO_ALLOWED_HOSTS` | `ui --allowed-host` names, comma-separated (added to the flags) |
| `PSI_API_KEY` | CLI PageSpeed Insights key fallback |
| `CRUX_API_KEY` | CLI CrUX key fallback |
| `OPENAI_API_KEY` | CLI OpenAI analysis and embeddings fallback |
| `ANTHROPIC_API_KEY` | CLI Anthropic analysis fallback |
| `PLAUSIBLE_API_KEY` | CLI Plausible key fallback |
| `MICELIO_GSC_CLIENT_ID` | `gsc-auth` OAuth client ID fallback |
| `MICELIO_GSC_CLIENT_SECRET` | `gsc-auth` OAuth client secret fallback |
| `MICELIO_PSI_KEY` | Dashboard Settings fallback |
| `MICELIO_CRUX_KEY` | Dashboard Settings fallback |
| `MICELIO_AI_KEY` | Dashboard Settings AI key fallback |
| `MICELIO_PLAUSIBLE_KEY` | Dashboard Settings Plausible key fallback |
| `MICELIO_SMTP_PASSWORD` | Email report password (dashboard and `schedule --email-to`) |
| `MICELIO_TELEGRAM_TOKEN` | Telegram bot token (dashboard and `schedule --telegram-chat`) |
| `MICELIO_ENV_FILE` | Path of the local secrets file; default `~/.micelio/micelio.env` |

Every alert channel setting can also come from `~/.micelio/micelio.env`. The
[user guide](USER-GUIDE.md#6-configure-integrations-and-defaults) lists the keys.

Avoid putting secrets directly in shell history. Use your shell's environment,
secret manager, or locally protected configuration.
