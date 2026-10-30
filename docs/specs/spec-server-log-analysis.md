# Server Log Analysis for SEO - Specification

## Overview

A standalone server log analysis module that parses web server access logs, identifies search engine and AI bot activity, analyzes crawl budget efficiency, detects crawl waste, and merges log data with crawl results for comprehensive SEO insights.

---

## 1. Log Format Specifications

### 1.1 Apache Common Log Format (CLF)

**Format string:** `%h %l %u %t \"%r\" %>s %b`

**Example:**
```
127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.1" 200 2326
```

**Field positions:**
| # | Directive | Field | Description |
|---|-----------|-------|-------------|
| 1 | `%h` | Remote host | Client IP address |
| 2 | `%l` | Identity | RFC 1413 identity (almost always `-`) |
| 3 | `%u` | User | Authenticated username (or `-`) |
| 4 | `%t` | Timestamp | `[dd/Mon/YYYY:HH:MM:SS +/-ZZZZ]` |
| 5 | `%r` | Request line | `"METHOD /path HTTP/version"` (quoted) |
| 6 | `%>s` | Status | Final HTTP status code |
| 7 | `%b` | Bytes | Response size in bytes (or `-` if zero) |

**Regex:**
```regex
^(\S+) (\S+) (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\S+)
```

### 1.2 Apache Combined Log Format

**Format string:** `%h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\"`

**Example:**
```
127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.1" 200 2326 "http://www.example.com/start.html" "Mozilla/5.0 (compatible; Googlebot/2.1)"
```

**Additional fields beyond CLF:**
| # | Directive | Field | Description |
|---|-----------|-------|-------------|
| 8 | `%{Referer}i` | Referer | HTTP Referer header (quoted) |
| 9 | `%{User-Agent}i` | User-Agent | Full user agent string (quoted) |

**Regex:**
```regex
^(\S+) (\S+) (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\S+) "([^"]*)" "([^"]*)"
```

### 1.3 Nginx Default Format

**Format directive:**
```nginx
log_format combined '$remote_addr - $remote_user [$time_local] '
                    '"$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent"';
```

Identical to Apache Combined format. Same regex applies.

**Common Nginx variables for custom/extended formats:**
| Variable | Description |
|----------|-------------|
| `$remote_addr` | Client IP |
| `$remote_user` | HTTP authenticated user |
| `$time_local` | Local time in CLF format |
| `$time_iso8601` | ISO 8601 timestamp |
| `$request` | Full request line |
| `$status` | HTTP status code |
| `$body_bytes_sent` | Response body size (excluding headers) |
| `$bytes_sent` | Total bytes sent including headers |
| `$http_referer` | Referer header |
| `$http_user_agent` | User-Agent header |
| `$request_time` | Total processing time (seconds, ms precision) |
| `$request_length` | Request length including headers and body |
| `$upstream_response_time` | Time to receive full response from upstream |
| `$upstream_connect_time` | Time to establish upstream connection |
| `$upstream_header_time` | Time to receive first header byte from upstream |
| `$upstream_status` | Upstream response status code |
| `$connection` | Connection serial number |
| `$connection_requests` | Number of requests on this connection |
| `$msec` | Time in seconds with millisecond resolution |
| `$pipe` | "p" if pipelined, "." otherwise |
| `$server_name` | Name of server that accepted request |
| `$ssl_protocol` | TLS protocol version |
| `$ssl_cipher` | TLS cipher used |
| `$http_x_forwarded_for` | X-Forwarded-For header (real client IP behind proxies) |
| `$gzip_ratio` | Compression ratio |

**Recommended Nginx extended format for SEO log analysis:**
```nginx
log_format seo_extended '$remote_addr - $remote_user [$time_local] '
    '"$request" $status $body_bytes_sent '
    '"$http_referer" "$http_user_agent" '
    'rt=$request_time uct=$upstream_connect_time '
    'uht=$upstream_header_time urt=$upstream_response_time';
```

### 1.4 IIS W3C Extended Log Format

**Key characteristic:** Self-describing format with header directives. Fields are configurable by the administrator and declared in the `#Fields:` header line. Fields are space-delimited. Times are in UTC.

**Header structure:**
```
#Software: Microsoft Internet Information Services 10.0
#Version: 1.0
#Date: 2024-01-15 00:00:00
#Fields: date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Referer) sc-status sc-substatus sc-win32-status time-taken
```

