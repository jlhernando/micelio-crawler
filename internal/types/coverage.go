package types

// CrawlCoverage records decisions in one execution, not an inventory of a site.
// Repeated rejected URLs count as repeated attempts. Samples are bounded.
type CrawlCoverage struct {
	AcceptedURLs int               `json:"acceptedUrls"`
	PendingURLs  int               `json:"pendingUrls"`
	Final        bool              `json:"final"`
	Reasons      []ExclusionReason `json:"reasons"`
}

type ExclusionReason struct {
	Reason   string   `json:"reason"`
	Rule     string   `json:"rule,omitempty"`
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
}
