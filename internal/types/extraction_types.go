package types

import "time"

// --- SEO Sub-types ---

// HeadingData holds heading content grouped by level.
type HeadingData struct {
	H1 []string `json:"h1,omitempty"`
	H2 []string `json:"h2,omitempty"`
	H3 []string `json:"h3,omitempty"`
	H4 []string `json:"h4,omitempty"`
	H5 []string `json:"h5,omitempty"`
	H6 []string `json:"h6,omitempty"`
}

// ImageData holds SEO-relevant image information.
type ImageData struct {
	Alt             *string `json:"alt"`
	Width           *string `json:"width,omitempty"`
	Height          *string `json:"height,omitempty"`
	Src             string  `json:"src"`
	AltLength       int     `json:"altLength,omitempty"`
	MissingAlt      bool    `json:"missingAlt,omitempty"`
	HasAltAttribute bool    `json:"hasAltAttribute"`
	AltTooLong      bool    `json:"altTooLong,omitempty"`
	MissingWidth    bool    `json:"missingWidth,omitempty"`
	MissingHeight   bool    `json:"missingHeight,omitempty"`
}

// HreflangEntry represents a single hreflang annotation.
type HreflangEntry struct {
	Lang string `json:"lang"`
	Href string `json:"href"`
}

// StructuredDataEntry holds a single piece of structured data.
type StructuredDataEntry struct {
	Type   string               `json:"type"`
	Format StructuredDataFormat `json:"format"`
	Raw    string               `json:"raw"`
}

// SchemaValidationIssue represents a single schema validation issue.
type SchemaValidationIssue struct {
	Severity IssueSeverity `json:"severity"`
	Message  string        `json:"message"`
	Path     string        `json:"path,omitempty"`
}

// SchemaValidationEntry holds validation results for structured data.
type SchemaValidationEntry struct {
	RichResultType     *string                 `json:"richResultType"`
	Type               string                  `json:"type"`
	Format             StructuredDataFormat    `json:"format"`
	Issues             []SchemaValidationIssue `json:"issues"`
	RichResultEligible bool                    `json:"richResultEligible"`
}

// SecurityData holds security-related page information.
type SecurityData struct {
	MixedContentURLs []string `json:"mixedContentUrls,omitempty"`
	IsHTTPS          bool     `json:"isHttps"`
	HasMixedContent  bool     `json:"hasMixedContent,omitempty"`
	HasHSTS          bool     `json:"hasHsts,omitempty"`
	HasXFrameOptions bool     `json:"hasXFrameOptions,omitempty"`
	HasCSP           bool     `json:"hasCsp,omitempty"`
}

// AnchorData holds information about a single link on a page.
type AnchorData struct {
	Rel              *string      `json:"rel,omitempty"`
	Href             string       `json:"href"`
	Text             string       `json:"text"`
	Position         LinkPosition `json:"position,omitempty"`
	IsInternal       bool         `json:"isInternal,omitempty"`
	IsNonDescriptive bool         `json:"isNonDescriptive,omitempty"`
}

// IndexabilityData holds indexability determination.
type IndexabilityData struct {
	Reason    string `json:"reason"`
	Indexable bool   `json:"indexable"`
}

// ReadabilityData holds text readability metrics.
// Google's HCU guidance correlates readability with "helpful content" but
// Flesch-Kincaid is NOT a confirmed direct ranking signal.
// Confidence: HEURISTIC (correlates with quality, not a confirmed signal).
type ReadabilityData struct {
	FleschKincaid       float64 `json:"fleschKincaid"`
	SentenceCount       int     `json:"sentenceCount"`
	AvgWordsPerSentence float64 `json:"avgWordsPerSentence"`
	SyllableCount       int     `json:"syllableCount"`
}

// TextLength holds text content with its length.
type TextLength struct {
	Text   string `json:"text"`
	Length int    `json:"length"`
}

// RenderDiff represents a difference between pre- and post-render page state.
type RenderDiff struct {
	Field    string `json:"field"`
	Original string `json:"original"`
	Rendered string `json:"rendered"`
}

// --- Rankpedia-informed SEO signals ---

// FreshnessData holds content freshness signals.
// Source: Patent US8924379, US8583617, DOJ (semanticDateInfo, bylineDate).
// Google applies FreshnessTwiddler to top 20-30 results for time-sensitive queries.
// Confidence: CONFIRMED (DOJ FreshnessTwiddler, Leak QDF signals).
type FreshnessData struct {
	BylineDate      *time.Time `json:"bylineDate,omitempty"`
	ModifiedDate    *time.Time `json:"modifiedDate,omitempty"`
	DateSource      string     `json:"dateSource,omitempty"`
	ContentAgeDays  int        `json:"contentAgeDays,omitempty"`
	DateConsistency bool       `json:"dateConsistency,omitempty"`
}