**Complete available fields:**
| Field | Prefix | Description |
|-------|--------|-------------|
| `date` | - | Date of request (YYYY-MM-DD, UTC) |
| `time` | - | Time of request (HH:MM:SS, UTC) |
| `c-ip` | c- | Client IP address |
| `cs-username` | cs- | Authenticated username |
| `s-sitename` | s- | Service name and instance number |
| `s-computername` | s- | Server name |
| `s-ip` | s- | Server IP address |
| `s-port` | s- | Server port number |
| `cs-method` | cs- | HTTP method (GET, POST, etc.) |
| `cs-uri-stem` | cs- | URI path (target of action) |
| `cs-uri-query` | cs- | Query string |
| `sc-status` | sc- | HTTP status code |
| `sc-substatus` | sc- | HTTP substatus code |
| `sc-win32-status` | sc- | Windows status code |
| `sc-bytes` | sc- | Bytes sent by server |
| `cs-bytes` | cs- | Bytes received from client |
| `time-taken` | - | Time to complete request (milliseconds) |
| `cs-version` | cs- | HTTP protocol version |
| `cs-host` | cs- | Host header value |
| `cs(User-Agent)` | cs- | User-Agent header |
| `cs(Cookie)` | cs- | Cookie header |
| `cs(Referer)` | cs- | Referer header |

**Prefix convention:** `c-` = client, `s-` = server, `cs-` = client-to-server, `sc-` = server-to-client

**Parsing approach:** Must read the `#Fields:` header line first to determine column order, then parse data lines accordingly. A dynamic parser is required since field selection varies per installation.

### 1.5 AWS CloudFront Access Logs

**Format:** Tab-delimited, gzip-compressed files (`.gz`). Two header lines (version and fields).

**All 33 fields in order:**
| # | Field | Description |
|---|-------|-------------|
| 1 | `date` | Event date (YYYY-MM-DD, UTC) |
| 2 | `time` | Time server finished responding (HH:MM:SS, UTC) |
| 3 | `x-edge-location` | Edge location code (e.g., DFW3) |
| 4 | `sc-bytes` | Total bytes server sent to viewer |
| 5 | `c-ip` | Viewer IP address |
| 6 | `cs-method` | HTTP method |
| 7 | `cs(Host)` | Distribution domain name |
| 8 | `cs-uri-stem` | URI path |
| 9 | `sc-status` | HTTP status code (or 000) |
| 10 | `cs(Referer)` | Referer header |
| 11 | `cs(User-Agent)` | User-Agent (URL-encoded) |
| 12 | `cs-uri-query` | Query string |
| 13 | `cs(Cookie)` | Cookie header |
| 14 | `x-edge-result-type` | Cache result (Hit, Miss, Error, etc.) |
| 15 | `x-edge-request-id` | Unique request identifier |
| 16 | `x-host-header` | Host header from viewer |
| 17 | `cs-protocol` | Protocol (http, https, ws, wss, grpcs) |
| 18 | `cs-bytes` | Bytes in viewer request |
| 19 | `time-taken` | Seconds from receipt to final byte |
| 20 | `x-forwarded-for` | Viewer IP if behind proxy |
| 21 | `ssl-protocol` | TLS protocol version |
| 22 | `ssl-cipher` | TLS cipher |
| 23 | `x-edge-response-result-type` | Response classification before return |
| 24 | `cs-protocol-version` | HTTP version |
| 25 | `fle-status` | Field-level encryption status |
| 26 | `fle-encrypted-fields` | Number of FLE encrypted fields |
| 27 | `c-port` | Viewer port number |
| 28 | `time-to-first-byte` | Seconds to first response byte |
| 29 | `x-edge-detailed-result-type` | Detailed result type with error info |
| 30 | `sc-content-type` | Content-Type header |
| 31 | `sc-content-len` | Content-Length header |
| 32 | `sc-range-start` | Range start (if Content-Range) |
| 33 | `sc-range-end` | Range end (if Content-Range) |

**Parsing notes:** User-Agent is URL-encoded and must be decoded. Files may also include `distribution-tenant-id` and `connection-id` as additional fields. As of late 2024, CloudFront also supports JSON and Apache Parquet output formats for logs delivered to S3.

### 1.6 Cloudflare Logs (Enterprise Logpush/Logpull)

**Format:** JSON (one JSON object per line / NDJSON), delivered via Logpush or retrieved via Logpull API.

