package types

// Agent readiness: can AI agents and AI crawlers find, read and use a site?
// The crawler probes each crawled host once (robots.txt AI rules, llms.txt,
// markdown negotiation, /.well-known agent files); analysis adds checks from
// the crawled pages (structured data, content that needs JavaScript) and
// scores everything.

// Agent check statuses.
const (
	AgentPass = "pass"
	AgentWarn = "warn"
	AgentFail = "fail"
	AgentNA   = "na" // not applicable or optional and absent: not scored
)

// AgentCheck is one agent-readiness check.
type AgentCheck struct {
	ID       string `json:"id"`
	Category string `json:"category"` // discovery, content, access, protocols
	Title    string `json:"title"`
	Status   string `json:"status"` // pass, warn, fail, na
	Evidence string `json:"evidence,omitempty"`
	Fix      string `json:"fix,omitempty"` // short advice, empty on pass
	URL      string `json:"url,omitempty"` // the URL that was checked
}

// AgentBotAccess is what robots.txt tells one AI bot about the home page.
type AgentBotAccess struct {
	Bot      string `json:"bot"`     // robots.txt token, e.g. GPTBot
	Owner    string `json:"owner"`   // OpenAI, Anthropic, ...
	Purpose  string `json:"purpose"` // training, search or user (fetches for a person)
	Allowed  *bool  `json:"allowed"` // nil when robots.txt could not be read
	Explicit bool   `json:"explicit,omitempty"`
	// LiveStatus is the HTTP status of a home page request sent with this
	// bot's User-Agent (only for the few bots tested live).
	LiveStatus int `json:"liveStatus,omitempty"`
}

// AgentCategoryScore is the score of one check category.
type AgentCategoryScore struct {
	Category string `json:"category"`
	Score    int    `json:"score"` // 0-100, -1 when every check is na
	Pass     int    `json:"pass"`
	Warn     int    `json:"warn"`
	Fail     int    `json:"fail"`
	NA       int    `json:"na"`
}

// AgentHostReport is the agent-readiness result for one host.
type AgentHostReport struct {
	Host           string               `json:"host"`
	URL            string               `json:"url"` // origin that was probed
	CheckedAt      string               `json:"checkedAt,omitempty"`
	Requests       int                  `json:"requests"`
	Error          string               `json:"error,omitempty"`
	Checks         []AgentCheck         `json:"checks"`
	Bots           []AgentBotAccess     `json:"bots,omitempty"`
	ContentSignals []string             `json:"contentSignals,omitempty"` // Content-Signal values from robots.txt
	HomeSchema     []string             `json:"homeSchema,omitempty"`     // JSON-LD types in the home page raw HTML
	Score          int                  `json:"score"`
	Level          string               `json:"level,omitempty"`
	Categories     []AgentCategoryScore `json:"categories,omitempty"`
}

// AgentReadinessStats is stats.agentReadiness. Score and Level are the
// first (seed) host's.
type AgentReadinessStats struct {
	Score int               `json:"score"`
	Level string            `json:"level"`
	Hosts []AgentHostReport `json:"hosts"`
}