// ContentRichnessData holds content effort/richness signals.
// Proxy for Google's contentEffort LLM signal (DOJ, Patent US8682892).
// Confidence: STRONG_PROXY (contentEffort confirmed in leak; weights are estimates).
type ContentRichnessData struct {
	HeadingDepth       int     `json:"headingDepth"`
	HeadingCount       int     `json:"headingCount"`
	ListCount          int     `json:"listCount"`
	ListItemCount      int     `json:"listItemCount"`
	TableCount         int     `json:"tableCount"`
	ImageInContent     int     `json:"imageInContent"`
	VideoEmbeds        int     `json:"videoEmbeds"`
	CodeBlocks         int     `json:"codeBlocks"`
	BlockquoteCount    int     `json:"blockquoteCount"`
	DefinitionLists    int     `json:"definitionLists"`
	RichnessScore      float64 `json:"richnessScore"`
	InformationDensity float64 `json:"informationDensity"`
}

// EEATData holds E-E-A-T (Experience, Expertise, Authoritativeness, Trust) signals.
// Source: DOJ (expertise signals), Patent US9697259 (authorshipMarkup).
// Confidence: STRONG_PROXY (E-E-A-T is a quality rater concept; proxy signals are heuristic).
// Note: CitationCount by TLD (.edu/.gov) is HEURISTIC — not in official E-E-A-T docs.
type EEATData struct {
	AuthorName         string `json:"authorName,omitempty"`
	AuthorSchemaType   string `json:"authorSchemaType,omitempty"`
	CitationCount      int    `json:"citationCount"`
	ExternalRefCount   int    `json:"externalRefCount"`
	HasAuthor          bool   `json:"hasAuthor"`
	HasAuthorPage      bool   `json:"hasAuthorPage"`
	HasAboutPage       bool   `json:"hasAboutPage"`
	HasContactPage     bool   `json:"hasContactPage"`
	HasEditorialPolicy bool   `json:"hasEditorialPolicy"`
}

// PassageReadinessData holds passage ranking readiness signals.
// Source: Patent US20160078102, US9940367.
// Confidence: STRONG_PROXY (passage ranking confirmed; section word ranges are heuristic).
type PassageReadinessData struct {
	AvgWordsPerSection float64 `json:"avgWordsPerSection"`
	PassageScore       float64 `json:"passageScore"`
	SectionsCount      int     `json:"sectionsCount"`
	HasDirectAnswers   int     `json:"hasDirectAnswers"`
	HasDefinitions     bool    `json:"hasDefinitions"`
	HasFAQStructure    bool    `json:"hasFaqStructure"`
	HasHowToStructure  bool    `json:"hasHowToStructure"`
}

// AIReadinessData holds AI Overview readiness signals.
// Source: DOJ (FastSearch, SnippetBrain), Leak (Q* >= 0.4 gate).
type AIReadinessData struct {
	TopPassageLength     int     `json:"topPassageLength"`
	AIReadinessScore     float64 `json:"aiReadinessScore"`
	HasConciseDefinition bool    `json:"hasConciseDefinition"`
	HasStructuredAnswer  bool    `json:"hasStructuredAnswer"`
	HasFAQSchema         bool    `json:"hasFaqSchema"`
	HasHowToSchema       bool    `json:"hasHowToSchema"`
}

// ConsensusReadinessData holds signals indicating content aligns with consensus patterns.
// Google counts passages that agree with, contradict, or remain neutral to "general consensus."
// Source: Candour Agency exploit (Mark Williams-Cook, Dec 2024), $13,337 Google VRP bounty.
// Confidence: CONFIRMED (exploit data across 800K domains).
type ConsensusReadinessData struct {
	ClaimCount      int     `json:"claimCount"`      // Passages with claim-like structure
	EvidenceCount   int     `json:"evidenceCount"`   // Passages with supporting evidence/citations
	FAQCount        int     `json:"faqCount"`         // Question-answer pairs
	MythBustCount   int     `json:"mythBustCount"`   // Myth/fact correction patterns
	ConsensusScore  float64 `json:"consensusScore"`  // Composite readiness (0-1)
}

// TopicalityData holds T* (Topicality) alignment signals.
// T* has three sub-signals (DOJ ABC framework): Anchors (what the web says),
// Body (what the page says), Clicks (what users say via NavBoost).
// This struct captures Anchors + Body. Clicks require user data (not crawl-time).
// Source: DOJ (HJ Kim PXR0356, Nayak PXR0357), Leak (Ascorer T* pillar).
// Confidence: CONFIRMED (DOJ testimony) for structure; STRONG_PROXY for weights.
type TopicalityData struct {
	TitleBodyOverlap    float64 `json:"titleBodyOverlap"`
	TitleH1Alignment    float64 `json:"titleH1Alignment"`
	HeadingBodyCoverage float64 `json:"headingBodyCoverage"`
	AnchorTitleRelevance float64 `json:"anchorTitleRelevance"` // T*-Anchors: how well inbound anchor text aligns with title
	TopicalConsistency  float64 `json:"topicalConsistency"`
}

// AnchorHealthData holds anchor text quality signals.
// Source: Patent US7533092 (anchorMismatch), DOJ (BayesSpam, link spam detection).
// Confidence: CONFIRMED (anchor diversity is a known spam signal).
type AnchorHealthData struct {
	InboundAnchorDiversity float64 `json:"inboundAnchorDiversity"`
	OverOptimizedAnchors   int     `json:"overOptimizedAnchors"`
	BrandAnchors           int     `json:"brandAnchors"`
	GenericAnchors         int     `json:"genericAnchors"`
	NakedURLAnchors        int     `json:"nakedURLAnchors"`
}
