package analysis

import "github.com/micelio/micelio/internal/types"

// BuildDeviceParity sums up a device comparison crawl, or nil when no page
// was compared.
func BuildDeviceParity(pages []*types.PageData) *types.DeviceParityStats {
	var st *types.DeviceParityStats
	simSum := 0
	for _, p := range pages {
		c := p.DeviceCompare
		if c == nil {
			continue
		}
		if st == nil {
			st = &types.DeviceParityStats{Primary: c.PrimaryDevice, Secondary: c.Device, Issues: map[string]int{}}
		}
		st.Compared++
		if c.Primary.Rendered || c.Secondary.Rendered {
			st.Rendered = true
		}
		if len(c.Diffs) == 0 {
			st.Identical++
		}
		for _, d := range c.Diffs {
			st.Issues[d]++
		}
		st.LinksOnlyPrimary += c.LinksOnlyPrimaryCount
		st.LinksOnlySecondary += c.LinksOnlySecondaryCount
		st.LinksParamOnly += c.LinksParamOnly
		simSum += c.TextSimilarity
		if c.Primary.Raw != nil && c.Secondary.Raw != nil {
			if st.RawIssues == nil {
				st.RawIssues, st.PrimaryJS, st.SecondaryJS = map[string]int{}, map[string]int{}, map[string]int{}
			}
			st.RawCompared++
			if len(c.RawDiffs) == 0 {
				st.RawIdentical++
			}
			for _, d := range c.RawDiffs {
				st.RawIssues[d]++
			}
		}
		if len(c.PrimaryJS) > 0 || len(c.SecondaryJS) > 0 {
			st.JSChanged++
		}
		if len(c.PrimaryJS) > 0 {
			st.PrimaryJSPages++
		}
		if len(c.SecondaryJS) > 0 {
			st.SecondaryJSPages++
		}
		countIDs(&st.PrimaryJS, c.PrimaryJS)
		countIDs(&st.SecondaryJS, c.SecondaryJS)
	}
	if st != nil {
		st.AvgSimilarity = simSum / st.Compared
	}
	return st
}

func countIDs(m *map[string]int, ids []string) {
	if len(ids) == 0 {
		return
	}
	if *m == nil {
		*m = map[string]int{}
	}
	for _, id := range ids {
		(*m)[id]++
	}
}