**Key SEO-relevant fields (from 128+ total available):**
| Field | Type | Description |
|-------|------|-------------|
| `ClientIP` | string | Client IP address |
| `ClientRequestHost` | string | Requested host header |
| `ClientRequestMethod` | string | HTTP method |
| `ClientRequestPath` | string | URI path without query |
| `ClientRequestURI` | string | Full URI with query |
| `ClientRequestUserAgent` | string | User-Agent string |
| `ClientRequestReferer` | string | Referer header |
| `ClientRequestProtocol` | string | HTTP protocol version |
| `ClientRequestScheme` | string | URL scheme (http/https) |
| `ClientRequestBytes` | int | Request size in bytes |
| `ClientCountry` | string | ISO-3166 country code |
| `ClientDeviceType` | string | Device classification |
| `ClientASN` | int | Client AS number |
| `EdgeResponseStatus` | int | HTTP status returned to client |
| `EdgeResponseBytes` | int | Total response bytes |
| `EdgeResponseBodyBytes` | int | Response body size |
| `EdgeResponseContentType` | string | Content-Type header |
| `EdgeStartTimestamp` | int/string | Request receipt timestamp |
| `EdgeEndTimestamp` | int/string | Response completion timestamp |
| `EdgeTimeToFirstByteMs` | int | Time to first byte (ms) |
| `EdgeColoCode` | string | IATA code of edge data center |
| `OriginResponseStatus` | int | Status from origin server |
| `OriginResponseDurationMs` | int | Upstream response time |
| `OriginIP` | string | Origin server IP |
| `CacheCacheStatus` | string | Cache status (hit/miss/bypass) |
| `BotScore` | int | Bot score (below 30 = likely bot) |
| `BotScoreSrc` | string | Detection engine used |
| `BotTags` | array[string] | Bot traffic classification |
| `VerifiedBotCategory` | string | Verified bot category |
| `RayID` | string | Unique request identifier |
| `JA3Hash` | string | TLS fingerprint hash |
| `SecurityAction` | string | Security action taken |

**Timestamp formats:** Configurable as `unix`, `unixnano`, or `rfc3339`.

**Parsing approach:** JSON-per-line (NDJSON) -- parse with `JSON.parse()` per line. Field selection is configured when creating a Logpush job.

### 1.7 AWS ALB (Application Load Balancer) Access Logs

**Format:** Space-delimited, gzip-compressed files. Published every 5 minutes per LB node.

**All 33 fields in order:**
| # | Field | Description |
|---|-------|-------------|
| 1 | `type` | Request type: http, https, h2, grpcs, ws, wss |
| 2 | `time` | ISO 8601 timestamp of response generation |
| 3 | `elb` | Load balancer resource ID |
| 4 | `client:port` | Client IP and port |
| 5 | `target:port` | Target IP and port |
| 6 | `request_processing_time` | Seconds: LB receive to send to target |
| 7 | `target_processing_time` | Seconds: send to target to response headers |
| 8 | `response_processing_time` | Seconds: receive headers to start client response |
| 9 | `elb_status_code` | Status from load balancer |
| 10 | `target_status_code` | Status from target |
| 11 | `received_bytes` | Request size from client |
| 12 | `sent_bytes` | Response size to client |
| 13 | `request_line` | `"METHOD protocol://host:port/uri HTTP/version"` (quoted) |
| 14 | `user_agent` | User-Agent string (quoted, max 8KB) |
| 15 | `ssl_cipher` | TLS cipher or `-` |
| 16 | `ssl_protocol` | TLS protocol or `-` |
| 17 | `target_group_arn` | Target group ARN |
| 18 | `trace_id` | X-Amzn-Trace-Id header (quoted) |
| 19 | `domain_name` | SNI domain (quoted) |
| 20 | `chosen_cert_arn` | Certificate ARN (quoted) |
| 21 | `matched_rule_priority` | Rule priority |
| 22 | `request_creation_time` | ISO 8601 when LB received request |
| 23 | `actions_executed` | Actions taken (quoted, comma-separated) |
| 24 | `redirect_url` | Redirect target URL (quoted) |
| 25 | `error_reason` | Error reason code (quoted) |
| 26 | `target:port_list` | Target IPs (quoted, space-delimited) |
| 27 | `target_status_code_list` | Target status codes (quoted, space-delimited) |
| 28 | `classification` | Desync classification (quoted) |
| 29 | `classification_reason` | Classification reason code (quoted) |
| 30 | `conn_trace_id` | Unique connection ID |
| 31 | `transformed_host` | Host after rewrite (quoted) |
| 32 | `transformed_uri` | URI after rewrite (quoted) |
| 33 | `request_transform_status` | Rewrite status (quoted) |

**Parsing notes:** Fields that can contain spaces are enclosed in quotes. The `request_line` field contains a full URL with protocol, host, port, and path -- the URL path must be extracted.

### 1.8 AWS Classic ELB Access Logs

**Format:** Space-delimited, 16 fields:
```
timestamp elb client:port backend:port request_processing_time backend_processing_time response_processing_time elb_status_code backend_status_code received_bytes sent_bytes "request" "user_agent" ssl_cipher ssl_protocol
```

