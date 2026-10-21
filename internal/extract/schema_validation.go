package extract

import (
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// maxSchemaValidationEntries bounds per-page validation output so pages with
// huge @graph blocks cannot bloat PageData (it is persisted per page).
const maxSchemaValidationEntries = 50

// maxSchemaValidationRaw skips validation of absurdly large JSON-LD blocks.
const maxSchemaValidationRaw = 512 * 1024

// validatePageSchema validates the page's structured data. JSON-LD is read
// from the original <script> blocks rather than StructuredData.Raw, because Raw
// is truncated to jsonldMaxRaw and would fail to parse. Microdata entries come
// from the already-extracted StructuredData.
func validatePageSchema(doc *goquery.Document, sd []types.StructuredDataEntry) []types.SchemaValidationEntry {
	var input []types.StructuredDataEntry
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		if raw == "" || len(raw) > maxSchemaValidationRaw {
			return
		}
		input = append(input, types.StructuredDataEntry{Format: types.FormatJSONLD, Raw: raw})
	})
	for _, e := range sd {
		if e.Format == types.FormatMicrodata {
			input = append(input, e)
		}
	}
	if len(input) == 0 {
		return nil
	}
	results := analysis.ValidateStructuredData(input)
	if len(results) > maxSchemaValidationEntries {
		results = results[:maxSchemaValidationEntries]
	}
	return results
}
