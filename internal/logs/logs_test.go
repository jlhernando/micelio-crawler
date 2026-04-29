package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// --- Bot Detection ---

func TestIdentifyBotKnown(t *testing.T) {
	cases := []struct {
		ua       string
		name     string
		category string
		mobile   bool
	}{
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "Googlebot", "search_engine", false},
		{"Mozilla/5.0 (Linux; Android 6.0.1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "Googlebot", "search_engine", true},
		{"Googlebot-Image/1.0", "Googlebot-Image", "search_media", false},
		{"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", "Bingbot", "search_engine", false},
		{"Mozilla/5.0 (compatible; GPTBot/1.0; +https://openai.com/gptbot)", "GPTBot", "ai_training", false},
		{"ClaudeBot/1.0", "ClaudeBot", "ai_training", false},
		{"PerplexityBot/1.0", "PerplexityBot", "ai_search", false},
		{"AhrefsBot/7.0; +http://ahrefs.com/robot/", "AhrefsBot", "seo_tool", false},
		{"facebookexternalhit/1.1", "Facebook", "social_media", false},
		{"UptimeRobot/2.0", "UptimeRobot", "monitoring", false},
		{"Feedly/1.0", "Feedly", "feed_crawler", false},
	}
	for _, c := range cases {
		bot := IdentifyBot(c.ua)
		if bot == nil {
			t.Errorf("IdentifyBot(%q) = nil, want %s", c.ua, c.name)
			continue
		}
		if bot.Name != c.name {
			t.Errorf("IdentifyBot(%q).Name = %s, want %s", c.ua, bot.Name, c.name)
		}
		if bot.Category != c.category {
			t.Errorf("IdentifyBot(%q).Category = %s, want %s", c.ua, bot.Category, c.category)
		}
		if bot.Mobile != c.mobile {
			t.Errorf("IdentifyBot(%q).Mobile = %v, want %v", c.ua, bot.Mobile, c.mobile)
		}
	}
}

func TestIdentifyBotGeneric(t *testing.T) {
	bot := IdentifyBot("UnknownCrawler bot/1.0")
	if bot == nil || bot.Name != "Other Bot" {
		t.Errorf("generic bot detection failed, got %v", bot)
	}
	bot = IdentifyBot("some spider thing")
	if bot == nil || bot.Name != "Other Bot" {
		t.Errorf("generic spider detection failed, got %v", bot)
	}
}

func TestIdentifyBotHuman(t *testing.T) {
	bot := IdentifyBot("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	if bot != nil {
		t.Errorf("human UA classified as bot: %v", bot)
	}
}

// --- Format Detection ---

func TestDetectFormat(t *testing.T) {
	combined := []string{
		`66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET / HTTP/1.1" 200 12345 "-" "Googlebot/2.1"`,
	}
	if f := DetectFormat(combined); f != FormatApacheCombined {
		t.Errorf("DetectFormat(combined) = %s, want apache_combined", f)
	}

	clf := []string{
		`66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET / HTTP/1.1" 200 12345`,
	}
	if f := DetectFormat(clf); f != FormatApacheCLF {
		t.Errorf("DetectFormat(clf) = %s, want apache_clf", f)
	}

	if f := DetectFormat([]string{"garbage line"}); f != FormatUnknown {
		t.Errorf("DetectFormat(garbage) = %s, want unknown", f)
	}

	// Empty/comment lines should be skipped
	if f := DetectFormat([]string{"", "# comment", combined[0]}); f != FormatApacheCombined {
		t.Errorf("DetectFormat with comments = %s, want apache_combined", f)
	}
}

// --- Line Parsing ---

func TestParseCombinedLine(t *testing.T) {
	line := `66.249.66.1 - - [01/Apr/2026:08:15:30 +0000] "GET /about HTTP/1.1" 200 8432 "https://example.com" "Googlebot/2.1"`
	e, err := ParseLine(line, FormatApacheCombined)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.IP != "66.249.66.1" {
		t.Errorf("IP = %s, want 66.249.66.1", e.IP)
	}
	if e.Method != "GET" {
		t.Errorf("Method = %s, want GET", e.Method)
	}
	if e.Path != "/about" {
		t.Errorf("Path = %s, want /about", e.Path)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
	if e.Bytes != 8432 {
		t.Errorf("Bytes = %d, want 8432", e.Bytes)
	}
	if e.Referer != "https://example.com" {
		t.Errorf("Referer = %s, want https://example.com", e.Referer)
	}
	if e.UserAgent != "Googlebot/2.1" {
		t.Errorf("UserAgent = %s, want Googlebot/2.1", e.UserAgent)
	}
}

func TestParseCLFLine(t *testing.T) {
	line := `192.168.1.1 - frank [10/Oct/2024:13:55:36 -0700] "GET /index.html HTTP/1.0" 200 2326`
	e, err := ParseLine(line, FormatApacheCLF)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.IP != "192.168.1.1" || e.Path != "/index.html" || e.Status != 200 || e.Bytes != 2326 {
		t.Errorf("CLF parse incorrect: %+v", e)
	}
}

func TestParseDashBytes(t *testing.T) {
	line := `192.168.1.1 - - [01/Apr/2026:08:00:00 +0000] "GET / HTTP/1.1" 301 - "-" "Bot/1.0"`
	e, err := ParseLine(line, FormatApacheCombined)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if e.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0 for '-'", e.Bytes)
	}
}