### 1.9 Custom Format Definitions (Regex-Based)

For any non-standard format, the tool accepts a custom regex pattern with named capture groups.

```typescript
interface LogFormatDefinition {
  name: string;
  pattern: RegExp;           // Regex with named capture groups
  fields: string[];          // Expected field names in order
  timestampFormat?: string;  // strftime-like pattern for parsing timestamps
  delimiter?: string;        // For simple delimited formats (tab, space)
  quoteChar?: string;        // Quote character for fields with spaces
  skipLines?: number;        // Header lines to skip
  commentPrefix?: string;    // e.g., '#' for W3C format headers
}
```

**Example custom regex:**
```regex
(?<ip>\S+) (?<ident>\S+) (?<user>\S+) \[(?<timestamp>[^\]]+)\] "(?<method>\S+) (?<path>\S+) (?<protocol>[^"]*)" (?<status>\d{3}) (?<bytes>\S+) "(?<referer>[^"]*)" "(?<useragent>[^"]*)" (?<response_time>\S+)
```

---

## 2. SEO-Specific Analysis Features

### 2.1 Bot Identification and Classification

#### Search Engine Bots

| Bot | User-Agent Token | Verification Domain |
|-----|-----------------|---------------------|
| Googlebot (Desktop) | `Googlebot/2.1` | `*.googlebot.com`, `*.google.com` |
| Googlebot (Mobile) | `Googlebot/2.1` (Mobile in UA) | `*.googlebot.com`, `*.google.com` |
| Googlebot-Image | `Googlebot-Image/1.0` | `*.googlebot.com` |
| Googlebot-Video | `Googlebot-Video/1.0` | `*.googlebot.com` |
| Googlebot-News | `Googlebot` (robots.txt: `Googlebot-News`) | `*.googlebot.com` |
| Google StoreBot | `Storebot-Google/1.0` | `*.googlebot.com` |
| Google-InspectionTool | `Google-InspectionTool/1.0` | `*.googlebot.com` |
| GoogleOther | `GoogleOther` | `*.googlebot.com` |
| Google-Extended | `Google-Extended` | `*.googlebot.com` |
| Bingbot | `bingbot/2.0` | `*.search.msn.com` |
| Bingbot (Mobile) | `bingbot/2.0` (Mobile in UA) | `*.search.msn.com` |
| YandexBot | `YandexBot/3.0` | `*.yandex.com`, `*.yandex.ru`, `*.yandex.net` |
| Baidu Spider | `Baiduspider/2.0` | `*.baidu.com`, `*.baidu.jp` |
| DuckDuckBot | `DuckDuckBot/1.1` | `*.duckduckgo.com` |
| Applebot | `Applebot/0.1` | `*.apple.com` |
| Sogou | `Sogou web spider` | `*.sogou.com` |
| Seznambot | `SeznamBot/4.0` | `*.seznam.cz` |
| Naver (Yeti) | `Yeti/1.1` | `*.naver.com` |

#### AI Crawler Bots

| Bot | Company | User-Agent String | Purpose |
|-----|---------|-------------------|---------|
| GPTBot | OpenAI | `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.3; +https://openai.com/gptbot)` | Training data |
| OAI-SearchBot | OpenAI | `Mozilla/5.0 ... compatible; OAI-SearchBot/1.3; +https://openai.com/searchbot` | ChatGPT search |
| ChatGPT-User | OpenAI | `ChatGPT-User` | Live browsing |
| ClaudeBot | Anthropic | `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)` | Training data |
| Claude-SearchBot | Anthropic | `Claude-SearchBot` | Search indexing |
| Claude-User | Anthropic | `Claude-User` | User-triggered |
| PerplexityBot | Perplexity | `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)` | Answer engine |
| Bytespider | ByteDance | `Bytespider` | TikTok/training |
| CCBot | Common Crawl | `CCBot/2.0` | Open web corpus |
| Meta-ExternalAgent | Meta | `meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)` | Meta AI |
| Amazonbot | Amazon | `Mozilla/5.0 ... compatible; Amazonbot/0.1; +https://developer.amazon.com/support/amazonbot` | Amazon AI |
| Applebot-Extended | Apple | `Applebot-Extended` | Apple AI features |
| DuckAssistBot | DuckDuckGo | `DuckAssistBot` | AI answers |
| Gemini-Deep-Research | Google | `Gemini-Deep-Research` | Gemini research |
| Diffbot | Diffbot | `Diffbot` | Data extraction |
| MistralAI-User | Mistral | `MistralAI-User` | Mistral AI |
| cohere-ai | Cohere | `cohere-ai` | Cohere training |

#### Bot Classification Categories

