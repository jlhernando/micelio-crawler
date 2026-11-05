# Micelio for AI agents (MCP)

`micelio mcp` lets an AI agent such as Claude Code or Claude Desktop read your
crawls. The agent can list crawls, get a short summary of one, query its pages
and ask why a URL is or is not indexable. With `--allow-crawl` it can also
start a small, polite crawl and wait for it.

MCP (Model Context Protocol) is the standard way AI apps call outside tools.
The agent starts `micelio mcp` and talks to it over stdin/stdout.

## How it works

`micelio mcp` does not read any crawl files itself. Every tool call goes to the
Micelio dashboard over HTTP (`http://127.0.0.1:3100` by default), the same API
the web dashboard uses. So what the agent sees is what you see in the
dashboard.

If no dashboard answers when the first tool is called, `micelio mcp` starts
`micelio ui --port 3100` in the background and waits up to 20 seconds for it.
The dashboard keeps running after the agent closes; its log goes to
`~/.micelio/ui/mcp-dashboard.log`. Only a dashboard on this computer is
started. With `--no-autostart` the tool returns an error that tells you to run
`micelio ui` instead.

## Setup

### Claude Code

```bash
# Read-only: list and explore crawls
claude mcp add micelio -- micelio mcp

# Also let the agent start crawls (with the safety caps below)
claude mcp add micelio -- micelio mcp --allow-crawl
```

### Claude Desktop

Add this to `claude_desktop_config.json` (Settings > Developer > Edit Config).
Use the full path of the binary (`which micelio`), because Claude Desktop does
not use your shell's `PATH`:

```json
{
  "mcpServers": {
    "micelio": {
      "command": "/usr/local/bin/micelio",
      "args": ["mcp"]
    }
  }
}
```

If your dashboard runs with `--auth-token`, add the token:

```json
{
  "mcpServers": {
    "micelio": {
      "command": "/usr/local/bin/micelio",
      "args": ["mcp", "--allow-crawl"],
      "env": { "MICELIO_AUTH_TOKEN": "your-dashboard-token" }
    }
  }
}
```

## Flags

| Flag | Default | Meaning |
|---|---:|---|
| `--server <url>` | `http://127.0.0.1:3100` | Dashboard to read crawls from |
| `--auth-token <token>` | `MICELIO_AUTH_TOKEN` | Dashboard token, if it was started with `--auth-token` |
| `--allow-crawl` | off | Add `crawl_start`, `crawl_wait` and `crawl_stop` |

The token is only sent over `https://` or to a dashboard on this computer
(`localhost`, `127.0.0.1`, `::1`). `micelio mcp --server http://other-host:3100`
with a token refuses to start; put the remote dashboard behind HTTPS. Redirects
to another origin or from `https` to `http` are not followed with the token.
| `--no-autostart` | off | Fail with a clear error instead of starting the dashboard |

## Tools

Read-only tools (always there):

| Tool | What it returns |
|---|---|
| `crawls_list` | Crawls, newest first: id, start URL, status, date, pages, errors. Filters: `site`, `status`, `limit` |
| `crawl_status` | One crawl: status, pages crawled, URLs queued, errors, phase, whether background analysis still runs |
| `crawl_summary` | A short digest (under 4 KB) of a finished crawl: totals, status codes, indexability, the top 10 issues with counts, 3 sample URLs each and a `pages_query` filter, soft-404 candidates, response-time percentiles |
| `pages_query` | Pages with filters (`status`, `indexable`, `issue`, `robotsBlocked`, `template`, `segment`, `search`), `sort`, `order`, `offset`, `limit` (default 25, max 200) and `fields`. Returns `{total, rows}` with only the fields asked for |
| `url_explain` | Whether one URL is indexable and why: status, redirects, canonical, meta robots, X-Robots-Tag, robots.txt, soft-404 signals, depth, inlinks, PageRank, sitemap |
| `url_links` | Pages linking to a URL (`direction: in`) or the internal links on it (`direction: out`) |

`pages_query` fields: `url`, `status`, `indexable`, `indexabilityReason`,
`title`, `titleLength`, `metaDescription`, `metaDescriptionLength`, `h1`,
`h1Count`, `depth`, `inlinks`, `outlinks`, `externalLinks`, `wordCount`,
`responseTimeMs`, `canonical`, `metaRobots`, `xRobotsTag`, `finalUrl`,
`redirectHops`, `pageRank`, `templateType`, `segments`, `soft404`,
`robotsBlocked`, `bodySize`, `error`, `htmlLang`, `inSitemap`,
`classification`. The default is `url, status, indexable, title, depth,
inlinks`.

Crawl tools (only with `--allow-crawl`):

| Tool | What it does |
|---|---|
| `crawl_start` | Starts a crawl (`url`, `mode` spider/list/sitemap, `urls`, `limit`, `depth`, `concurrency`, `delayMs`, `device` such as `googlebot-smartphone`) and returns its id at once |
| `crawl_wait` | Waits up to 50 s (default 45) and returns the status with `done`. Sends progress notifications when the client asks for them. Call again while `done` is false |
| `crawl_stop` | Stop & Analyze: no new pages are fetched and the pages so far are analysed. Nothing is deleted |

A typical session: `crawl_start` → `crawl_wait` until `done` → `crawl_summary`
→ `pages_query` with an issue's filter → `url_explain` on a sample URL.

## Safety

- **Read-only by default.** Without `--allow-crawl` the agent cannot start or
  stop anything.
- **Crawl caps the agent cannot raise.** `crawl_start` uses at most 10,000
  pages (default 500), 5 parallel requests (default 3) and at least 500 ms
  between requests (default 1,000 ms). robots.txt is always respected,
  adaptive rate limiting is on and JavaScript rendering is off. When a value
  is changed to fit a cap, the answer lists it in `adjusted` (for example
  `limit 50000 -> 10000 (max 10000)`) and the message asks the agent to tell
  you. The dashboard
  runs one crawl at a time: if one is running, `crawl_start` says so and gives
  its id.
- **No secrets.** The tools never call the settings API and never return a
  crawl's config (which can hold API keys). Error messages are scrubbed of
  bearer tokens, `key=`/`token=` values and well-known key formats.
- **Page text is data.** Titles, descriptions and headings are cut to 200
  characters and come from the crawled websites, so an agent should never
  follow instructions found in them. Raw HTML is never returned.
- **Errors are tool errors.** A missing crawl or a stopped dashboard comes back
  as a readable tool error the agent can act on, not as a protocol failure.
- **Clean stdout.** Only the MCP protocol is written to stdout; logs go to
  stderr.
