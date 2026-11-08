package storage

import (
	"bytes"
	"encoding/json"
	"github.com/micelio/micelio/internal/types"
)

// stringArrayCount validates like []string, but does not allocate a string and
// slice entry for every link that the analysis loader immediately discards.
// encoding/json validates syntax before invoking this method.
type stringArrayCount int

func (c *stringArrayCount) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	fallback := func() error {
		var values []string
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		*c = stringArrayCount(len(values))
		return nil
	}
	if bytes.Equal(data, []byte("null")) {
		*c = 0
		return nil
	}
	if len(data) < 2 || data[0] != '[' {
		return fallback()
	}
	n := 0
	for i := 1; i < len(data); {
		for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
			i++
		}
		if i >= len(data) {
			return fallback()
		}
		if data[i] == ']' {
			*c = stringArrayCount(n)
			return nil
		}
		switch data[i] {
		case '"':
			i++
			for i < len(data) {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '"' {
					i++
					break
				}
				i++
			}
		case 'n':
			if !bytes.HasPrefix(data[i:], []byte("null")) {
				return fallback()
			}
			i += 4
		default:
			return fallback()
		}
		n++
		for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
			i++
		}
		if i < len(data) && data[i] == ',' {
			i++
		}
	}
	return fallback()
}

type analysisPageData types.PageData

func decodeAnalysisPage(data []byte) (*types.PageData, error) {
	page := new(types.PageData)
	projection := struct {
		*analysisPageData
		InternalLinks stringArrayCount `json:"internalLinks"`
		ExternalLinks stringArrayCount `json:"externalLinks"`
	}{analysisPageData: (*analysisPageData)(page)}
	if err := json.Unmarshal(data, &projection); err != nil {
		return nil, err
	}
	if page.InternalLinkCount == 0 {
		page.InternalLinkCount = int(projection.InternalLinks)
	}
	if page.ExternalLinkCount == 0 {
		page.ExternalLinkCount = int(projection.ExternalLinks)
	}
	return page, nil
}