```typescript
type BotCategory =
  | 'search_engine'          // Primary indexing bots
  | 'search_media'           // Image, video, news bots
  | 'ai_training'            // Model training crawlers
  | 'ai_search'              // Real-time AI search
  | 'ai_user'                // Live user-triggered AI fetching
  | 'seo_tool'               // Ahrefs, Semrush, Moz, etc.
  | 'social_media'           // Facebook, Twitter, LinkedIn, Pinterest
  | 'feed_crawler'           // RSS/Atom feed fetchers
  | 'monitoring'             // Uptime/performance monitors
  | 'other_bot'              // Other identified bots
  | 'unknown_bot'            // Bot-like but unidentified
  | 'human';                 // Not a bot
```

### 2.2 Bot Verification

#### Forward-Confirmed Reverse DNS (FCrDNS)

The gold standard for bot verification:

1. Extract IP address from log entry
2. Perform reverse DNS (PTR) lookup: `IP -> hostname`
3. Validate hostname ends with approved domain (e.g., `*.googlebot.com`)
4. Perform forward DNS (A/AAAA) lookup: `hostname -> IP`
5. Confirm forward lookup IP matches original IP

```typescript
interface BotVerificationResult {
  ip: string;
  hostname: string | null;      // PTR result
  verified: boolean;            // FCrDNS passed
  verifiedBot: string | null;   // Identified bot name
  method: 'fcrdns' | 'ip_range' | 'user_agent_only';
  checkedAt: number;
  spoofDetected: boolean;       // UA claims bot but verification failed
}
```

#### Verification Domains

| Bot | Valid Reverse DNS Domains |
|-----|--------------------------|
| Googlebot | `*.googlebot.com`, `*.google.com`, `*.googleusercontent.com` |
| Bingbot | `*.search.msn.com` |
| YandexBot | `*.yandex.com`, `*.yandex.ru`, `*.yandex.net` |
| Applebot | `*.apple.com` |
| Baidu Spider | `*.baidu.com`, `*.baidu.jp` |

#### IP Range List Verification (Alternative)

Google publishes daily-updated JSON files:
- `https://developers.google.com/static/search/apis/ipranges/googlebot.json`
- `https://developers.google.com/static/search/apis/ipranges/user-triggered-fetchers.json`
- `https://developers.google.com/static/search/apis/ipranges/user-triggered-fetchers-google.json`

Bing does NOT publish IP ranges. Reverse DNS is the only reliable method.

### 2.3 Crawl Budget Analysis

#### Key Metrics
- **Pages crawled per day** by bot type
- **Crawl rate** (requests per second/minute/hour) by bot
- **Status code distribution per bot** (2xx, 3xx, 4xx, 5xx breakdown)
- **Average response time per bot** (and per URL/section)
- **Bytes downloaded per day** by bot
- **Crawl depth distribution** (how deep bots go into URL structure)
- **Desktop vs. mobile crawl split** (especially for Googlebot)
- **New URLs discovered** (first-seen timestamps)
- **Recrawl interval** per URL or URL group
- **Crawl frequency by site section** (directory-level analysis)

### 2.4 Crawl Waste Detection

| Waste Type | Description | Detection Method |
|------------|-------------|------------------|
| Faceted navigation | Bots crawling filter/sort parameter combos | Count URLs with params like `?color=`, `?sort=`, `?page=` |
| Pagination sprawl | Excessive crawling of paginated archives | Identify `/page/N` or `?page=N` patterns with high N |
| Session/tracking IDs | URLs with session tokens being crawled | Detect `?sid=`, `?session=`, `?utm_` parameters |
| Internal search results | Bots indexing search result pages | Identify `/search?q=` patterns |
| Calendar traps | Infinite calendar navigation | Detect date-patterned URLs extending far past/future |
| Soft 404s | Pages returning 200 but no useful content | Cross-reference with crawl data for thin/empty pages |
| Tag/category archives | Low-value taxonomy pages consuming budget | High-frequency crawl of `/tag/`, `/category/` paths |
| Redirect chains | Bots following chains of 301/302 redirects | Analyze redirect sequences in logs |
| Non-indexable pages | Pages with noindex, canonical elsewhere | Cross-reference log hits with crawl data indexability |
| Duplicate content | Same content under different URLs | Cross-reference with crawl data duplicate detection |
| Resource files | Excessive crawling of CSS/JS/images | Filter by file extension and content type |
| API endpoints | Bots hitting API URLs | Identify `/api/` or JSON-returning paths |

**Waste ratio metric:** `crawl_waste_ratio = bot_hits_on_non_indexable_pages / total_bot_hits`

### 2.5 URL-Level Analysis

