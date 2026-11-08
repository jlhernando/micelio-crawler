# Changelog

All notable changes to Micelio. Versions follow [semantic versioning](https://semver.org/). The version shown in the app and used by the in-app updater comes from the git tag.

## [Unreleased]

### Fixed

- Crawls block additional non-global address ranges, including IPv4 addresses hidden in IPv6 transition formats. Fake-IP proxies have an explicit, limited opt-in.
- Search Console and GA4 retry oversized response pages with smaller batches and retain rows fetched before a later error.
- Output rewrites preserve the original when flushing or closing fails. Failed log persistence is reported as a failed job.
- Refused restarts, late lifecycle calls and concurrent schedule creation no longer corrupt jobs or leave untracked schedules. Network checks resume only pauses they own and stop supervising deleted jobs.
- Typed crawl comparisons retain totals when legacy URL issue details have a different shape, while preserving valid issue lists.
- Legacy Node.js crawls handle sitemap-only configuration, stop on failed database writes, check reload size, save schedules under a cross-process lock and compress graph responses asynchronously.
- Legacy Node.js dashboard login uses an HttpOnly cookie and throttles wrong tokens. Configured tokens need at least 32 characters. Similarity yields during comparisons and reports its vector-width comparison limit.

### Development

- The Node.js engine needs Node.js 22.12 or newer. Test tooling uses Vitest 4.1.12. The dashboard development proxy accepts `VITE_API_PROXY_TARGET`.

## [1.14.1] - 2026-11-06

Dashboard and reliability fixes: the Monitor and sidebar show live, complete numbers, list crawls work from the dashboard, and about fifty smaller bugs are gone.

### Fixed

- **Monitor errors panel** lists 4xx/5xx pages as well as network errors, and a Monitor opened or reloaded mid-crawl shows the errors seen so far (the server keeps the latest 200, credentials in URLs masked). Before, it could say "No errors yet" next to a non-zero error counter.
- **List and sitemap crawls show their site** in the sidebar, History and Monitor: they use the first URL of the list or the first sitemap, also for crawls saved before.
- **Sidebar page count is live** and matches the Monitor (it read a number saved every 10 pages).
- **Dashboard list crawls** in the legacy TypeScript dashboard crawl the pasted URLs.
- **Search Console and GA4 ask for exactly the configured number of days** (the inclusive end date added one day).
- **Log analysis**: a byte-order mark no longer drops the first row; unreadable rows are counted, and a file in the wrong format fails with a clear message; W3C logs keep the query string; Ghost pages exclude redirects, errors, noindex and canonicalised pages; more real bots are verified.
- **No races or hangs** in pause, live tuning, schedule saves, import uploads, MCP autostart, update rollback and Google OAuth.
- **Pages table** sorts by body size; CSV export and Compare wait for pages to load instead of returning partial files.
- **MCP**: `crawl_summary` always fits its size limit; `crawl_stop` is marked destructive.
- **Large JSON-LD** is stored as valid JSON; Search Console rows match crawled URLs exactly first.
- **Bundled snippets** run in the Go crawler with `--js`; `performance.js` reports LCP and CLS.
- **Legacy TypeScript crawler**: JSON-LD validation, Plausible/GA4/Search Console/CrUX matching and retries, faster near-duplicate detection, redirect-aware link analysis, sitemap `lastmod` only from real dates, a time limit for regex searches, stricter segments and cron.

## [1.14.0] - 2026-11-06

A second round of security and reliability fixes: proxies and Tor work safely, credentials stay on the exact site, crawled content is treated as data everywhere it leaves Micelio, and the dashboard, log analysis and imports have firm limits.

### Security

- **Proxies get the checked address, never the host name.** With a proxy (HTTP, SOCKS5, Tor or `HTTPS_PROXY`), every target is resolved and checked on this computer first, and the proxy is asked for that IP. A proxy can no longer resolve a name to an internal address. Proxies must accept IP targets; plain `http` goes through `CONNECT` to port 80.
- **Local proxies and Tor work again.** A proxy you set yourself may run on this computer or the local network (`--proxy http://127.0.0.1:…`, `--proxy tor`); sites reached through it keep the full check. Before, such crawls returned 0 pages.
- **Dashboard crawls use only the proxy you trust.** Start the dashboard with `micelio ui --trusted-proxy <url>` (or `MICELIO_TRUSTED_PROXY`); any other proxy in a crawl config, schedule or resume is refused.
- **Credentials go to the same host or its www twin only**, on the same port and never after a switch from https to http. Other subdomains (shop tenants, shared hosting) no longer receive cookies or auth headers. Masked secrets are restored only for that same host.
- **Wrong tokens are throttled.** After 5 wrong tokens an address waits 1 s, doubling up to 5 min; a logged-in browser keeps working. The browser login posts the token instead of putting it in the URL, and `/ws?token=` is gone.
- **`micelio import` and `micelio mcp` check they are talking to your own dashboard** before sending a crawl or a token, so another program on the port cannot collect them.
- **Crawled content is treated as data.** CSV exports neutralise spreadsheet formulas, terminal output escapes control characters, the HTML report escapes attribute quotes, and MCP answers redact credentials in URLs, cap their size and mark page content as untrusted.
- Google OAuth callbacks check the state on every call; unknown SMTP security modes are refused instead of sending in clear text; webhook errors no longer log token-bearing URLs; Docker base images are pinned by digest.

### Fixed

- **Firm limits for the dashboard**: header, idle and no-progress timeouts, at most 64 WebSocket clients, shared work for identical requests, one log merge at a time, batched merge reads, and size caps for `micelio import` on the command line too.
- **JSON-LD** is read item by item with size caps, so a hostile page cannot blow up memory.
- **Log analysis** caps distinct URLs, decompressed size and format-detection buffering; turning GC off is safe with several analyses at once; bot verification runs on a bounded pool, caches by bot and IP, and accepts Googlebot only from `googlebot.com` and `google.com`. Speed is unchanged (about 270K lines/s).
- **Legacy TypeScript crawler**: OAuth with state and PKCE, CSV and terminal sanitising, caps on page, sitemap, JSON-LD, n-gram and embedding work, a safer AI prompt, redacted scheduler logs, no crash on a bad Host header, guarded webhook redirects and IP-pinned proxies.

## [1.13.0] - 2026-11-05

A client-ready PDF report with an SEO and GEO backlog, Core Web Vitals and CDN checks; hreflang and AI agent readiness audits; and a round of security and reliability fixes.

### Added

- **PDF report.** Results → *Export PDF* (CLI `--pdf <path>`) writes a client-ready A4 report in the dashboard's style: cover, summary, health scorecard, structure and speed, strongest pages. With the dashboard running it adds the site's **Universe** map, and a map inside each ticket it proves (errors, redirects, click depth, PageRank).
- **SEO and GEO backlog.** Findings become tickets (SEO-1…, GEO-1…) with priority, effort, owner, example URLs, fix steps and a measurable *Done when*, plus an impact × effort matrix and a Now / Next / Later board. *Export backlog* (CLI `--backlog <path>`) writes the same tickets as a CSV for JIRA's import, with SEO or GEO as the component.
- **GEO: ready for AI search?** A page and tickets on whether AI search and agents can reach and read the site: AI bot rules in robots.txt (18 bots), llms.txt, markdown for agents, JSON-LD and answer blocks. These are crawl-time signals, not proof that an AI system cites the site.
- **Core Web Vitals** in the report: LCP, INP and CLS of real Chrome users (Chrome UX Report, phones and desktop) and a Lighthouse lab run, with a ticket per failing metric. Needs a free Google API key in Settings or `MICELIO_CRUX_KEY` / `MICELIO_PSI_KEY`; the key travels in a header, never in the URL.
- **CDN & caching.** The report names the CDN and checks whether it caches HTML and static files, compression, HTTP/3, Early Hints and HSTS, with fix steps for the CDN in use.
- **Hreflang audit** from HTML, HTTP Link headers and XML sitemaps: invalid codes, missing return links and self references, x-default, targets that redirect, fail or are noindex, and HTML vs sitemap conflicts. New Results tab, HTML report section and MCP `crawl_summary.hreflang`.
- **Agent readiness audit** (`micelio agent-check <url>`, Results tab, `--no-agent-check`): robots.txt AI bot rules, Content-Signal, llms.txt, markdown negotiation, Link headers, /.well-known agent files and a live fetch as AI bots.

### Fixed

- **Off-site redirects** are stored as redirects instead of crawling the other site's page as part of this one. Devices tab compares raw and rendered HTML of both devices.
- Chrome UX Report CLS was never read.

### Security

- **Outbound requests can no longer reach this computer or the local network.** Webhooks, alert channels, Plausible and the Google token URL refuse private and loopback addresses, also after DNS and redirects; Chromium goes through the same guard.
- **The dashboard is locked down.** Requests from other sites (CSRF, DNS rebinding) are refused with a port-exact Origin check; a dashboard that listens beyond this computer needs an access token and refuses to start without one, and shows a login screen when the browser has none; pprof sits behind the same token.
- **Crawl configs are allowlisted and capped**: unknown keys are dropped, pages, concurrency, log uploads and imports have limits.
- **Credentials stay with the site they belong to.** Cookies, auth headers and API keys are masked in API responses, sent only to the crawled host, kept out of crawl databases and exports, and crawl files are owner-only. Re-crawl restores the stored secrets only for the same site. Header masking is default-deny; URLs and failed precheck output are redacted.
- **The legacy TypeScript crawler** gets the same SSRF guard, credential scoping and locked-down dashboard.

### Fixed

- Response time no longer includes rate-limiter and retry waits.
- One dashboard per data folder: a second `micelio ui` on the same folder exits instead of marking running crawls failed.
- Pausing could deadlock; analysis now runs per crawl; races between page loading and Universe fixed.
- Log × crawl merges keep their own cache table and are not lost; failed commits are reported; a crawl file in use is never deleted.
- Deferred analysis no longer copies every page; a wrong login token returns to the login screen.
- Legacy TypeScript crawler: cancel skips post-crawl work, bounded results cache, deleting a crawl removes its files, and 7 more reliability fixes.

## [1.12.0] - 2026-11-02

Crawl as any device, render on its screen, and compare two devices in one crawl.

### Added

- **Device profiles.** New Crawl has a Device card: Micelio bot (default), Desktop, Laptop, Tablet, Mobile, iPhone, Googlebot Smartphone, Googlebot Desktop, Bingbot or Custom, with a turn button for landscape. The device sets the User-Agent and client hints (`Sec-CH-UA-Mobile: ?1` on phones), so dynamic-serving sites answer with their mobile HTML, and robots.txt is read for the device's bot. Settings has a default device; Monitor, History and Results show it. CLI `--device`, `--viewport`, `--dpr`, `--mobile`, `--landscape`; MCP `crawl_start` takes `device`.
- **Compare two devices in one crawl.** Turn on *Compare with another device* (CLI `--compare-device`): every page is fetched, and with JS rendered, as both devices within the same per-site delay, so the crawl makes about twice the requests. Links only the second device sees are crawled too. Results get a **Devices** tab with pages compared, identical pages, text similarity and links only one device sees, one chip per difference (links, main content, status, redirect, canonical, robots, title, description, h1, structured data, hreflang, missing meta viewport) and a side-by-side view per page. Issues cards, a Universe *Device differences* pill, the HTML report and MCP `crawl_summary.devices` carry the same data.

### Fixed

- **JS rendering used a desktop screen for every crawl.** Chrome always rendered 1920×1080, even with a mobile User-Agent, so responsive sites showed their desktop menu and links. With a device, Chrome now emulates its screen, pixel ratio, touch and client hints. Without one it still renders 1920×1080.

## [1.11.0] - 2026-11-01

Universe moves like Figma, and the Links tab drops Link Explorer.

### Changed

- **Universe no longer grabs page scroll.** Scrolling with the cursor over the map used to zoom it. Now it works like Figma: plain scroll moves the page (a short hint says how to zoom), ⌘/Ctrl + scroll or a trackpad pinch zooms, and Shift + scroll or right-drag pans. Labels on the map zoom too.

### Removed

- **Link Explorer.** Links now has Universe and Treemap. Explorer's drill-down added little next to Universe, whose page card already lists inlinks and outlinks, and on small sites it drew a tangle. Its three graph libraries (sigma, graphology, ForceAtlas2) are gone too. The `/graph/subgraph` and `/graph/search` API endpoints stay.

## [1.10.1] - 2026-11-01

### Fixed

- **`micelio mcp` says when it changes a crawl to fit the caps.** `crawl_start` used to cut a request for 50,000 pages, concurrency 20 and no delay down to 10,000, 5 and 500 ms without a word. The answer now lists every changed value in `adjusted` (for example `limit 50000 -> 10000 (max 10000)`), and the message asks the agent to tell you.

## [1.10.0] - 2026-11-01

AI agents can read and run crawls through MCP, and no crawl request can reach a private address.

### Added

- **`micelio mcp`: an MCP server for AI agents.** Claude, Cursor and other MCP clients can list crawls, read a short summary (4 KB max), query pages, explain a URL and see its links. It talks to the dashboard over HTTP and starts `micelio ui` in the background if none is running (`--no-autostart` to fail instead). `--allow-crawl` lets the agent start, wait for and stop crawls, with fixed limits: 10,000 pages max, concurrency 5 max, at least 500 ms between requests, robots.txt always on, no JS rendering. New endpoint `GET /api/crawl/{id}/page`. See [docs/MCP.md](docs/MCP.md).

### Security

- **Every crawl request refuses private addresses.** One rule now covers loopback, private ranges, link-local (cloud metadata), CGNAT 100.64.0.0/10, 0.0.0.0/8, multicast and broadcast, IPv4-mapped IPv6 included. Before, stealth mode, page-weight checks and the sitemap fallback could reach a host name that resolved to 127.0.0.1. Host names that only start with `fc` or `fd` (like fdic.gov) are no longer blocked by mistake. Refused connections are not retried.
- **Headless Chrome goes through a guarded local proxy.** JS rendering and Universe thumbnails send every request, redirect, WebSocket and service worker through it, so a page cannot make the browser load a private address. If the proxy cannot start, Chrome gets no network at all.

### Fixed

- **A URL list that mixes sites crawls every URL.** List mode (`micelio list` and List on New Crawl) only crawled URLs on the first URL's site and silently dropped the rest: a list with example.com, example.org and iana.org gave 1 page of 3. Every site in the list is now crawled. `--allowed-domains` still limits the sites when set, and the log says how many URLs were skipped.

## [1.9.0] - 2026-10-31

The CLI can do every crawl the dashboard can, and JS rendering asks each page once.

### Added

- **The CLI has every crawl option the dashboard has.** `micelio crawl`, `micelio list` and `micelio sitemap` now share the same ~80 flags, so a list or sitemap crawl can use n-grams, link intelligence, the HTML report, JS rendering, PSI/CrUX/GSC/GA4, custom extraction and the rest. New flags on all three: `--retries`, `--save-html`, `--save-rendered`, `--path-only-filters` (off by default, so existing scripts behave as before) and `--config <file>`.
- **`--config` reads a crawl config saved from the dashboard** (the same JSON the New Crawl page sends). Flags you type win over the file. Keys the file leaves out keep the CLI defaults, so a file without `respectRobots` or `delay` still respects robots.txt and waits between requests.

### Changed

- `micelio list` and `micelio sitemap` now run the post-crawl analysis, write a crawl database and respect robots.txt, like `micelio crawl`. Use `--no-analysis`, `--no-db` or `--no-robots` for the old behaviour. Without `-o` or `--db` the database is named `<host>-list.db` or `<host>-sitemap.db`, so crawls of different sites never share one file.

### Fixed

- **JS rendering requests each page once.** The headless browser now gets the HTML the crawler already downloaded instead of fetching the page a second time, so the site sees one request per page, not two. Files the browser still loads itself wait their turn in the per-site delay, so rendering no longer goes faster than the delay or robots.txt Crawl-delay allow.

## [1.8.7] - 2026-10-30

### Fixed

- **robots.txt Crawl-delay is never undercut.** The per-site pacing booked each request's slot in advance, so after a request that went out late (opening a new connection, for example) the next one still left on time and the server saw two requests closer than the Crawl-delay (141 ms for a 200 ms delay). The gap now counts from when the previous request was really sent. Sitemap downloads, the missing-page check, page-weight requests and redirect hops now follow the same per-site pacing. Crawl speed is unchanged; crawls with many redirects get a little slower because each hop now waits too.
- PageRank options with only some fields set use the defaults for the rest (from #3, thanks @estevecastells).

### Changed

- **Shorter README.** The detail moved to the [User Guide](docs/USER-GUIDE.md), the [CLI Reference](docs/CLI-REFERENCE.md), [Architecture](docs/ARCHITECTURE.md) and [Contributing](CONTRIBUTING.md), with new screenshots and a Universe GIF. The guide covers everything up to this version.

## [1.8.6] - 2026-10-29

### Fixed

- **Deleting a crawl deletes its data file.** History removed the crawl but left its database in `~/.micelio/ui/crawls/`, so deleted crawls kept using disk (hundreds of MB for big ones). Now the file goes too, unless another crawl still uses it (a resumed crawl shares it) or it lives outside that folder (a custom path is yours). At start, Micelio removes once the files that older versions left behind.

### Changed

- A running crawl can no longer be deleted: cancel it first.

## [1.8.5] - 2026-10-29

### Fixed

- **Page Limit and the other number fields on New Crawl send what they show.** Clicking into a field rewrote "1,000,000" as "1000000" and lost your selection, so typing 150 over it gave 1000000150. The value was also only saved when you left the field. Now every keystroke counts, commas are ignored, and an empty or invalid field turns red and blocks Start with "<field>: enter a number." This applies to all 12 number fields.
- **A page picked in Universe's search lands in the middle of the visible map**, between the side panel and the page card, so a click there selects it. Orbit and zoom turn around the selected page.
- The Schedules dashboard test uses the current form again.

## [1.8.4] - 2026-10-28

### Added

- **Universe up front.** Links opens with Universe at the top and selected. Overview has a "Site architecture" card: a small map of the top 4,000 pages by PageRank and their links, with the page and cluster counts. A click opens Universe.
- **Status pills in Universe.** Errors, Redirects and Robots.txt, each with its count. A click lights up that kind of page and the links to it, and fades everything else, so you see where they sit in the architecture.
- **Link styles by target.** With "Redirects, errors & blocked" on, links to 4xx/5xx pages are solid red, links to redirects dashed yellow, links to URLs blocked by robots.txt dotted grey. The pages take the same colours.

### Changed

- **Universe draws the site's architecture only**: pages that were fetched and answered 2xx. Errors, URLs blocked by robots.txt and redirects to other sites come back with the "Redirects, errors & blocked" toggle. Same-site redirects stay, because the map merges them with the page they lead to.
- **A crawl starts in seconds.** Only robots.txt is read before the first request; sitemap discovery and the missing-page (soft 404) check run while the crawl goes. With sitemaps on, bbc.com went from 113.6 s to 2.4 s before its first page. The crawl still waits for sitemap URLs that are on their way, and the log has a `[timing] start` line.

### Fixed

- **No more links fanning out of empty space in Universe.** On big crawls, pages with low PageRank shrank to a faint pixel while their links still showed. Every page now stays a visible dot.

## [1.8.3] - 2026-10-28

### Changed

- **Failed URLs are retried, and failures never stop a crawl.** New Crawl's "Max Errors" is replaced by "Retries per URL" (default 2, max 10). A URL that fails with a network error, a timeout, 408, 429 or any 5xx is fetched again with growing pauses (Retry-After is honoured), then stored as failed. 404, 410, 403 and other 4xx are not retried. Before, only 429, 503 and network errors were, with a fixed 3 tries. `--max-errors` stays on the CLI.
- **"Timeout (seconds)" is now "Stop crawl after (minutes)"**: it always was the total crawl time, not a per-request timeout.
- **A crawl stopped by its time limit or by max errors says so.** Results shows the reason with a Resume button, and History offers Resume for these crawls.

### Fixed

- Settings edited when resuming a crawl are now saved. Before, resuming again brought back the old settings (for example a 60 s time limit).

## [1.8.2] - 2026-10-28

Fewer false broken links, a truthful speed reading, and New Crawl uses your saved defaults.

### Changed

- **New Crawl starts at a 200 ms delay** instead of 1,000 ms. The delay is per site, so 1,000 ms capped every crawl at 1 page/s whatever the concurrency; 200 ms allows up to 5 pages/s. New Crawl and Settings show the cap for the chosen delay.
- **Monitor speed is the last 15 seconds**, with the average and the delay's cap below it. The average since the start hid changes: after Adjust from 800 to 200 ms it still read 3.0 pages/s while the crawl ran at 5.0.

### Fixed

- **New Crawl now uses Settings → Default Crawl Settings** (delay, concurrency, depth, limit, user agent, external links, JS rendering, robots, n-grams, embeddings, link intelligence…). Before, the page ignored them. Settings' own defaults now match New Crawl, so saving Settings for something else does not change your next crawl.

- **A 429 is no longer reported as a broken external link.** 429 means the site asks us to slow down. In a real crawl, GitHub answered 429 to 316 links after about 750 checks, and all 316 showed as broken. Now a site that answers 429 is checked one URL at a time after a pause (its Retry-After, else 5 s, doubling up to 60 s) and the URL is retried. If it keeps answering 429, its remaining URLs are left unchecked, never broken, and Issues says which sites and how many URLs. Re-run: all 805 GitHub URLs checked, none reported as 429.
- The Monitor no longer shows "Loading pages for analysis" and a low % under "Crawl Complete" after Stop & Analyze.

## [1.8.1] - 2026-10-27

### Changed

- **External link check about 3× faster.** Each site now has its own queue, served in turn, so hundreds of links to one site no longer block checks of every other site. Large platforms (GitHub, YouTube, Wikipedia, X, Google…) get 6 checks at once instead of 2. A site that does not resolve or refuses connections fails its other URLs without a request each. On a blog with about 1,800 external URLs the check went from 200 s to 60 s, with the same broken links found.
- The server log names the three sites that took the most time to check.

## [1.8.0] - 2026-10-27

Change a running crawl, and get results right after Stop & Analyze.

### Added

- **Adjust a running crawl.** Monitor → Adjust changes the delay, concurrency and page limit without stopping the crawl (also `POST /api/crawl/{id}/tune`). The delay never goes below the robots.txt Crawl-delay, and adaptive rate still slows down when the server struggles. The change is kept if the crawl is resumed and shows as a "tuned" event. Filters, depth, robots and JS rendering stay fixed so the analysis stays consistent.

### Changed

- **Stop & Analyze shows results in seconds.** External links are checked after the results are shown, not before. The Results page shows "Checking external links n / total" and adds the broken ones when done. In a test with 1,895 external URLs, the wait after Stop & Analyze went from 226 s to 0.4 s, with the same broken links found.
- External links are checked 20 at a time (still at most 2 per site) and wait up to 6 s per URL, instead of the crawl's concurrency and 10 s.
- Monitor: "Pending" shows the URLs really waiting in the queue, and the % is based on the URLs found so far when the page limit is far above the site's size.
- The server log has a `[timing]` line with how long each post-crawl step took.

### Fixed

- Raising the page limit of a running crawl a lot no longer makes new URLs look already crawled.

## [1.7.2] - 2026-10-27

### Changed

- **One place for alert channels.** `micelio schedule` now uses the email, Telegram and Slack channels saved in the dashboard's Settings (or `~/.micelio/micelio.env`). It reports skipped and failed runs, and completed runs when "Send a report after every crawl" is on. `--email-to` and `--telegram-chat` still override Settings for one schedule.

## [1.7.1] - 2026-10-27

### Added

- **Keep tokens and IDs out of git.** Micelio reads `~/.micelio/micelio.env` (or the file in `MICELIO_ENV_FILE`) at start. Every alert channel setting can live there: SMTP account, Telegram bot token and chat ID, Slack and webhook URLs, dashboard address. Values saved in Settings win; blank fields fall back to the file. Micelio warns if other users can read it.
- CI fails if a Telegram bot token or a Slack/Discord webhook URL is committed.

### Fixed

- Saving the Settings page with blank alert fields no longer hides values set in the environment.
- A scheduled crawl paused by a failing check now shows as paused at the same moment the crawl pauses.

## [1.7.0] - 2026-10-26

Crawl reports on Telegram and Slack, with key points.

### Added

- **Telegram reports.** Create a bot with @BotFather, paste its token in Settings → Alert Notifications, message the bot (or add it to a group or channel) and click "Find my chat". The token stays local and is never shown again (or set `MICELIO_TELEGRAM_TOKEN`).
- **Key points**: every report starts with up to five one-line takeaways, such as new soft-404 candidates, a drop in indexable pages or more errors.
- Test buttons for email, Telegram and Slack.
- `micelio schedule --telegram-chat <id>` (token in `MICELIO_TELEGRAM_TOKEN`).

### Changed

- **Slack** now gets the full crawl report (key points, alerts, changes since the previous crawl with example URLs, link to the crawl) instead of only the alert list, and only when there is something to report.
- One option, "Send a report after every crawl", applies to all channels.

### Fixed

- The Alert Notifications section of Settings failed to open in 1.6.0, so email could not be set up from the dashboard.

## [1.6.0] - 2026-10-26

Alert emails that say what changed.

### Added

- **Email alerts** (Settings → Alert Notifications → Email). Any SMTP account works: Gmail or Outlook with an app password, SendGrid, Resend or a company relay. STARTTLS by default; "Send test email" checks the account. The password stays local and is never shown again (or set `MICELIO_SMTP_PASSWORD`).
- **Changes since the previous crawl** of the same schedule or site: pages crawled, 4xx/5xx and 5xx responses, indexable share, new and resolved soft-404 candidates, and URLs no longer found, each with up to 10 example URLs and a link to the crawl.
- Emails go out only when there is something to read: critical or new alerts, an important change, or a scheduled run that was skipped or failed. Tick "Email every crawl" to get one after every run.
- New alert rule for high-confidence soft-404 candidates (1% of pages or 10 pages), also sent to webhooks and Slack.
- `micelio schedule --email-to ... --smtp-host ... --smtp-from ...` emails each CLI scheduled run.

## [1.5.0] - 2026-10-26

Automatic soft-404 detection.

### Added

- **Soft-404 candidates with confidence and reasons.** Pages that answer 200 (or redirect) but show no real content are flagged with five signals instead of an English phrase list:
  - At the start of a crawl Micelio requests one URL that cannot exist. If the site answers 200, pages that look like that "not found" page are high-confidence candidates. Turn it off with `--no-soft404-probe` or on New Crawl.
  - "Not found" and "0 results" wording in English, Spanish, Portuguese, French, Italian, German, Polish and Romanian.
  - Listings with no product or ad links while similar listings have them (low alone, since some sites load products with JavaScript; high with "no results" wording).
  - Deep URLs redirected to the home page, and many URLs redirected to one page.
  - Pages with far less text than others of the same template.
- The Issues tab lists candidates with their confidence and reasons. They are candidates to review; Search Console's page indexing report confirms.

## [1.4.0] - 2026-10-26

Crawl on a small cloud server, analyse on your computer.

### Added

- **Import crawls.** `micelio import <file>` and **History → Import crawl…** add a crawl made elsewhere to History and run the dashboard's full analysis on this computer: stats, issues, PageRank, Universe and the directory tree. They take the crawl database or the JSONL output (`.jsonl`, `.gz`, `.zst`). The CLI sends the file to a running dashboard, or imports it straight into `~/.micelio`.
- **Lean crawls.** `micelio crawl --no-analysis` only crawls and saves; `--db-only` writes just the crawl database. A 1,136-page crawl used 63 MB of memory on one CPU core.
- **[Crawl in the cloud](docs/CLOUD-CRAWLS.md)** guide: server size, moving the files, importing, and what an import cannot rebuild.

### Changed

- Dashboard crawls and imports share the same post-crawl analysis code.
- The crawl database records when the crawl finished, so imports show the real duration.

## [1.3.7] - 2026-10-25

### Fixed

- **Rollback restores a working binary.** The updater saved its backup without the execute permission, so "Roll back" left a Micelio that would not start ("permission denied"). Backups now keep the binary's permissions, and rollback also repairs backups made by older versions.

## [1.3.6] - 2026-10-25

### Fixed

- **FAQ, contact and legal pages on shops get their own type.** Shops put the same large menu on these pages as on category pages, so link counts made them look like listings. When the last part of the URL and the H1 agree (for example `/customer-service/faq/` with "Preguntas frecuentes"), the page is now classified as FAQ, contact or legal. Works in English, Spanish, Portuguese, French, Italian, German, Polish, Dutch and Romanian. Pages with product signals are not affected.
- **Scheduled runs** no longer risk saving a stale "paused" state after the run ends.

## [1.3.5] - 2026-10-25

Three fixes found on a 10,000-page ecommerce test crawl.

### Fixed

- **Help pages are no longer classified as products.** A cart or checkout link in the site header, menu or footer counted as a product-page signal, so FAQ, terms and store pages on a shop showed up as products. Only signals in the page's own content count now. Real product pages are unchanged.
- **Universe error count.** URLs blocked by robots.txt were counted and coloured as errors (166 shown where the crawl had 3). They now count as normal pages.
- **Folder include filters accept the folder page.** A filter like `^https://example.com/shop/` rejected `https://example.com/shop` (Micelio removes the trailing slash), so a crawl started at that folder ended with 0 pages. Include and exclude filters now also try the URL with its trailing slash.

## [1.3.4] - 2026-10-25

### Fixed

- **Links with spaces or accents in the query no longer return 400.** A link like `?filter=Acero inoxidable` was sent with a raw space, and servers answered 400 Bad Request. Micelio now encodes the query the way a browser does (`%20`, `%C3%AD`). On a 3,000-page ecommerce test crawl this removed 172 false errors and raised indexable pages from 82.7% to 88.3%.

## [1.3.3] - 2026-10-25

Scheduled crawls get the full crawl setup, show up in History, and can be limited to an allowed network.

### Added

- **Schedule any crawl.** "Schedule…" on New Crawl schedules the current setup: sitemap or list mode, include/exclude patterns, adaptive rate, extractions and every other option. The Schedules page keeps a quick form.
- **Runs appear in History.** Each scheduled run is a normal crawl, tagged with its schedule, so you can open it, compare runs with Diff and get alerts.
- **Schedules survive restarts.** Dashboard schedules are saved and resume when Micelio starts.
- **"Only run when…" checks.** Limit runs to a public IP range (for example, your office network). A failing check skips the run with a reason. During a crawl the check repeats every minute: the crawl pauses until it passes again and stops (with analysis) after 6 hours paused.
- **Run now** and **Test check** buttons, the reason for skipped or stopped runs, and a link to the last run's crawl.
- Webhooks also report skipped and stopped runs (`crawl_skipped`, `crawl_stopped`) with a reason.
- **CLI:** `micelio schedule --config file.json` takes a crawl config in the dashboard's JSON format. New flags: `--name`, `--precheck-egress`, `--precheck-cmd` (shell command that must exit 0; CLI only), `--precheck-interval` and `--check-only`.

### Changed

- Signal and report descriptions now describe Micelio's crawl-time proxies plainly and point to Search Console, logs and analytics for validation.

### Fixed

- A flaky server test that raced post-crawl work against temp-dir cleanup.

## [1.3.2] - 2026-10-24

Kinder to the sites you crawl, much lighter on your machine, and a smaller dashboard.

### Fixed: crawling politeness

- **Crawls started from the dashboard ignored the delay.** The dashboard sent `delay` and the server read `delayMs`, so every dashboard crawl ran with no delay between requests. List mode from the dashboard also crawled nothing. Both now work.
- **Adaptive rate no longer goes faster than your delay or the site's robots.txt Crawl-delay.** It used to speed up to 50 ms between requests. Each host now has its own rate.
- **robots.txt follows Google's rules:** `*` and `$` wildcards, query strings, longest match wins, groups chosen by product token. It is fetched through your proxy, follows redirects, is parsed once per host, and is checked for every allowed domain, not only the start URL. Expect more URLs to be blocked than before.
- **429 and 503 responses:** `Retry-After` pauses the host for all workers (up to 10 minutes). At most 3 attempts per URL. Timeouts and network errors also slow the crawl down.
- **Non-HTML files** (PDFs, videos, archives) are no longer downloaded in full just to read the status code. At most 64 KB is read.
- **Page weight checks** use the crawl's client and proxy, with at most 2 requests at a time per host.

### Fixed: stability

- Two requests to `/stats` at the same time could crash the server.
- Diff crashed on the URLs and Technical tabs when a list was empty.

### Changed: memory and speed (measured on 200,000 pages)

- Stats are capped: long URL lists keep the top 1,000 plus a total, and PageRank is stored per page with a histogram. Stats JSON: 25.8 MB → 0.22 MB.
- Pages are written in batches outside the crawl lock: 83.5 → 7.4 µs per page with 16 workers.
- Inlinks use a shared reverse index: 2.7 ms → 42 ns per lookup. Self-links no longer count as inlinks anywhere.
- The link graph, PageRank and HITS scores are stored in the crawl database, so reopening a crawl is about twice as fast. Authority and hub scores no longer reset to 0 after a restart.
- Embeddings compare pages 37 times faster. Above 10,000 pages they compare pages within the same folder.
- `/pages` caches sorted results; the crawl cache loads each crawl once and can evict by memory when `--memlimit` is set.
- Custom-search regexes are compiled once.
- Removed the unused `/api/crawl/{id}/results` and `/api/crawl/{id}/graph` endpoints, which could allocate several GB on large crawls.

### Changed: dashboard

- Main bundle: 827 KB → 369 KB (247 → 108 KB gzipped). Secondary pages, Explorer, Universe, Treemap and charts load when opened.
- The Content tab's word-count histogram now covers every page (it fetched 50,000 pages and used 100).
- Diff lists show 200 rows with "Show more".
- One WebSocket for the whole dashboard; shared number, date and status formatting.
- Removed 7 unused dependencies.

### Docker

- The Docker image now builds the Go binary with the embedded dashboard. It used to build the old TypeScript crawler. Add `--build-arg WITH_CHROMIUM=true` for JS rendering.

## [1.3.1] - 2026-10-24

### Added

- **Universe page search.** Find a page by title or URL and fly to it.
- **Metric colours in Universe:** Depth, PageRank, Authority and Hub, with a legend. Authority and hub use percentiles because a few pages hold most of the score.
- **Open in Explorer** on a selected page switches to Graph Explorer with that page open.

### Removed

- **Full Graph.** Universe covers the whole-site view for any crawl size. This drops the Cosmos.gl dependency and three web workers.

### Fixed

- Graph Explorer search left the view empty: the camera jumped to graph coordinates instead of screen coordinates.
- Authority and hub scores were 0 for every crawl opened after a restart, because they were never stored with the pages. They are now recomputed from the link graph when a crawl loads (crawls up to 200,000 pages, the same limit as during a crawl).

## [1.3.0] - 2026-10-24

A 3D map of your site's internal links, and directory views that stay fast on 1M-page crawls.

### Added

- **Universe** (Links > Universe). A 3D map where every dot is a page and every line is a real internal link.
  - Each page sits next to the page that links to it on its shortest click path from the start page. Branches with more pages below them get more room.
  - Pages the start page cannot reach by links sit on an outer shell and are labelled as such.
  - Layout toggle: **Link structure** (default) or **Grouped** (pages around their cluster's centre, with lines between clusters showing real link counts).
  - Colour by **Section**, Folder, Subfolder or Template. Section finds the folder most of the site shares and groups by the folder below it.
  - **Cross links** and **Menu, footer & section nav links** toggles add the links that hide the structure on large sites.
  - Click a page to see its inlinks (green), outlinks (amber), click path ("Reached in 2 clicks via …") and a thumbnail.
  - Side panel with sortable cluster and page tables.
- **Page thumbnails**, captured with headless Chrome when you open a page and cached in `~/.micelio/ui/thumbs/`. They are removed when the crawl is deleted.
- **Progressive loading** for large crawls. Universe loads the first 10,000 pages by PageRank, then keeps going until a limit set by the device's memory, CPU and GPU, or until a frame takes longer than 14 ms. Pages and links travel as compact binary data. The 3D view only redraws when something changes.

### Changed

- **Directory tree and treemap** load one folder at a time instead of the whole site. Open a folder to fetch its subfolders and pages, with **Load more** for the rest. A failed folder shows Retry instead of hiding the tree.
- The full directory tree response is capped at 50 children per folder and 5,000 nodes.
- Dashboard built with Vite 8, with unused dependencies removed. The JS renderer uses the chromedp v0.21 API.

### Fixed

- The Links tab could use more than 4 GB of browser memory on a 200,000-page crawl, because the directory tree sent one row per page. A 1M-page crawl now stays near 1 GB with a 9 MB JavaScript heap.
- The directory tree root showed as `//`.
- GSC, GA4 and Plausible numbers in the directory tree are kept after a crawl's pages are trimmed in memory. Values that cannot be known show "—" instead of 0.
- Encoded paths such as `/caf%C3%A9/` now match between the tree views.

## [1.2.0] - 2026-10-22

Automatic page segmentation, correct PageRank on large crawls, and link calculations you can trust.

### Added

- **Segments tab.** Every page gets a value in 15 dimensions: template, directory L1 to L3, extension, text amount, link profile, language, URL parameters, click depth, authority, index state, schema type, host and freshness. Click a value to open All Pages filtered to it.
- JavaScript rendering differences in Issues, and Link Structure in Links.

### Changed

- Sturdier template detection: ties no longer resolve at random, and commerce signals tell product pages from listings.

### Fixed

- PageRank on DB-backed crawls: every page used to score 10.
- Orphans, inlinks, broken-link sources, links to non-indexable pages and link counts now come from the link graph.
- Anchor stats and nofollow counts are collected during the crawl.
- Check External works from the dashboard.
- Structured data is validated.
- Template-filtered stats no longer show false zeros.

## [1.1.0] - 2026-10-20

A redesigned dashboard, clearer crawl results, and security fixes.

### Added

- Crawl coverage on the results overview, with the reason for each exclusion and example URLs.
- Priority issues at the top of the overview.
- User Guide and CLI Reference in `docs/`.

### Changed

- New dashboard look across every page.

### Security

- Patched vulnerable Go and npm dependencies. Requires Go 1.26.5.
- GitHub Actions pinned to commit SHAs, with tighter token permissions. Dependabot enabled.
- Release binaries no longer embed the build machine's source path.

## [1.0.0] - 2026-07-05

First public release: spider, list, sitemap, HEAD-only and scheduled crawls; embedded web dashboard; SEO, content and link analysis; server log analysis; PSI, CrUX, GSC, GA4, Plausible and AI integrations; JS rendering; robots.txt testing; and an in-app updater.

[1.3.3]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.2...v1.3.3
[1.3.2]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.1...v1.3.2
[1.3.1]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/jlhernando/micelio-crawler/releases/tag/v1.0.0