func TestParseLineInvalid(t *testing.T) {
	_, err := ParseLine("not a log line", FormatApacheCombined)
	if err == nil {
		t.Error("expected error for invalid line")
	}
}

// --- CloudFront ---

func TestDetectFormatCloudFront(t *testing.T) {
	lines := []string{
		"#Version: 1.0",
		"#Fields: date time x-edge-location sc-bytes c-ip cs-method cs(Host) cs-uri-stem sc-status cs(Referer) cs(User-Agent) cs-uri-query cs-cookie x-edge-result-type",
		"2026-04-01\t08:15:30\tSEA19-C2\t12345\t66.249.66.1\tGET\texample.com\t/about\t200\t-\tGooglebot/2.1\t-\t-\tHit",
	}
	if f := DetectFormat(lines); f != FormatW3C {
		t.Errorf("DetectFormat(cloudfront) = %s, want w3c", f)
	}
}

func TestParseCloudFrontLine(t *testing.T) {
	// Positional path: no header captured → fall back to canonical column order.
	ResetHeaderFields()
	line := "2026-04-01\t08:15:30\tSEA19-C2\t12345\t66.249.66.1\tGET\texample.com\t/about\t200\t-\tGooglebot/2.1\t-\t-\tHit"
	e, err := ParseLine(line, FormatCloudFront)
	if err != nil {
		t.Fatalf("ParseLine cloudfront error: %v", err)
	}
	if e.IP != "66.249.66.1" {
		t.Errorf("IP = %s, want 66.249.66.1", e.IP)
	}
	if e.Path != "/about" {
		t.Errorf("Path = %s, want /about", e.Path)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
	if e.Bytes != 12345 {
		t.Errorf("Bytes = %d, want 12345", e.Bytes)
	}
	if e.UserAgent != "Googlebot/2.1" {
		t.Errorf("UserAgent = %s, want Googlebot/2.1", e.UserAgent)
	}
}

// CloudFront CSV-export style: first line is a snake_case TSV header, subsequent
// lines are data. Both detection and parsing should work via field names.
func TestDetectFormatCloudFrontCSVHeader(t *testing.T) {
	ResetHeaderFields()
	lines := []string{
		"date\ttime\tlocation\tbytes\trequest_ip\tmethod\thost\turi\tstatus\treferrer\tuser_agent\tquery_string\tcookie\tresult_type",
		"2026-03-09\t02:59:47\tIAD89-P4\t63696\t66.249.64.100\tGET\texample.com\t/osobowe/foo.html\t404\t-\tGooglebot/2.1\t-\t-\tError",
	}
	if f := DetectFormat(lines); f != FormatCloudFront {
		t.Errorf("DetectFormat(cloudfront csv header) = %s, want cloudfront", f)
	}
	// Detection side-effect: header fields captured
	if len(defaultParseState.Fields) < 10 {
		t.Errorf("SetTSVFields not invoked: defaultParseState.Fields = %v", defaultParseState.Fields)
	}
}

func TestParseCloudFrontCSVExport(t *testing.T) {
	ResetHeaderFields()
	SetTSVFields([]string{
		"date", "time", "location", "bytes", "request_ip", "method", "host",
		"uri", "status", "referrer", "user_agent", "query_string",
	})
	line := "2026-03-09\t02:59:47\tIAD89-P4\t63696\t66.249.64.100\tGET\texample.com\t/osobowe/oferta/kia-rio.html\t404\t-\tMozilla/5.0%20(compatible;%20Googlebot/2.1;%20+http://www.google.com/bot.html)\tsearch%5Bdist%5D=50"
	e, err := ParseLine(line, FormatCloudFront)
	if err != nil {
		t.Fatalf("ParseLine cloudfront csv error: %v", err)
	}
	if e.IP != "66.249.64.100" {
		t.Errorf("IP = %q, want 66.249.64.100", e.IP)
	}
	if e.Method != "GET" {
		t.Errorf("Method = %q, want GET", e.Method)
	}
	if e.Path != "/osobowe/oferta/kia-rio.html?search%5Bdist%5D=50" {
		t.Errorf("Path = %q", e.Path)
	}
	if e.Status != 404 {
		t.Errorf("Status = %d, want 404", e.Status)
	}
	if e.Bytes != 63696 {
		t.Errorf("Bytes = %d, want 63696", e.Bytes)
	}
	if !strings.Contains(e.UserAgent, "Googlebot") {
		t.Errorf("UserAgent = %q, want contains Googlebot", e.UserAgent)
	}
	if !strings.Contains(e.UserAgent, " ") {
		t.Errorf("UserAgent not URL-decoded: %q", e.UserAgent)
	}
	// Confirm bot identification works downstream
	if bot := IdentifyBot(e.UserAgent); bot == nil || bot.Name != "Googlebot" {
		t.Errorf("IdentifyBot(%q) = %v, want Googlebot", e.UserAgent, bot)
	}
}

// CloudFront export as real CSV (commas + quoted fields) — matches CloudFront
// exports from BigQuery / Athena / Redshift UNLOAD.
func TestDetectAndParseCloudFrontCSV(t *testing.T) {
	ResetHeaderFields()
	lines := []string{
		`"date","time","location","bytes","request_ip","method","host","uri","status","referrer","user_agent","query_string","cookie","result_type"`,
		`"2026-03-09","02:59:47","IAD89-P4","63696","66.249.64.100","GET","example.com","/foo.html","404","-","Mozilla/5.0%20(compatible;%20Googlebot/2.1;%20+http://www.google.com/bot.html)","-","-","Error"`,
	}
	if f := DetectFormat(lines); f != FormatCloudFront {
		t.Fatalf("DetectFormat(cloudfront csv) = %s, want cloudfront", f)
	}
	if len(defaultParseState.Fields) < 10 {
		t.Errorf("CSV fields not captured: %v", defaultParseState.Fields)
	}
	e, err := ParseLine(lines[1], FormatCloudFront)
	if err != nil {
		t.Fatalf("ParseLine cloudfront csv error: %v", err)
	}
	if e.IP != "66.249.64.100" {
		t.Errorf("IP = %q, want 66.249.64.100", e.IP)
	}
	if e.Status != 404 {
		t.Errorf("Status = %d, want 404", e.Status)
	}
	if e.Bytes != 63696 {
		t.Errorf("Bytes = %d, want 63696", e.Bytes)
	}
	if !strings.Contains(e.UserAgent, "Googlebot") {
		t.Errorf("UserAgent = %q, want contains Googlebot", e.UserAgent)
	}
}

// BOM-prefixed header (common from Windows/Excel-touched CSV exports).
func TestDetectCSVHeaderWithBOM(t *testing.T) {
	ResetHeaderFields()
	const bom = "\ufeff"
	lines := []string{
		bom + `"date","time","location","bytes","request_ip","method","host","uri","status","referrer","user_agent"`,
		`"2026-03-09","02:59:47","IAD89-P4","100","66.249.64.100","GET","example.com","/x","200","-","Googlebot"`,
	}
	if f := DetectFormat(lines); f != FormatCloudFront {
		t.Errorf("DetectFormat(BOM csv) = %s, want cloudfront", f)
	}
	if len(defaultParseState.Fields) == 0 || defaultParseState.Fields[0] != "date" {
		t.Errorf("BOM not stripped from first field: %q", defaultParseState.Fields)
	}
}

func TestDetectTSVHeaderRejectsDataRow(t *testing.T) {
	ResetHeaderFields()
	// A real data row with IPs, timestamps, 404s — should NOT be misdetected as a header.
	line := "2026-03-09\t02:59:47\tIAD89-P4\t63696\t66.249.64.100\tGET\texample.com\t/foo\t404\t-\tGooglebot/2.1\t-\t-\tError"
	if fields := detectTSVHeader(line); fields != nil {
		t.Errorf("detectTSVHeader misclassified data row as header: %v", fields)
	}
}

// --- Cloudflare JSON ---

func TestDetectFormatCloudflare(t *testing.T) {
	lines := []string{
		`{"ClientIP":"66.249.66.1","ClientRequestMethod":"GET","ClientRequestURI":"/about","EdgeResponseStatus":200,"EdgeResponseBytes":8000,"ClientRequestUserAgent":"Googlebot/2.1","EdgeStartTimestamp":1743494130000000000}`,
	}
	if f := DetectFormat(lines); f != FormatCloudflare {
		t.Errorf("DetectFormat(cloudflare) = %s, want cloudflare", f)
	}
}

func TestParseCloudflare(t *testing.T) {
	line := `{"ClientIP":"66.249.66.1","ClientRequestMethod":"GET","ClientRequestURI":"/about","EdgeResponseStatus":200,"EdgeResponseBytes":8000,"ClientRequestUserAgent":"Googlebot/2.1","ClientRequestReferer":"https://example.com","EdgeStartTimestamp":1743494130000000000,"EdgeEndTimestamp":1743494130050000000}`
	e, err := ParseLine(line, FormatCloudflare)
	if err != nil {
		t.Fatalf("ParseLine cloudflare error: %v", err)
	}
	if e.IP != "66.249.66.1" {
		t.Errorf("IP = %s, want 66.249.66.1", e.IP)
	}
	if e.Path != "/about" {
		t.Errorf("Path = %s, want /about", e.Path)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
	if e.Bytes != 8000 {
		t.Errorf("Bytes = %d, want 8000", e.Bytes)
	}
	if e.UserAgent != "Googlebot/2.1" {
		t.Errorf("UserAgent = %s, want Googlebot/2.1", e.UserAgent)
	}
	if e.Referer != "https://example.com" {
		t.Errorf("Referer = %s, want https://example.com", e.Referer)
	}
	if e.ResponseTime < 0.04 || e.ResponseTime > 0.06 {
		t.Errorf("ResponseTime = %f, want ~0.05", e.ResponseTime)
	}
}

// --- ALB ---

func TestDetectFormatALB(t *testing.T) {
	lines := []string{
		`https 2026-04-01T08:15:30.000000Z app/my-lb/abc123 66.249.66.1:443 10.0.0.1:80 0.001 0.010 0.000 200 200 100 12345 "GET https://example.com:443/about HTTP/2.0" "Googlebot/2.1" ECDHE-RSA-AES128 TLSv1.2`,
	}
	if f := DetectFormat(lines); f != FormatALB {
		t.Errorf("DetectFormat(alb) = %s, want alb", f)
	}
}

func TestParseALBLine(t *testing.T) {
	line := `https 2026-04-01T08:15:30.000000Z app/my-lb/abc123 66.249.66.1:443 10.0.0.1:80 0.001 0.010 0.000 200 200 100 12345 "GET https://example.com:443/about HTTP/2.0" "Googlebot/2.1" ECDHE-RSA-AES128 TLSv1.2`
	e, err := ParseLine(line, FormatALB)
	if err != nil {
		t.Fatalf("ParseLine ALB error: %v", err)
	}
	if e.IP != "66.249.66.1" {
		t.Errorf("IP = %s, want 66.249.66.1", e.IP)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
	if e.UserAgent != "Googlebot/2.1" {
		t.Errorf("UserAgent = %s, want Googlebot/2.1", e.UserAgent)
	}
}

// --- W3C ---

func TestParseW3CLine(t *testing.T) {
	SetW3CFields("#Fields: date time c-ip cs-method cs-uri-stem sc-status sc-bytes cs(User-Agent) cs(Referer)")
	line := "2026-04-01 08:15:30 66.249.66.1 GET /about 200 8432 Googlebot/2.1 -"
	e, err := ParseLine(line, FormatW3C)
	if err != nil {
		t.Fatalf("ParseLine W3C error: %v", err)
	}
	if e.IP != "66.249.66.1" {
		t.Errorf("IP = %s, want 66.249.66.1", e.IP)
	}
	if e.Path != "/about" {
		t.Errorf("Path = %s, want /about", e.Path)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
}

// --- Custom Format ---

func TestCompileCustomFormat(t *testing.T) {
	// Valid format
	pattern := `(?P<ip>\S+) \[(?P<timestamp>[^\]]+)\] "(?P<method>\S+) (?P<path>\S+)" (?P<status>\d+) (?P<bytes>\d+) "(?P<useragent>[^"]*)"$`
	def, err := CompileCustomFormat(pattern)
	if err != nil {
		t.Fatalf("CompileCustomFormat error: %v", err)
	}

	line := `66.249.66.1 [01/Apr/2026:08:15:30 +0000] "GET /about" 200 8432 "Googlebot/2.1"`
	e, err := ParseLineCustom(line, def)
	if err != nil {
		t.Fatalf("ParseLineCustom error: %v", err)
	}
	if e.IP != "66.249.66.1" || e.Path != "/about" || e.Status != 200 || e.UserAgent != "Googlebot/2.1" {
		t.Errorf("custom parse incorrect: %+v", e)
	}

	// Missing required group
	_, err = CompileCustomFormat(`(?P<ip>\S+) (?P<timestamp>\S+)`)
	if err == nil {
		t.Error("expected error for missing required groups")
	}
}

// --- Verification ---

func TestVerificationDomains(t *testing.T) {
	// Verify that verification domains exist for key bots
	for _, bot := range []string{"Googlebot", "Bingbot", "YandexBot", "Applebot"} {
		if _, ok := verificationDomains[bot]; !ok {
			t.Errorf("missing verification domain for %s", bot)
		}
	}
}

func TestBotVerifierCache(t *testing.T) {
	v := NewBotVerifier()
	// Verify a bot that has no verification domain (should return ua_only)
	result := v.VerifyBotIP("1.2.3.4", "AhrefsBot")
	if result.Method != "ua_only" {
		t.Errorf("expected ua_only for unverifiable bot, got %s", result.Method)
	}
	if result.SpoofDetected {
		t.Error("should not flag spoof for bots without verification domains")
	}
	// Should be cached
	result2 := v.VerifyBotIP("1.2.3.4", "AhrefsBot")
	if result2.Method != "ua_only" {
		t.Errorf("cached result should be ua_only, got %s", result2.Method)
	}
}

// --- Log-to-Crawl Merge ---

func TestMergeLogWithCrawl(t *testing.T) {
	logStats := &LogStats{
		TopURLs: []URLStat{
			{Path: "/", Hits: 100, BotHits: 80, HumanHits: 20, TopBot: "Googlebot", Status: 200},
			{Path: "/about", Hits: 50, BotHits: 30, HumanHits: 20, TopBot: "Bingbot", Status: 200},
			{Path: "/orphan", Hits: 10, BotHits: 8, HumanHits: 2, TopBot: "Googlebot", Status: 200},
			{Path: "/noindex-page", Hits: 40, BotHits: 35, HumanHits: 5, TopBot: "GPTBot", Status: 200},
		},
	}
	crawlPages := []CrawlPageInfo{
		{URL: "https://example.com/", StatusCode: 200, Indexable: true, Depth: 0, Inlinks: 10, WordCount: 500, Title: "Home"},
		{URL: "https://example.com/about", StatusCode: 200, Indexable: true, Depth: 1, Inlinks: 5, WordCount: 300, Title: "About"},
		{URL: "https://example.com/noindex-page", StatusCode: 200, Indexable: false, HasNoindex: true, Depth: 2, WordCount: 100, Title: "Hidden"},
		{URL: "https://example.com/ghost-page", StatusCode: 200, Indexable: true, Depth: 3, WordCount: 200, Title: "Ghost"},
	}

	result := MergeLogWithCrawl(logStats, crawlPages)

	if result.TotalPages == 0 {
		t.Fatal("TotalPages should be > 0")
	}
	if result.Segments[SegHealthy] != 2 {
		t.Errorf("healthy = %d, want 2", result.Segments[SegHealthy])
	}
	if result.Segments[SegCrawlWaste] != 1 {
		t.Errorf("crawl_waste = %d, want 1", result.Segments[SegCrawlWaste])
	}
	if result.Segments[SegOrphanCrawled] != 1 {
		t.Errorf("orphan = %d, want 1", result.Segments[SegOrphanCrawled])
	}
	if result.Segments[SegUncrawled] != 1 {
		t.Errorf("uncrawled/ghost = %d, want 1", result.Segments[SegUncrawled])
	}
	if len(result.OrphanPages) != 1 {
		t.Errorf("OrphanPages = %d, want 1", len(result.OrphanPages))
	}
	if len(result.GhostPages) != 1 {
		t.Errorf("GhostPages = %d, want 1", len(result.GhostPages))
	}
}

// findCrawlPage must match log URLs with query strings against canonical
// crawl pages (without query strings). This is the otomoto-style faceted-nav
// pattern that was inflating ghost counts.
func TestMergeMatchesQueryStringedURLs(t *testing.T) {
	logURLs := []URLStat{
		{Path: "/listings/cars?filter=red&page=2", Hits: 50, BotHits: 50, TopBot: "Googlebot", Status: 200},
		{Path: "/listings/cars?utm_source=newsletter", Hits: 10, BotHits: 10, TopBot: "Googlebot", Status: 200},
		{Path: "/news/?fbclid=abc", Hits: 5, BotHits: 5, TopBot: "Googlebot", Status: 200},
	}
	crawlPages := []CrawlPageInfo{
		{URL: "https://example.com/listings/cars", StatusCode: 200, Indexable: true},
		{URL: "https://example.com/news/", StatusCode: 200, Indexable: true},
	}
	res, err := MergeLogWithCrawlStream(func(fn func(URLStat) error) error {
		for _, u := range logURLs {
			if err := fn(u); err != nil {
				return err
			}
		}
		return nil
	}, crawlPages)
	if err != nil {
		t.Fatalf("merge error: %v", err)
	}
	// All three log URLs should match crawl pages (none should be orphans).
	if res.Segments[SegOrphanCrawled] != 0 {
		t.Errorf("orphan = %d, want 0 (query-stripped matches should resolve)", res.Segments[SegOrphanCrawled])
	}
	// Both crawl pages were matched, so no ghosts.
	if res.Segments[SegUncrawled] != 0 {
		t.Errorf("ghost = %d, want 0", res.Segments[SegUncrawled])
	}
	if res.Segments[SegHealthy] != 3 {
		t.Errorf("healthy = %d, want 3", res.Segments[SegHealthy])
	}
}

// MergeLogWithCrawlStream consumes URLs via callback and produces the same
// segmentation + counts as the legacy in-memory merge.
func TestMergeStreamMatchesLegacy(t *testing.T) {
	urls := []URLStat{
		{Path: "/", Hits: 100, BotHits: 80, HumanHits: 20, TopBot: "Googlebot", Status: 200},
		{Path: "/about", Hits: 50, BotHits: 30, HumanHits: 20, TopBot: "Bingbot", Status: 200},
		{Path: "/orphan", Hits: 10, BotHits: 8, HumanHits: 2, TopBot: "Googlebot", Status: 200},
		{Path: "/noindex-page", Hits: 40, BotHits: 35, HumanHits: 5, TopBot: "GPTBot", Status: 200},
	}
	pages := []CrawlPageInfo{
		{URL: "https://example.com/", StatusCode: 200, Indexable: true, Depth: 0, Inlinks: 10, WordCount: 500, Title: "Home"},
		{URL: "https://example.com/about", StatusCode: 200, Indexable: true, Depth: 1, Inlinks: 5, WordCount: 300, Title: "About"},
		{URL: "https://example.com/noindex-page", StatusCode: 200, Indexable: false, HasNoindex: true, Depth: 2, WordCount: 100, Title: "Hidden"},
		{URL: "https://example.com/ghost-page", StatusCode: 200, Indexable: true, Depth: 3, WordCount: 200, Title: "Ghost"},
	}

	streamResult, err := MergeLogWithCrawlStream(func(fn func(URLStat) error) error {
		for _, u := range urls {
			if err := fn(u); err != nil {
				return err
			}
		}
		return nil
	}, pages)
	if err != nil {
		t.Fatalf("stream merge error: %v", err)
	}
	if streamResult.Segments[SegHealthy] != 2 {
		t.Errorf("stream healthy = %d, want 2", streamResult.Segments[SegHealthy])
	}
	if streamResult.Segments[SegCrawlWaste] != 1 {
		t.Errorf("stream crawl_waste = %d, want 1", streamResult.Segments[SegCrawlWaste])
	}
	if streamResult.Segments[SegOrphanCrawled] != 1 {
		t.Errorf("stream orphan = %d, want 1", streamResult.Segments[SegOrphanCrawled])
	}
	if streamResult.Segments[SegUncrawled] != 1 {
		t.Errorf("stream uncrawled = %d, want 1", streamResult.Segments[SegUncrawled])
	}
	if len(streamResult.OrphanPages) != 1 {
		t.Errorf("stream orphan list = %d, want 1", len(streamResult.OrphanPages))
	}
	if len(streamResult.GhostPages) != 1 {
		t.Errorf("stream ghost list = %d, want 1", len(streamResult.GhostPages))
	}
	// TopMergedPages should be sorted by LogBotHits desc.
	if len(streamResult.TopMergedPages) > 0 && streamResult.TopMergedPages[0].URL != "/" {
		t.Errorf("top merged[0] = %s, want /", streamResult.TopMergedPages[0].URL)
	}
}

func TestMergeDedupesGhostPaths(t *testing.T) {
	// Crawl pages with different full URLs but same extracted path (http vs https).
	pages := []CrawlPageInfo{
		{URL: "https://example.com/page1", StatusCode: 200, Indexable: true, Depth: 1, Title: "Page1"},
		{URL: "http://example.com/page1", StatusCode: 301, Indexable: false, Depth: 2, Title: "Page1 HTTP"},
		{URL: "https://example.com/page2", StatusCode: 200, Indexable: true, Depth: 1, Title: "Page2"},
	}
	// No log URLs — all pages should be ghosts, but /page1 only once.
	streamResult, err := MergeLogWithCrawlStream(func(fn func(URLStat) error) error {
		return nil
	}, pages)
	if err != nil {
		t.Fatalf("stream merge error: %v", err)
	}
	if streamResult.Segments[SegUncrawled] != 2 {
		t.Errorf("uncrawled = %d, want 2 (deduped /page1)", streamResult.Segments[SegUncrawled])
	}

	// Also test StreamMergeEmit.
	var emitted []MergedPage
	summary, err := StreamMergeEmit(func(fn func(URLStat) error) error {
		return nil
	}, pages, func(mp MergedPage) error {
		emitted = append(emitted, mp)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMergeEmit error: %v", err)
	}
	if len(emitted) != 2 {
		t.Errorf("emitted %d pages, want 2", len(emitted))
	}
	if summary.GhostCount != 2 {
		t.Errorf("ghost count = %d, want 2", summary.GhostCount)
	}
}

// --- Waste Detection ---

func TestClassifyWaste(t *testing.T) {
	cases := []struct {
		path string
		want WasteType
	}{
		{"/products?color=red&size=xl", WasteFacetedNav},
		{"/blog/page/5", WastePagination},
		{"/page?p=10", WastePagination},
		{"/checkout?sid=abc123", WasteSessionIDs},
		{"/track?utm_source=google", WasteSessionIDs},
		{"/search?q=test", WasteSearch},
		{"/blog/2024/03/", WasteCalendar},
		{"/assets/style.css", WasteResources},
		{"/images/logo.png?v=2", WasteResources},
		{"/api/v1/users", WasteAPI},
		{"/tag/seo/", WasteTaxonomy},
		{"/category/tools/", WasteTaxonomy},
		{"/about", ""},
		{"/products/widget", ""},
	}
	for _, c := range cases {
		got := ClassifyWaste(c.path)
		if got != c.want {
			t.Errorf("ClassifyWaste(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestAnalyzeWaste(t *testing.T) {
	urls := []URLStat{
		{Path: "/", Hits: 100, BotHits: 80, HumanHits: 20},
		{Path: "/style.css", Hits: 50, BotHits: 40, HumanHits: 10},
		{Path: "/search?q=test", Hits: 30, BotHits: 25, HumanHits: 5},
		{Path: "/about", Hits: 20, BotHits: 15, HumanHits: 5},
	}
	wa := AnalyzeWaste(urls, 160)
	if wa.WasteHits != 65 { // 40 (css) + 25 (search)
		t.Errorf("WasteHits = %d, want 65", wa.WasteHits)
	}
	if len(wa.ByType) != 2 {
		t.Errorf("ByType count = %d, want 2", len(wa.ByType))
	}
	if wa.ByType[WasteResources] == nil {
		t.Error("missing resources waste type")
	}
	if wa.ByType[WasteSearch] == nil {
		t.Error("missing search waste type")
	}
}

// --- Analyzer ---

func TestAnalyzeStream(t *testing.T) {
	log := strings.Join([]string{
		`66.249.66.1 - - [01/Apr/2026:08:00:00 +0000] "GET / HTTP/1.1" 200 5000 "-" "Googlebot/2.1"`,
		`66.249.66.1 - - [01/Apr/2026:09:00:00 +0000] "GET /about HTTP/1.1" 200 3000 "-" "Googlebot/2.1"`,
		`44.0.0.1 - - [01/Apr/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 4000 "-" "GPTBot/1.0"`,
		`192.168.1.1 - - [01/Apr/2026:11:00:00 +0000] "GET / HTTP/1.1" 200 6000 "-" "Mozilla/5.0 Chrome/120"`,
		`192.168.1.1 - - [01/Apr/2026:12:00:00 +0000] "GET /bad HTTP/1.1" 404 100 "-" "Mozilla/5.0 Chrome/120"`,
		`66.249.66.1 - - [02/Apr/2026:08:00:00 +0000] "GET / HTTP/1.1" 200 5000 "-" "Googlebot/2.1"`,
	}, "\n")

	a := newAggregator()
	_, err := ParseReader(strings.NewReader(log), FormatApacheCombined, a.add, nil)
	if err != nil {
		t.Fatalf("ParseReader error: %v", err)
	}
	stats := a.finalize()

	if stats.TotalHits != 6 {
		t.Errorf("TotalHits = %d, want 6", stats.TotalHits)
	}
	if stats.HumanHits != 2 {
		t.Errorf("HumanHits = %d, want 2", stats.HumanHits)
	}
	if len(stats.BotHits) != 2 {
		t.Errorf("BotHits count = %d, want 2", len(stats.BotHits))
	}
	if gb, ok := stats.BotHits["Googlebot"]; ok {
		if gb.Hits != 3 {
			t.Errorf("Googlebot hits = %d, want 3", gb.Hits)
		}
		if gb.UniqueURLs != 2 {
			t.Errorf("Googlebot unique URLs = %d, want 2", gb.UniqueURLs)
		}
	} else {
		t.Error("Googlebot not found in BotHits")
	}

	// Status codes
	if stats.StatusCodes[200] != 5 {
		t.Errorf("StatusCodes[200] = %d, want 5", stats.StatusCodes[200])
	}
	if stats.StatusCodes[404] != 1 {
		t.Errorf("StatusCodes[404] = %d, want 1", stats.StatusCodes[404])
	}
	if stats.StatusGroups["2xx"] != 5 {
		t.Errorf("StatusGroups[2xx] = %d, want 5", stats.StatusGroups["2xx"])
	}

	// Daily hits
	if stats.DailyHits["2026-04-01"] != 5 {
		t.Errorf("DailyHits[2026-04-01] = %d, want 5", stats.DailyHits["2026-04-01"])
	}
	if stats.DailyHits["2026-04-02"] != 1 {
		t.Errorf("DailyHits[2026-04-02] = %d, want 1", stats.DailyHits["2026-04-02"])
	}

	// Hourly
	if stats.HourlyHits[8] != 2 {
		t.Errorf("HourlyHits[8] = %d, want 2", stats.HourlyHits[8])
	}

	// Crawl budget
	if stats.CrawlBudget.TotalBotHits != 4 {
		t.Errorf("TotalBotHits = %d, want 4", stats.CrawlBudget.TotalBotHits)
	}
	if stats.CrawlBudget.UniqueURLsCrawled != 2 {
		t.Errorf("UniqueURLsCrawled = %d, want 2", stats.CrawlBudget.UniqueURLsCrawled)
	}

	// Top URLs
	if len(stats.TopURLs) == 0 {
		t.Fatal("TopURLs empty")
	}
	if stats.TopURLs[0].Path != "/" {
		t.Errorf("TopURLs[0].Path = %s, want /", stats.TopURLs[0].Path)
	}

	// Total bytes
	if stats.TotalBytes != 23100 {
		t.Errorf("TotalBytes = %d, want 23100", stats.TotalBytes)
	}
}

func TestAnalyzeHeatmapAndAITrends(t *testing.T) {
	log := strings.Join([]string{
		`66.249.66.1 - - [01/Apr/2026:08:00:00 +0000] "GET / HTTP/1.1" 200 5000 "-" "Googlebot/2.1"`,
		`44.0.0.1 - - [01/Apr/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 4000 "-" "GPTBot/1.0"`,
		`44.0.0.2 - - [02/Apr/2026:14:00:00 +0000] "GET /about HTTP/1.1" 200 3000 "-" "ClaudeBot/1.0"`,
	}, "\n")

	a := newAggregator()
	ParseReader(strings.NewReader(log), FormatApacheCombined, a.add, nil)
	stats := a.finalize()

	// Heatmap: Apr 1, 2026 is a Wednesday (dow=3)
	if stats.Heatmap[3][8] != 1 {
		t.Errorf("Heatmap[Wed][8] = %d, want 1", stats.Heatmap[3][8])
	}
	if stats.Heatmap[3][10] != 1 {
		t.Errorf("Heatmap[Wed][10] = %d, want 1", stats.Heatmap[3][10])
	}
	// Apr 2 is Thursday (dow=4)
	if stats.Heatmap[4][14] != 1 {
		t.Errorf("Heatmap[Thu][14] = %d, want 1", stats.Heatmap[4][14])
	}

	// AI bot trends
	if stats.AIBotTrends == nil {
		t.Fatal("AIBotTrends is nil")
	}
	if stats.AIBotTrends["GPTBot"]["2026-04-01"] != 1 {
		t.Errorf("GPTBot 2026-04-01 = %d, want 1", stats.AIBotTrends["GPTBot"]["2026-04-01"])
	}
	if stats.AIBotTrends["ClaudeBot"]["2026-04-02"] != 1 {
		t.Errorf("ClaudeBot 2026-04-02 = %d, want 1", stats.AIBotTrends["ClaudeBot"]["2026-04-02"])
	}
}

func TestAnalyzeMultiParallel(t *testing.T) {
	// Write three CloudFront-CSV-style files, each covering a different day,
	// and verify that AnalyzeMulti merges them into unified stats.
	dir := t.TempDir()
	header := `"date","time","location","bytes","request_ip","method","host","uri","status","referrer","user_agent","query_string","cookie","result_type"`

	writeFile := func(name, day string, hits int) string {
		p := filepath.Join(dir, name)
		var b strings.Builder
		b.WriteString(header + "\n")
		for i := 0; i < hits; i++ {
			b.WriteString(`"` + day + `","08:00:0` + string(rune('0'+i%10)) + `","IAD89","1234","66.249.64.100","GET","example.com","/page/` + day + `","200","-","Mozilla/5.0%20(compatible;%20Googlebot/2.1)","-","-","Miss"` + "\n")
		}
		if err := os.WriteFile(p, []byte(b.String()), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	paths := []string{
		writeFile("day1.csv", "2026-04-07", 10),
		writeFile("day2.csv", "2026-04-08", 20),
		writeFile("day3.csv", "2026-04-09", 15),
	}

	var progressCount int64
	result, err := AnalyzeMulti(paths, "", 0, func(p MultiProgress) {
		atomic.AddInt64(&progressCount, 1)
	}, nil)
	if err != nil {
		t.Fatalf("AnalyzeMulti error: %v", err)
	}
	if result.Stats.TotalHits != 45 {
		t.Errorf("TotalHits = %d, want 45 (10+20+15)", result.Stats.TotalHits)
	}
	if gb := result.Stats.BotHits["Googlebot"]; gb == nil || gb.Hits != 45 {
		t.Errorf("Googlebot hits = %v, want 45", gb)
	}
	// Should have daily hits across 3 days
	if len(result.Stats.DailyHits) != 3 {
		t.Errorf("DailyHits days = %d, want 3", len(result.Stats.DailyHits))
	}
	if result.Stats.DailyHits["2026-04-07"] != 10 {
		t.Errorf("day1 = %d, want 10", result.Stats.DailyHits["2026-04-07"])
	}
	if result.Stats.DailyHits["2026-04-08"] != 20 {
		t.Errorf("day2 = %d, want 20", result.Stats.DailyHits["2026-04-08"])
	}
	if len(result.Files) != 3 {
		t.Errorf("Files = %d, want 3", len(result.Files))
	}
	for _, fr := range result.Files {
		if fr.Error != "" {
			t.Errorf("file %s error: %s", fr.Filename, fr.Error)
		}
		if fr.Format != FormatCloudFront {
			t.Errorf("file %s format = %s, want cloudfront", fr.Filename, fr.Format)
		}
	}
	if progressCount == 0 {
		t.Error("no progress events fired")
	}
}

func TestAnalyzeMultiPartialFailure(t *testing.T) {
	// One good file + one unreadable path — partial failure should return
	// stats for the good file, with error noted on the bad one.
	dir := t.TempDir()
	good := filepath.Join(dir, "good.log")
	os.WriteFile(good, []byte(`66.249.66.1 - - [01/Apr/2026:08:00:00 +0000] "GET / HTTP/1.1" 200 5000 "-" "Googlebot/2.1"`+"\n"), 0644)

	result, err := AnalyzeMulti([]string{good, filepath.Join(dir, "does-not-exist.log")}, "", 2, nil, nil)
	if err != nil {
		t.Fatalf("AnalyzeMulti unexpected error: %v", err)
	}
	if result.Stats.TotalHits != 1 {
		t.Errorf("TotalHits = %d, want 1", result.Stats.TotalHits)
	}
	// Exactly one file should have an error
	errCount := 0
	for _, fr := range result.Files {
		if fr.Error != "" {
			errCount++
		}
	}
	if errCount != 1 {
		t.Errorf("expected 1 file error, got %d", errCount)
	}
}

func TestAsyncFlusherLifecycle(t *testing.T) {
	// Verify that the async flusher receives all batches and errors propagate.
	var flushedCount int64
	var flushedURLs int64
	flusher := URLStatsFlusher(func(batch []URLStat) error {
		atomic.AddInt64(&flushedCount, 1)
		atomic.AddInt64(&flushedURLs, int64(len(batch)))
		return nil
	})

	a := newAggregator()
	a.urlFlusher = flusher
	a.urlFlushAt = 100 // low threshold to trigger multiple flushes
	a.startAsyncFlusher()

	// Add enough bot entries to trigger multiple flushes.
	bot := &BotInfo{Name: "TestBot", Category: "search_engine"}
	for i := 0; i < 550; i++ {
		a.add(&LogEntry{
			Path:        "/page/" + string(rune('a'+i%26)) + "/" + string(rune('0'+i%10)),
			Status:      200,
			Bytes:       100,
			Bot:         bot,
			TSFormatted: "2026-04-07T08:00:00Z",
		})
	}
	// Flush remaining + wait for all async writes.
	a.flushURLs()
	err := a.waitFlushDone()
	if err != nil {
		t.Fatalf("waitFlushDone error: %v", err)
	}
	if flushedCount == 0 {
		t.Error("expected at least one flush batch")
	}
	if flushedURLs == 0 {
		t.Error("expected flushed URLs > 0")
	}
	// Double waitFlushDone should be safe (nil-guarded).
	err = a.waitFlushDone()
	if err != nil {
		t.Fatalf("double waitFlushDone should return nil, got: %v", err)
	}
}

func TestAsyncFlusherErrorPropagation(t *testing.T) {
	flusher := URLStatsFlusher(func(batch []URLStat) error {
		return fmt.Errorf("simulated write failure")
	})

	a := newAggregator()
	a.urlFlusher = flusher
	a.urlFlushAt = 10
	a.startAsyncFlusher()

	bot := &BotInfo{Name: "TestBot", Category: "search_engine"}
	for i := 0; i < 20; i++ {
		a.add(&LogEntry{
			Path:        "/err/" + string(rune('a'+i%26)),
			Status:      200,
			Bytes:       100,
			Bot:         bot,
			TSFormatted: "2026-04-07T08:00:00Z",
		})
	}
	a.flushURLs()
	err := a.waitFlushDone()
	if err == nil {
		t.Fatal("expected error from flusher, got nil")
	}
	if !strings.Contains(err.Error(), "simulated write failure") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAnalyzerURLPruning(t *testing.T) {
	// Verify that URL pruning doesn't panic with many unique URLs
	a := newAggregator()
	for i := 0; i < maxTopURLs*3; i++ {
		a.add(&LogEntry{
			Path:   "/page/" + strings.Repeat("a", i%100),
			Status: 200,
			Bytes:  100,
		})
	}
	stats := a.finalize()
	if len(stats.TopURLs) > maxTopURLs {
		t.Errorf("TopURLs = %d, should be <= %d", len(stats.TopURLs), maxTopURLs)
	}
}