- **Hit frequency**: Total and per-bot hit counts per URL
- **First seen / last seen timestamps**: When a URL was first and last crawled by each bot
- **Recrawl interval**: Average time between consecutive bot visits per URL
- **Response time trends**: Per-URL response time over time
- **Status code history**: Track status changes per URL over time (e.g., 200 -> 301 -> 404)
- **Orphan page detection**: URLs in logs that are NOT in the crawl data
- **Ghost pages**: URLs in crawl data that never appear in logs

### 2.6 Log-to-Crawl Data Merge

The most powerful feature that distinguishes enterprise SEO log analyzers from generic log analysis tools.

#### Merge Dimensions

| Crawl Data Field | Log Data Field | Insight |
|-----------------|----------------|---------|
| URL | Requested URL | Match crawl results to bot activity |
| Indexability status | Bot hits | Identify bots crawling non-indexable pages |
| Canonical URL | Bot hits on non-canonical | Find canonical waste |
| HTTP status (crawl) | HTTP status (bot) | Detect inconsistencies |
| Internal links count | Bot crawl frequency | Correlate link equity with crawl priority |
| Content quality | Recrawl interval | Verify high-value pages get frequent crawls |
| Page depth | Bot visit depth | Compare intended vs. actual crawl depth |
| Hreflang clusters | Bot coverage | Ensure all language versions are crawled |
| Sitemap inclusion | Bot discovery | Verify sitemap URLs are being found |

#### Three-Way Merge

1. **Crawl data**: Structure, content, technical issues
2. **Log data**: What search engine bots actually visited
3. **Search Console / Analytics data**: Impressions, clicks, traffic

#### Page Segmentation

```typescript
interface MergedPageData {
  url: string;

  // From logs
  log_totalHits: number;
  log_botHits: Record<string, number>;
  log_firstSeen: Date;
  log_lastSeen: Date;
  log_avgResponseTime: number;
  log_statusCodes: Record<number, number>;
  log_avgRecrawlInterval: number;

  // From crawl
  crawl_httpStatus: number;
  crawl_indexable: boolean;
  crawl_canonicalUrl: string;
  crawl_internalLinksIn: number;
  crawl_internalLinksOut: number;
  crawl_depth: number;
  crawl_wordCount: number;
  crawl_title: string;
  crawl_inSitemap: boolean;
  crawl_hasNoindex: boolean;
  crawl_redirectTarget: string | null;

  // From GSC (optional)
  gsc_impressions: number;
  gsc_clicks: number;
  gsc_avgPosition: number;

  // Computed
  segment: PageSegment;
}

type PageSegment =
  | 'healthy'              // Crawled + indexed + traffic
  | 'indexed_no_traffic'   // Crawled + indexed but no traffic
  | 'crawled_not_indexed'  // Crawled but not indexed
  | 'uncrawled_indexable'  // Indexable but not visited by bots
  | 'orphan_crawled'       // Not in site structure but visited by bots
  | 'crawl_waste'          // Non-indexable but heavily crawled
  | 'redirect_waste'       // Redirects consuming crawl budget
  | 'error_pages';         // 4xx/5xx consistently served to bots
```

### 2.7 Bot Behavior Patterns

- **Time-of-day crawl patterns**: Heatmap (hour x day-of-week)
- **Day-of-week patterns**: Weekend vs. weekday differences
- **Crawl bursts**: Detect sudden spikes in bot activity
- **Crawl slowdowns**: Detect drops (may indicate server issues or penalties)
- **Path traversal patterns**: Analyze sequential bot hits
- **User agent switching**: Track mobile/desktop UA changes
- **AI bot emergence**: Track new AI bots over time and their growth

---

## 3. Technical Implementation

### 3.1 Streaming Parsing for Large Files (100GB+)

#### Architecture

```
ReadStream (256KB chunks)
  -> GunzipTransform (if compressed)
  -> ReadlineInterface (line splitting)
  -> ParseTransform (regex/format parsing)
  -> BotIdentifyTransform (UA matching)
  -> AggregateTransform (metrics accumulation)
  -> OutputWriter (SQLite/JSON/CSV)
```

#### Performance Targets
- Memory: ~40-60MB regardless of file size
- Speed: ~500K-1M lines/second with regex parsing
- CPU: 15-30% utilization with streaming

#### Configuration
```typescript
const STREAM_OPTIONS = {
  highWaterMark: 256 * 1024,  // 256KB chunks (benchmark-optimal)
  encoding: 'utf-8'
};
```

#### Compressed File Support
- `.gz`: Pipe through `zlib.createGunzip()`
- `.bz2`: Use `unbzip2-stream`
- `.zst`: Use `zstd-codec` or shell to `zstd -d`

