package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchCWVCrUX(t *testing.T) {
	var bodies []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The key goes in a header, never in the URL (URLs reach logs and errors).
		if r.Header.Get("X-Goog-Api-Key") != "ck" || r.URL.RawQuery != "" {
			t.Errorf("key header %q, query %q", r.Header.Get("X-Goog-Api-Key"), r.URL.RawQuery)
		}
		var b map[string]string
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		if b["formFactor"] == "DESKTOP" {
			w.WriteHeader(404) // not enough desktop traffic
			return
		}
		w.Write([]byte(`{"record":{"metrics":{
			"largest_contentful_paint":{"histogram":[{"density":0.6},{"density":0.25},{"density":0.15}],"percentiles":{"p75":3100}},
			"cumulative_layout_shift":{"histogram":[{"density":0.9},{"density":0.06},{"density":0.04}],"percentiles":{"p75":"0.05"}},
			"interaction_to_next_paint":{"histogram":[{"density":0.8},{"density":0.15},{"density":0.05}],"percentiles":{"p75":180}}}}}`))
	}))
	defer srv.Close()
	old := cruxEndpoint
	setCruxEndpoint(srv.URL)
	defer setCruxEndpoint(old)

	rep := FetchCWV(context.Background(), srv.Client(), "ck", "", "https://www.ex.com/es/page")
	if rep.Origin != "https://www.ex.com" || rep.Source != "crux" || len(rep.Field) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if len(bodies) != 2 || bodies[0]["origin"] != "https://www.ex.com" || bodies[0]["formFactor"] != "PHONE" {
		t.Errorf("requests %v", bodies)
	}
	f := rep.Field[0]
	if f.FormFactor != "phone" || f.Scope != "origin" || len(f.Metrics) != 3 {
		t.Fatalf("field %+v", f)
	}
	if lcp := f.Metrics[0]; lcp.ID != "lcp" || lcp.P75 != 3100 || lcp.Good != 0.6 || lcp.Poor != 0.15 {
		t.Errorf("lcp %+v", lcp)
	}
	if cls := f.Metrics[2]; cls.ID != "cls" || cls.P75 != 0.05 {
		t.Errorf("CLS p75 is a string in CrUX: %+v", cls)
	}
}

func TestFetchCWVPageSpeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "pk" || r.URL.Query().Has("key") {
			t.Errorf("PageSpeed key must be a header, query %q", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.Query().Get("fields"), "originLoadingExperience") {
			t.Error("no fields filter: the full answer is megabytes")
		}
		w.Write([]byte(`{"originLoadingExperience":{"metrics":{
			"LARGEST_CONTENTFUL_PAINT_MS":{"percentile":2300,"distributions":[{"proportion":0.8},{"proportion":0.12},{"proportion":0.08}]},
			"CUMULATIVE_LAYOUT_SHIFT_SCORE":{"percentile":12,"distributions":[{"proportion":0.7},{"proportion":0.2},{"proportion":0.1}]}}},
			"lighthouseResult":{"categories":{"performance":{"score":0.42}},"audits":{"largest-contentful-paint":{"numericValue":5200},"total-blocking-time":{"numericValue":640}}}}`))
	}))
	defer srv.Close()
	old := psiEndpoint
	setPSIEndpoint(srv.URL)
	defer setPSIEndpoint(old)

	rep := FetchCWV(context.Background(), srv.Client(), "", "pk", "https://ex.com/")
	if rep.Source != "psi" || len(rep.Field) != 1 || rep.Lab == nil {
		t.Fatalf("report %+v", rep)
	}
	if cls := rep.Field[0].Metrics[1]; cls.ID != "cls" || cls.P75 != 0.12 {
		t.Errorf("PageSpeed CLS is ×100: %+v", cls)
	}
	if rep.Lab.Score != 42 || rep.Lab.LCP != 5200 || rep.Lab.TBT != 640 {
		t.Errorf("lab %+v", rep.Lab)
	}
}

func TestFetchCWVNoKey(t *testing.T) {
	rep := FetchCWV(context.Background(), http.DefaultClient, "", "", "https://ex.com/")
	if rep.Missing != "no-key" || rep.Source != "none" {
		t.Errorf("report %+v", rep)
	}
}

func TestScrubKey(t *testing.T) {
	err := scrubKey(errors.New(`Get "https://x/?key=AIzaSECRET": dial tcp`), "AIzaSECRET")
	if strings.Contains(err.Error(), "AIzaSECRET") {
		t.Errorf("key leaked: %v", err)
	}
}
