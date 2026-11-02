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
	}
	if st != nil {
		st.AvgSimilarity = simSum / st.Compared
	}
	return st
}
