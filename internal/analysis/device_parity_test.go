package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestBuildDeviceParityRawAndJS(t *testing.T) {
	raw := &types.DeviceSnapshot{}
	pages := []*types.PageData{
		// Same raw HTML; JS adds the h1 on the primary device only.
		{DeviceCompare: &types.DeviceComparison{Primary: types.DeviceSnapshot{Raw: raw}, Secondary: types.DeviceSnapshot{Raw: raw}, PrimaryJS: []string{types.DeviceDiffH1}}},
		// Raw HTML differs between devices; JS changes links on both.
		{DeviceCompare: &types.DeviceComparison{Primary: types.DeviceSnapshot{Raw: raw}, Secondary: types.DeviceSnapshot{Raw: raw}, RawDiffs: []string{types.DeviceDiffTitle},
			PrimaryJS: []string{types.DeviceDiffLinks}, SecondaryJS: []string{types.DeviceDiffLinks}}},
		// Not rendered: no raw snapshots.
		{DeviceCompare: &types.DeviceComparison{}},
	}
	st := BuildDeviceParity(pages)
	if st.Compared != 3 || st.RawCompared != 2 || st.RawIdentical != 1 || st.RawIssues[types.DeviceDiffTitle] != 1 {
		t.Errorf("raw: compared %d rawCompared %d rawIdentical %d rawIssues %v", st.Compared, st.RawCompared, st.RawIdentical, st.RawIssues)
	}
	if st.JSChanged != 2 || st.PrimaryJSPages != 2 || st.SecondaryJSPages != 1 || st.PrimaryJS[types.DeviceDiffH1] != 1 || st.SecondaryJS[types.DeviceDiffLinks] != 1 {
		t.Errorf("js: %+v", st)
	}
}