#### Multi-File Processing
- Process files sequentially to avoid memory pressure
- Track per-file progress for UI feedback
- Use Worker threads for CPU-intensive regex parsing if needed

### 3.2 IP-to-Bot Mapping

#### Two-Tier Strategy

**Tier 1: User-Agent string matching (fast, first pass)**
```typescript
interface BotSignature {
  name: string;
  category: BotCategory;
  patterns: RegExp[];
  verificationDomain?: string;
  ipRangeUrl?: string;
}
```

**Tier 2: IP-based verification (accurate, second pass)**
- Load IP range lists from official sources (Google JSON files, etc.)
- Store in CIDR-lookup data structure for O(1) IP matching
- For bots without published IP ranges, use reverse DNS

### 3.3 Reverse DNS Caching

```typescript
interface DnsCache {
  get(ip: string): VerificationResult | undefined;
  set(ip: string, result: VerificationResult, ttlMs: number): void;
}
```

**Implementation recommendations:**
- In-memory LRU cache for 10K-100K entries
- Default TTL: 3600 seconds (1 hour)
- Cache both positive and negative results
- Limit concurrent DNS lookups (50 max)
- Set DNS timeout to 5 seconds
- Process unique IPs first, then map results to all entries
- Consider local DNS caching resolver (dnsmasq) for high-volume processing

### 3.4 Incremental Processing

```typescript
interface ProcessingState {
  filePath: string;
  lastByteOffset: number;     // Resume position
  lastLineNumber: number;
  lastTimestamp: string;
  checksum: string;            // MD5 of first 1KB (detect rotation)
}
```

**Log rotation handling:**
- Detect rotation by checking first-N-bytes checksum
- If checksum changes -> file was rotated, process from beginning
- If checksum matches -> seek to `lastByteOffset` and continue
- Support: `.log.1`, `.log.gz`, dated suffixes

### 3.5 Memory-Efficient Processing

**Bounded data structures:**
- Top-N URLs by hit count: Min-heap of size N
- Bot counters: Fixed number of known bots
- Time-series buckets: Fixed hourly/daily slots
- Status code counters: 5 categories

**Approximate data structures for extreme scale:**
- HyperLogLog for unique URL counts
- Count-Min Sketch for frequency estimation
- Bloom filter for "seen URL" deduplication

**Memory budget for 100GB processing:**
| Component | Memory |
|-----------|--------|
| Stream buffer | ~2MB |
| Bot counters | ~100KB |
| URL top-N heap | ~10MB |
| DNS cache | ~50MB |
| SQLite connection | ~2MB |
| **Total** | **~65MB** |

---

## 4. Output Formats and Reports

### 4.1 Key Metrics and KPIs

| KPI | Formula | Target |
|-----|---------|--------|
| Crawl Efficiency Ratio | `unique_pages_crawled / total_bot_hits` | > 0.7 |
| Crawl Waste Ratio | `hits_on_non_indexable / total_bot_hits` | < 0.15 |
| Index Coverage Rate | `pages_crawled_and_indexed / total_indexable_pages` | > 0.9 |
| Crawl Freshness | `avg_recrawl_interval_for_key_pages` | < 7 days |
| Error Rate | `4xx_5xx_hits / total_bot_hits` | < 0.05 |
| Response Time P95 | `95th_percentile_response_time` | < 500ms |
| Mobile Crawl Share | `mobile_bot_hits / total_bot_hits` | > 50% |
| AI Bot Share | `ai_bot_hits / total_bot_hits` | Trending |
| Orphan Page Count | `urls_in_logs_not_in_crawl` | Minimize |
| Crawl Depth Coverage | `pages_at_depth_3+_crawled / total_at_depth_3+` | > 0.8 |

### 4.2 Dashboard Views

#### Overview Dashboard
- Total bot hits (big numbers with trend arrows)
- Crawl trend line chart (daily, one line per bot)
- Status code distribution donut chart
- Top 10 most crawled URLs
- Crawl waste percentage gauge

#### Bot Activity Dashboard
- Bot selector (tabs/dropdown)
- Crawl volume time series per bot
- Heatmap: hour-of-day x day-of-week showing crawl intensity
- Desktop vs. mobile split
- Top crawled directories (treemap)
- Response time distribution (histogram)

#### Crawl Budget Dashboard
- Efficiency funnel: Total hits -> Unique URLs -> Indexable -> Indexed -> Traffic
- Waste breakdown pie chart (by waste type)
- Section-level crawl allocation vs. traffic contribution
- Recrawl interval distribution
- New URL discovery rate

#### Technical Health Dashboard
- Error rate trend
- Redirect chain analysis (Sankey diagram)
- Response time P50/P95/P99 over time
- Slow pages table
- Status code change log

