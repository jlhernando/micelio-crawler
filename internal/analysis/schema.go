package analysis

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// typeSpec defines validation requirements for a Schema.org type.
type typeSpec struct {
	richResultType *string
	required       []string
	recommended    []string
}

func strPtr(s string) *string { return &s }

// TYPE_SPECS: validation specs for common Schema.org types that
// Google supports for rich results. Based on Google docs as of 2025.
var typeSpecs = map[string]typeSpec{
	"Product": {
		required:       []string{"name"},
		recommended:    []string{"image", "description", "offers", "brand", "review", "aggregateRating"},
		richResultType: strPtr("Product"),
	},
	"Article": {
		required:       []string{"headline", "image", "datePublished", "author"},
		recommended:    []string{"dateModified", "publisher", "description", "mainEntityOfPage"},
		richResultType: strPtr("Article"),
	},
	"NewsArticle": {
		required:       []string{"headline", "image", "datePublished", "author"},
		recommended:    []string{"dateModified", "publisher", "description"},
		richResultType: strPtr("Article"),
	},
	"BlogPosting": {
		required:       []string{"headline", "image", "datePublished", "author"},
		recommended:    []string{"dateModified", "publisher", "description"},
		richResultType: strPtr("Article"),
	},
	"FAQPage": {
		required:       []string{"mainEntity"},
		recommended:    nil,
		richResultType: strPtr("FAQ"),
	},
	"HowTo": {
		required:       []string{"name", "step"},
		recommended:    []string{"image", "totalTime", "estimatedCost", "supply", "tool"},
		richResultType: strPtr("HowTo"),
	},
	"Review": {
		required:       []string{"itemReviewed", "reviewRating"},
		recommended:    []string{"author", "datePublished", "reviewBody"},
		richResultType: strPtr("Review"),
	},
	"BreadcrumbList": {
		required:       []string{"itemListElement"},
		recommended:    nil,
		richResultType: strPtr("BreadcrumbList"),
	},
	"Event": {
		required:       []string{"name", "startDate", "location"},
		recommended:    []string{"endDate", "image", "description", "offers", "performer", "organizer"},
		richResultType: strPtr("Event"),
	},
	"Recipe": {
		required:       []string{"name", "image"},
		recommended:    []string{"author", "datePublished", "description", "prepTime", "cookTime", "totalTime", "recipeIngredient", "recipeInstructions", "nutrition"},
		richResultType: strPtr("Recipe"),
	},
	"LocalBusiness": {
		required:       []string{"name", "address"},
		recommended:    []string{"telephone", "openingHours", "image", "url", "geo", "priceRange"},
		richResultType: strPtr("LocalBusiness"),
	},
	"Organization": {
		required:       []string{"name"},
		recommended:    []string{"url", "logo", "sameAs", "contactPoint"},
		richResultType: nil,
	},
	"Person": {
		required:       []string{"name"},
		recommended:    []string{"url", "image", "sameAs", "jobTitle"},
		richResultType: nil,
	},
	"WebSite": {
		required:       []string{"name", "url"},
		recommended:    []string{"potentialAction"},
		richResultType: nil,
	},
	"WebPage": {
		required:       nil,
		recommended:    []string{"name", "description", "breadcrumb"},
		richResultType: nil,
	},
	"VideoObject": {
		required:       []string{"name", "description", "thumbnailUrl", "uploadDate"},
		recommended:    []string{"contentUrl", "duration", "embedUrl"},
		richResultType: strPtr("Video"),
	},
	"SoftwareApplication": {
		required:       []string{"name"},
		recommended:    []string{"offers", "aggregateRating", "operatingSystem", "applicationCategory"},
		richResultType: strPtr("SoftwareApp"),
	},
	"Course": {
		required:       []string{"name", "description", "provider"},
		recommended:    []string{"offers"},
		richResultType: strPtr("Course"),
	},
	"JobPosting": {
		required:       []string{"title", "description", "datePosted", "hiringOrganization", "jobLocation"},
		recommended:    []string{"baseSalary", "employmentType", "validThrough"},
		richResultType: strPtr("JobPosting"),
	},
}

var isoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:\d{2})?)?$`)

// ValidateStructuredData validates all structured data entries for a page.
func ValidateStructuredData(entries []types.StructuredDataEntry) []types.SchemaValidationEntry {
	var results []types.SchemaValidationEntry
	for _, entry := range entries {
		switch entry.Format {
		case types.FormatJSONLD:
			results = append(results, validateJsonLd(entry.Raw)...)
		case types.FormatMicrodata:
			spec, found := typeSpecs[entry.Type]
			issue := types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  "Microdata detected — property-level validation requires JSON-LD format",
			}
			var richType *string
			if found {
				richType = spec.richResultType
			}
			results = append(results, types.SchemaValidationEntry{
				Type:               entry.Type,
				Format:             types.FormatMicrodata,
				Issues:             []types.SchemaValidationIssue{issue},
				RichResultEligible: false,
				RichResultType:     richType,
			})
		}
	}
	return results
}

// validateJsonLd parses and validates a JSON-LD string.
// Returns multiple results for @graph containers.
func validateJsonLd(raw string) []types.SchemaValidationEntry {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return []types.SchemaValidationEntry{{
			Type:   "ParseError",
			Format: types.FormatJSONLD,
			Issues: []types.SchemaValidationIssue{
				{Severity: types.SeverityError, Message: "Invalid JSON in JSON-LD script"},
			},
			RichResultEligible: false,
			RichResultType:     nil,
		}}
	}

	// Handle @graph arrays
	if graphRaw, ok := parsed["@graph"]; ok {
		var graphItems []json.RawMessage
		if err := json.Unmarshal(graphRaw, &graphItems); err == nil {
			if len(graphItems) == 0 {
				return []types.SchemaValidationEntry{{
					Type:   "Graph",
					Format: types.FormatJSONLD,
					Issues: []types.SchemaValidationIssue{
						{Severity: types.SeverityWarning, Message: "@graph array is empty"},
					},
					RichResultEligible: false,
					RichResultType:     nil,
				}}
			}
			var results []types.SchemaValidationEntry
			for _, itemRaw := range graphItems {
				var item map[string]interface{}
				if err := json.Unmarshal(itemRaw, &item); err == nil {
					results = append(results, validateJsonLdObject(item, true))
				} else {
					results = append(results, types.SchemaValidationEntry{
						Type:   "Unknown",
						Format: types.FormatJSONLD,
						Issues: []types.SchemaValidationIssue{
							{Severity: types.SeverityWarning, Message: "Non-object item in @graph array"},
						},
						RichResultEligible: false,
						RichResultType:     nil,
					})
				}
			}
			return results
		}
	}

	// Single object
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil
	}
	return []types.SchemaValidationEntry{validateJsonLdObject(data, false)}
}

// validateJsonLdObject validates a single JSON-LD object.
func validateJsonLdObject(data map[string]interface{}, isGraphChild bool) types.SchemaValidationEntry {
	var issues []types.SchemaValidationIssue

	typeName := "Unknown"
	if t, ok := data["@type"]; ok {
		switch v := t.(type) {
		case string:
			typeName = v
		case []interface{}:
			if len(v) > 0 {
				typeName = fmt.Sprintf("%v", v[0])
			}
		}
	}

	// Check @context (skip for @graph children)
	if !isGraphChild {
		ctx, hasCtx := data["@context"]
		if !hasCtx || ctx == nil {
			issues = append(issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  `Missing @context (should be "https://schema.org")`,
			})
		} else {
			ctxStr := strings.ToLower(fmt.Sprintf("%v", ctx))
			if !strings.Contains(ctxStr, "schema.org") {
				issues = append(issues, types.SchemaValidationIssue{
					Severity: types.SeverityWarning,
					Message:  fmt.Sprintf(`@context "%v" does not reference schema.org`, ctx),
				})
			}
		}
	}

	// Check @type
	if _, ok := data["@type"]; !ok {
		issues = append(issues, types.SchemaValidationIssue{
			Severity: types.SeverityError,
			Message:  "Missing @type",
		})
		return types.SchemaValidationEntry{
			Type:               "Unknown",
			Format:             types.FormatJSONLD,
			Issues:             issues,
			RichResultEligible: false,
			RichResultType:     nil,
		}
	}

	// Resolve type list
	var typeList []string
	switch v := data["@type"].(type) {
	case string:
		typeList = []string{v}
	case []interface{}:
		for _, item := range v {
			typeList = append(typeList, fmt.Sprintf("%v", item))
		}
	default:
		typeList = []string{typeName}
	}

	// Find best matching spec
	var spec *typeSpec
	var primaryType string
	for _, t := range typeList {
		if s, ok := typeSpecs[t]; ok {
			spec = &s
			primaryType = t
			break
		}
	}

	displayType := strings.Join(typeList, ", ")

	if spec == nil {
		return types.SchemaValidationEntry{
			Type:               displayType,
			Format:             types.FormatJSONLD,
			Issues:             issues,
			RichResultEligible: false,
			RichResultType:     nil,
		}
	}

	richResultEligible := true

	// Check required properties
	for _, prop := range spec.required {
		if !hasValue(data, prop) {
			issues = append(issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  fmt.Sprintf("Missing required property %q", prop),
				Path:     prop,
			})
			richResultEligible = false
		}
	}

	// Check recommended properties
	for _, prop := range spec.recommended {
		if !hasValue(data, prop) {
			issues = append(issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  fmt.Sprintf("Missing recommended property %q", prop),
				Path:     prop,
			})
		}
	}

	// Type-specific deep validation
	switch primaryType {
	case "Product":
		validateOffers(data["offers"], &issues)
	case "FAQPage":
		validateFAQSchema(data, &issues)
	case "HowTo":
		validateHowToSteps(data, &issues)
	case "BreadcrumbList":
		validateBreadcrumbList(data, &issues)
	case "Review":
		validateReviewSchema(data, &issues)
	case "Event":
		validateEventSchema(data, &issues)
	case "Article", "NewsArticle", "BlogPosting":
		validateArticleSchema(data, &issues)
	}

	// Any error-severity issues disqualify rich results
	for _, issue := range issues {
		if issue.Severity == types.SeverityError {
			richResultEligible = false
			break
		}
	}

	eligible := spec.richResultType != nil && richResultEligible

	return types.SchemaValidationEntry{
		Type:               displayType,
		Format:             types.FormatJSONLD,
		Issues:             issues,
		RichResultEligible: eligible,
		RichResultType:     spec.richResultType,
	}
}

// hasValue checks if a map key exists and has a non-empty value.
func hasValue(obj map[string]interface{}, key string) bool {
	val, ok := obj[key]
	if !ok || val == nil {
		return false
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case []interface{}:
		return len(v) > 0
	}
	return true
}

func validateOffers(offers interface{}, issues *[]types.SchemaValidationIssue) {
	if offers == nil {
		return
	}
	var offerList []interface{}
	switch v := offers.(type) {
	case []interface{}:
		offerList = v
	case map[string]interface{}:
		offerList = []interface{}{v}
	default:
		return
	}

	for _, offer := range offerList {
		o, ok := offer.(map[string]interface{})
		if !ok {
			continue
		}
		oType := "Offer"
		if t, ok := o["@type"].(string); ok {
			oType = t
		}
		if oType == "AggregateOffer" {
			if !hasValue(o, "lowPrice") && !hasValue(o, "highPrice") {
				*issues = append(*issues, types.SchemaValidationIssue{
					Severity: types.SeverityWarning, Message: "AggregateOffer missing lowPrice or highPrice", Path: "offers",
				})
			}
		} else {
			if !hasValue(o, "price") && !hasValue(o, "priceSpecification") {
				*issues = append(*issues, types.SchemaValidationIssue{
					Severity: types.SeverityWarning, Message: "Offer missing price", Path: "offers.price",
				})
			}
		}
		if !hasValue(o, "priceCurrency") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning, Message: "Offer missing priceCurrency", Path: "offers.priceCurrency",
			})
		}
		if !hasValue(o, "availability") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning, Message: "Offer missing availability", Path: "offers.availability",
			})
		}
	}
}

func validateFAQSchema(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	mainEntity := data["mainEntity"]
	if mainEntity == nil {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "FAQPage missing mainEntity", Path: "mainEntity",
		})
		return
	}

	var questions []interface{}
	switch v := mainEntity.(type) {
	case []interface{}:
		questions = v
	case map[string]interface{}:
		questions = []interface{}{v}
	default:
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "FAQPage mainEntity is empty", Path: "mainEntity",
		})
		return
	}

	if len(questions) == 0 {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "FAQPage mainEntity is empty", Path: "mainEntity",
		})
		return
	}

	validQuestions := 0
	for i, q := range questions {
		qMap, ok := q.(map[string]interface{})
		if !ok {
			continue
		}
		qType, _ := qMap["@type"].(string)
		if qType != "Question" {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  fmt.Sprintf("mainEntity[%d] should be @type Question, got %q", i, qType),
				Path:     fmt.Sprintf("mainEntity[%d].@type", i),
			})
		}
		if !hasValue(qMap, "name") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  fmt.Sprintf("mainEntity[%d] missing question name", i),
				Path:     fmt.Sprintf("mainEntity[%d].name", i),
			})
		}
		answer, hasAnswer := qMap["acceptedAnswer"].(map[string]interface{})
		if !hasAnswer {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  fmt.Sprintf("mainEntity[%d] missing acceptedAnswer", i),
				Path:     fmt.Sprintf("mainEntity[%d].acceptedAnswer", i),
			})
		} else if !hasValue(answer, "text") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  fmt.Sprintf("mainEntity[%d].acceptedAnswer missing text", i),
				Path:     fmt.Sprintf("mainEntity[%d].acceptedAnswer.text", i),
			})
		} else {
			validQuestions++
		}
	}
	if validQuestions == 0 {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "FAQPage has no valid question/answer pairs",
		})
	}
}

func validateHowToSteps(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	steps := data["step"]
	if steps == nil {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "HowTo missing step", Path: "step",
		})
		return
	}
	var stepList []interface{}
	switch v := steps.(type) {
	case []interface{}:
		stepList = v
	case map[string]interface{}:
		stepList = []interface{}{v}
	default:
		return
	}
	if len(stepList) == 0 {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "HowTo step array is empty", Path: "step",
		})
		return
	}
	for i, s := range stepList {
		step, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if !hasValue(step, "text") && !hasValue(step, "name") && !hasValue(step, "itemListElement") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  fmt.Sprintf("step[%d] missing text or name", i),
				Path:     fmt.Sprintf("step[%d]", i),
			})
		}
	}
}

func validateBreadcrumbList(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	items := data["itemListElement"]
	if items == nil {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "BreadcrumbList missing itemListElement", Path: "itemListElement",
		})
		return
	}
	var itemList []interface{}
	switch v := items.(type) {
	case []interface{}:
		itemList = v
	case map[string]interface{}:
		itemList = []interface{}{v}
	default:
		return
	}
	if len(itemList) == 0 {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "BreadcrumbList itemListElement is empty", Path: "itemListElement",
		})
		return
	}
	for i, it := range itemList {
		item, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if !hasValue(item, "name") && !hasValue(item, "item") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  fmt.Sprintf("itemListElement[%d] missing name and item", i),
				Path:     fmt.Sprintf("itemListElement[%d]", i),
			})
		}
		if !hasValue(item, "position") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  fmt.Sprintf("itemListElement[%d] missing position", i),
				Path:     fmt.Sprintf("itemListElement[%d].position", i),
			})
		}
	}
}

func validateReviewSchema(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	rating, ok := data["reviewRating"].(map[string]interface{})
	if !ok {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "Review missing reviewRating", Path: "reviewRating",
		})
		return
	}
	if !hasValue(rating, "ratingValue") {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityError, Message: "reviewRating missing ratingValue", Path: "reviewRating.ratingValue",
		})
	}
	if !hasValue(rating, "bestRating") {
		*issues = append(*issues, types.SchemaValidationIssue{
			Severity: types.SeverityWarning, Message: "reviewRating missing bestRating (defaults to 5)", Path: "reviewRating.bestRating",
		})
	}
}

func validateEventSchema(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	if startDate, ok := data["startDate"].(string); ok {
		if !isoDateRe.MatchString(startDate) {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  "startDate is not valid ISO 8601 (expected YYYY-MM-DD)",
				Path:     "startDate",
			})
		}
	}
	if location, ok := data["location"].(map[string]interface{}); ok {
		if !hasValue(location, "name") && !hasValue(location, "address") {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityWarning,
				Message:  "location missing name and address",
				Path:     "location",
			})
		}
	}
}

func validateArticleSchema(data map[string]interface{}, issues *[]types.SchemaValidationIssue) {
	if author := data["author"]; author != nil {
		var authorList []interface{}
		switch v := author.(type) {
		case []interface{}:
			authorList = v
		case map[string]interface{}:
			authorList = []interface{}{v}
		}
		for i, a := range authorList {
			aMap, ok := a.(map[string]interface{})
			if !ok {
				continue
			}
			if !hasValue(aMap, "name") {
				path := "author.name"
				if len(authorList) > 1 {
					path = fmt.Sprintf("author[%d].name", i)
				}
				*issues = append(*issues, types.SchemaValidationIssue{
					Severity: types.SeverityWarning,
					Message:  "author missing name",
					Path:     path,
				})
			}
		}
	}
	if datePublished, ok := data["datePublished"].(string); ok {
		if !isoDateRe.MatchString(datePublished) {
			*issues = append(*issues, types.SchemaValidationIssue{
				Severity: types.SeverityError,
				Message:  "datePublished is not valid ISO 8601 (expected YYYY-MM-DD)",
				Path:     "datePublished",
			})
		}
	}
}

// BuildSchemaValidationStats aggregates schema validation metrics across pages.
func BuildSchemaValidationStats(pages []*types.PageData) *types.SchemaValidationStats {
	stats := &types.SchemaValidationStats{
		RichResultEligible: make(map[string]int),
		TypeDistribution:   make(map[string]int),
	}
	issueCounts := make(map[string]int)

	for _, p := range pages {
		if len(p.SchemaValidation) == 0 {
			continue
		}
		stats.PagesWithSchema++
		pageHasError := false
		for _, sv := range p.SchemaValidation {
			stats.TypeDistribution[sv.Type]++
			if sv.RichResultEligible && sv.RichResultType != nil {
				stats.RichResultEligible[*sv.RichResultType]++
			}
			for _, issue := range sv.Issues {
				issueCounts[issue.Message]++
				if issue.Severity == types.SeverityError {
					pageHasError = true
				}
			}
		}
		if pageHasError {
			stats.PagesWithErrors++
		} else {
			stats.PagesWithValidSchema++
		}
	}

	// Top 20 issues
	type kv struct {
		msg   string
		count int
	}
	var sorted []kv
	for msg, count := range issueCounts {
		sorted = append(sorted, kv{msg, count})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if len(sorted) > 20 {
		sorted = sorted[:20]
	}
	for _, item := range sorted {
		stats.TopIssues = append(stats.TopIssues, types.MessageCount{
			Message: item.msg,
			Count:   item.count,
		})
	}

	return stats
}
