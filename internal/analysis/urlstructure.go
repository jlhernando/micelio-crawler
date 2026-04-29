package analysis

import (
	"net/url"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

var commonWebExtensions = map[string]bool{
	"html": true, "htm": true, "php": true, "asp": true, "aspx": true,
	"jsp": true, "cgi": true, "xml": true, "json": true, "pdf": true,
	"js": true, "css": true,
}

// AnalyzeURLStructure decomposes a URL into structured components.
func AnalyzeURLStructure(rawURL string) types.URLStructureData {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return types.URLStructureData{}
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 1 && segments[0] == "" {
		segments = nil
	}

	var lastSegment string
	if len(segments) > 0 {
		lastSegment = segments[len(segments)-1]
	}

	var fileExtension string
	if dotIdx := strings.LastIndex(lastSegment, "."); dotIdx >= 0 {
		ext := lastSegment[dotIdx+1:]
		if commonWebExtensions[strings.ToLower(ext)] {
			fileExtension = ext
		}
	}

	params := make(map[string]string)
	for k, v := range parsed.Query() {
		if _, exists := params[k]; !exists && len(v) > 0 {
			params[k] = v[0]
		}
	}

	return types.URLStructureData{
		Scheme:           parsed.Scheme,
		Hostname:         parsed.Hostname(),
		Port:             parsed.Port(),
		PathDepth:        len(segments),
		PathSegments:     segments,
		LastSegment:      lastSegment,
		QueryParams:      params,
		ParameterCount:   len(params),
		HasFragment:      parsed.Fragment != "",
		HasTrailingSlash: strings.HasSuffix(parsed.Path, "/") && len(parsed.Path) > 1,
		FileExtension:    fileExtension,
	}
}

// BuildURLStructureStats aggregates URL structure statistics across all pages.
func BuildURLStructureStats(pages []*types.PageData) *types.URLStructureStats {
	if len(pages) == 0 {
		return nil
	}

	depthDist := make(map[int]int)
	dirFreq := make(map[string]int)
	paramFreq := make(map[string]int)
	extFreq := make(map[string]int)
	totalDepth := 0
	maxDepth := 0
	urlsWithParams := 0
	urlsWithTrailingSlash := 0

	for _, p := range pages {
		data := AnalyzeURLStructure(p.URL)
		depthDist[data.PathDepth]++
		totalDepth += data.PathDepth
		if data.PathDepth > maxDepth {
			maxDepth = data.PathDepth
		}
		if data.ParameterCount > 0 {
			urlsWithParams++
		}
		if data.HasTrailingSlash {
			urlsWithTrailingSlash++
		}
		if data.FileExtension != "" {
			extFreq[data.FileExtension]++
		}
		if len(data.PathSegments) > 0 {
			dirFreq["/"+data.PathSegments[0]+"/"]++
		}
		for k := range data.QueryParams {
			paramFreq[k]++
		}
	}

	// Top 15 directories
	topDirs := sortedNamedCounts(dirFreq, 15)
	topParams := sortedNamedCounts(paramFreq, 15)
	extDist := sortedExtCounts(extFreq)

	avgDepth := 0.0
	if len(pages) > 0 {
		avgDepth = float64(totalDepth) / float64(len(pages))
	}

	return &types.URLStructureStats{
		TotalURLs:             len(pages),
		AvgPathDepth:          avgDepth,
		MaxPathDepth:          maxDepth,
		DepthDistribution:     depthDist,
		TopDirectories:        topDirs,
		TopParameters:         topParams,
		ExtensionDistribution: extDist,
		URLsWithParams:        urlsWithParams,
		URLsWithTrailingSlash: urlsWithTrailingSlash,
	}
}

func sortedNamedCounts(freq map[string]int, n int) []types.NamedCount {
	type kv struct {
		key   string
		count int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].count > items[j].count
	})
	if n > 0 && len(items) > n {
		items = items[:n]
	}
	result := make([]types.NamedCount, len(items))
	for i, item := range items {
		result[i] = types.NamedCount{Name: item.key, Count: item.count}
	}
	return result
}

func sortedExtCounts(freq map[string]int) []types.ExtensionCount {
	type kv struct {
		ext   string
		count int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].count > items[j].count
	})
	result := make([]types.ExtensionCount, len(items))
	for i, item := range items {
		result[i] = types.ExtensionCount{Extension: item.ext, Count: item.count}
	}
	return result
}