#### AI Bots Dashboard
- AI bot volume by bot over time
- Most accessed content sections
- AI vs. search engine bot behavior comparison
- robots.txt compliance analysis for AI bots

### 4.3 Output Formats

- **HTML report**: Self-contained single file (like GoAccess)
- **JSON**: Structured data for programmatic consumption
- **CSV**: Per-table exports for spreadsheet analysis
- **SQLite**: Full queryable dataset
- **Real-time stream**: WebSocket or SSE for live dashboards

---

## 5. Competitive Landscape

| Tool | Strength | Limitation | Price |
|------|----------|------------|-------|
| **Botify** | Three-way merge, real-time, enterprise scale | $2K-$10K/month | Enterprise |
| **Oncrawl** | Data science approach, GA/GSC layering | Complexity | $249+/month |
| **JetOctopus** | Speed, 40+ bot detection, AI bot analyzer | Newer | $171+/month |
| **Screaming Frog Log Analyzer** | Desktop, auto bot verification, affordable | Not cloud, 1K free limit | $139/year |
| **Seolyzer** | Real-time dashboards, free tier | Limited depth | Free/Paid |
| **GoAccess** | Open source, fast, terminal+HTML | No SEO features | Free |
| **advertools** | Python library, flexible, free | Requires coding | Free |

### Differentiation Opportunities

1. Open-source with SEO focus (no existing tool combines streaming + SEO)
2. AI bot tracking as first-class feature
3. TypeScript/Node.js (integrates with web-based tools)
4. Log-to-crawl merge built-in
5. Custom format flexibility via regex
6. Memory-efficient streaming for 100GB+ files
7. Incremental processing with state persistence
8. Pluggable output formats

---

## 6. Architecture Summary

```
Input Layer:
  - Multi-format parser (CLF, Combined, W3C, CloudFront, ALB, Cloudflare, custom)
  - Streaming line reader (256KB chunks, readline interface)
  - Compressed file support (.gz, .bz2, .zst)
  - Incremental processing with byte-offset tracking

Processing Layer:
  - Bot identification (user-agent regex matching)
  - Bot verification (FCrDNS with LRU cache, IP range list matching)
  - URL normalization and classification
  - Time-series bucketing (hourly/daily aggregation)
  - Status code and response time aggregation

Storage Layer:
  - In-memory aggregation for bounded metrics (top-N, counters)
  - SQLite for per-URL detailed data (spillable)
  - Processing state persistence (resume support)

Merge Layer:
  - Cross-reference with crawl data (URL-keyed join)
  - Cross-reference with GSC data (optional)
  - Page segmentation (healthy, waste, orphan, etc.)

Output Layer:
  - JSON report
  - HTML dashboard (self-contained)
  - CSV tables
  - SQLite database
  - Real-time stream (WebSocket or SSE)
```

---

## References

- [Apache mod_log_config documentation](https://httpd.apache.org/docs/current/mod/mod_log_config.html)
- [Nginx ngx_http_log_module](https://nginx.org/en/docs/http/ngx_http_log_module.html)
- [IIS W3C Extended Log Format](https://learn.microsoft.com/en-us/previous-versions/iis/6.0-sdk/ms525807(v=vs.90))
- [AWS CloudFront standard logs reference](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/standard-logs-reference.html)
- [AWS ALB access logs](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-access-logs.html)
- [Cloudflare Logs HTTP request fields](https://developers.cloudflare.com/logs/logpush/logpush-job/datasets/zone/http_requests/)
- [Google common crawlers](https://developers.google.com/crawling/docs/crawlers-fetchers/google-common-crawlers)
- [Verify Google requests](https://developers.google.com/crawling/docs/crawlers-fetchers/verify-google-requests)
- [Verify Bingbot](https://www.bing.com/webmasters/help/how-to-verify-bingbot-3905dc26)
- [AI crawler user agents list (Search Engine Journal)](https://www.searchenginejournal.com/ai-crawler-user-agents-list/558130/)
- [ai.robots.txt (GitHub)](https://github.com/ai-robots-txt/ai.robots.txt)
- [Screaming Frog Log File Analyser](https://www.screamingfrog.co.uk/log-file-analyser/)
- [JetOctopus log analyzer](https://jetoctopus.com/log-analyzer/)
- [Botify log analysis](https://www.botify.com/insight/log-file-analysis)
- [Oncrawl SEO log analyzer](https://www.oncrawl.com/products/seo-log-analyzer/)
- [GoAccess web log analyzer](https://goaccess.io/)
- [advertools log file analysis](https://advertools.readthedocs.io/en/master/advertools.logs.html)
