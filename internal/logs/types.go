// Package logs provides server access log parsing and analysis for SEO.
package logs

import "time"

type LogEntry struct {
	IP           string
	Timestamp    time.Time
	Method       string
	Path         string
	Status       int
	Bytes        int64
	Referer      string
	UserAgent    string
	ResponseTime float64 // seconds, from formats that include it (CloudFront, ALB, Cloudflare, custom)
	Bot          *BotInfo
	// Pre-computed by parser goroutines to offload work from the single aggregator core.
	TSFormatted string // "2006-01-02T15:04:05Z"
	Hour        int    // 0-23
	Weekday     int    // 0=Sunday .. 6=Saturday
}

type BotInfo struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Mobile   bool   `json:"mobile"`
}

type LogStats struct {
	TotalLines    int64                         `json:"totalLines"`
	TotalHits     int64                         `json:"totalHits"`
	ParseErrors   int64                         `json:"parseErrors"`
	DateRange     [2]string                     `json:"dateRange"`
	BotHits       map[string]*BotStats          `json:"botHits"`
	HumanHits     int64                         `json:"humanHits"`
	StatusCodes   map[int]int64                 `json:"statusCodes"`
	StatusGroups  map[string]int64              `json:"statusGroups"`
	TopURLs       []URLStat                     `json:"topUrls"`
	HourlyHits    [24]int64                     `json:"hourlyHits"`
	DailyHits     map[string]int64              `json:"dailyHits"`
	BotDailyHits  map[string]map[string]int64   `json:"botDailyHits"`
	TotalBytes    int64                         `json:"totalBytes"`
	CrawlBudget   CrawlBudgetStats              `json:"crawlBudget"`
	Waste         *WasteAnalysis                `json:"waste,omitempty"`
	Heatmap       [7][24]int64                  `json:"heatmap"`       // [dayOfWeek][hour] hit counts (0=Sun)
	BotHeatmap    map[string][7][24]int64       `json:"botHeatmap,omitempty"` // per-bot heatmaps (top 5 bots only)
	AIBotTrends   map[string]map[string]int64   `json:"aiBotTrends,omitempty"` // AI bot daily hits for emergence tracking
	// BotHourlyHits records bot activity with hourly granularity across the
	// entire timeframe (key format: "2006-01-02T15:00"). Used by the Overview
	// chart to render hourly bars when timeframe ≤ 48h.
	BotHourlyHits map[string]map[string]int64 `json:"botHourlyHits,omitempty"`
	// HourlyHitsTimeline is the non-bot equivalent — total hits per hour-stamp.
	HourlyHitsTimeline map[string]int64 `json:"hourlyHitsTimeline,omitempty"`
}

type BotStats struct {
	Hits        int64          `json:"hits"`
	UniqueURLs  int            `json:"uniqueUrls"`
	StatusCodes map[int]int64  `json:"statusCodes"`
	Bytes       int64          `json:"bytes"`
	Category    string         `json:"category"`
	Mobile      bool           `json:"mobile"`
	FirstSeen   string         `json:"firstSeen"`
	LastSeen    string         `json:"lastSeen"`
}

type URLStat struct {
	Path      string `json:"path"`
	Hits      int64  `json:"hits"`
	BotHits   int64  `json:"botHits"`
	HumanHits int64  `json:"humanHits"`
	TopBot    string `json:"topBot"`
	Status    int    `json:"status"`
	Bytes     int64  `json:"bytes"`
}

type CrawlBudgetStats struct {
	TotalBotHits      int64   `json:"totalBotHits"`
	UniqueURLsCrawled int     `json:"uniqueUrlsCrawled"`
	CrawlEfficiency   float64 `json:"crawlEfficiency"`
	ErrorRate         float64 `json:"errorRate"`
	MobileCrawlShare  float64 `json:"mobileCrawlShare"`
	AIBotShare        float64 `json:"aiBotShare"`
}
