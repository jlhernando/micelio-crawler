package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Core Web Vitals for a report: CrUX for the whole origin (real users,
// p75 over 28 days, phone and desktop) and, with a PageSpeed key, one
// Lighthouse run of the start page. Both need a Google API key.

var cwvMetricKeys = []struct{ id, crux, psi string }{
	{"lcp", "largest_contentful_paint", "LARGEST_CONTENTFUL_PAINT_MS"},
	{"inp", "interaction_to_next_paint", "INTERACTION_TO_NEXT_PAINT"},
	{"cls", "cumulative_layout_shift", "CUMULATIVE_LAYOUT_SHIFT_SCORE"},
	{"fcp", "first_contentful_paint", "FIRST_CONTENTFUL_PAINT_MS"},
	{"ttfb", "experimental_time_to_first_byte", "EXPERIMENTAL_TIME_TO_FIRST_BYTE"},
}

// FetchCWV gathers Core Web Vitals for the origin of pageURL. cruxKey is
// tried first (fast, phone and desktop); psiKey adds a lab run and is the
// fallback for field data. With no key the report says so.
func FetchCWV(ctx context.Context, client *http.Client, cruxKey, psiKey, pageURL string) *types.CWVReport {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return &types.CWVReport{Source: "none", Missing: "invalid URL"}
	}
	rep := &types.CWVReport{Origin: u.Scheme + "://" + u.Host, Source: "none"}
	if cruxKey == "" && psiKey == "" {
		rep.Missing = "no-key"
		return rep
	}
	var errs []string
	if cruxKey != "" {
		for _, ff := range []string{"PHONE", "DESKTOP"} {
			f, err := cruxOrigin(ctx, client, cruxKey, rep.Origin, ff)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if f != nil {
				rep.Field = append(rep.Field, *f)
			}
		}
		if len(rep.Field) > 0 {
			rep.Source = "crux"
		}
	}
	if psiKey != "" {
		field, lab, err := psiRun(ctx, client, psiKey, pageURL)
		if err != nil {
			errs = append(errs, err.Error())
		}
		rep.Lab = lab
		if len(rep.Field) == 0 && field != nil {
			rep.Field = append(rep.Field, *field)
			rep.Source = "psi"
		} else if rep.Source == "none" && lab != nil {
			rep.Source = "psi"
		}
	}
	if len(rep.Field) == 0 && rep.Lab == nil {
		rep.Missing = "no-data"
		if len(errs) > 0 {
			rep.Missing = errs[0]
		}
	}
	return rep
}

func cruxOrigin(ctx context.Context, client *http.Client, key, origin, formFactor string) (*types.CWVField, error) {
	body, _ := json.Marshal(map[string]string{"origin": origin, "formFactor": formFactor})
	req, err := http.NewRequestWithContext(ctx, "POST", cruxEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAPIKey(req, key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, scrubKey(fmt.Errorf("crux: %w", err), key)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, cruxMaxBodySize))
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // the origin has too little traffic for CrUX
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crux: HTTP %d", resp.StatusCode)
	}
	var r struct {
		Record struct {
			Metrics map[string]struct {
				Histogram []struct {
					Density float64 `json:"density"`
				} `json:"histogram"`
				Percentiles struct {
					P75 json.RawMessage `json:"p75"`
				} `json:"percentiles"`
			} `json:"metrics"`
		} `json:"record"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("crux: parse: %w", err)
	}
	f := &types.CWVField{FormFactor: strings.ToLower(formFactor), Scope: "origin"}
	for _, k := range cwvMetricKeys {
		m, ok := r.Record.Metrics[k.crux]
		if !ok {
			continue
		}
		v, ok := p75Value(m.Percentiles.P75)
		if !ok {
			continue
		}
		cm := types.CWVMetric{ID: k.id, P75: v}
		if len(m.Histogram) == 3 {
			cm.Good, cm.NI, cm.Poor = m.Histogram[0].Density, m.Histogram[1].Density, m.Histogram[2].Density
		}
		f.Metrics = append(f.Metrics, cm)
	}
	if len(f.Metrics) == 0 {
		return nil, nil
	}
	return f, nil
}

// psiFields keeps the PageSpeed answer small: field data and a few audits.
const psiFields = "loadingExperience,originLoadingExperience,lighthouseResult(finalUrl,categories/performance/score," +
	"audits/largest-contentful-paint/numericValue,audits/total-blocking-time/numericValue,audits/cumulative-layout-shift/numericValue," +
	"audits/first-contentful-paint/numericValue,audits/speed-index/numericValue,audits/server-response-time/numericValue)"

func psiRun(ctx context.Context, client *http.Client, key, pageURL string) (*types.CWVField, *types.CWVLab, error) {
	q := url.Values{"url": {pageURL}, "strategy": {"mobile"}, "category": {"performance"}, "fields": {psiFields}}
	req, err := http.NewRequestWithContext(ctx, "GET", psiEndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	setAPIKey(req, key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, scrubKey(fmt.Errorf("pagespeed: %w", err), key)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("pagespeed: HTTP %d", resp.StatusCode)
	}
	type experience struct {
		Metrics map[string]struct {
			Percentile    float64 `json:"percentile"`
			Distributions []struct {
				Proportion float64 `json:"proportion"`
			} `json:"distributions"`
		} `json:"metrics"`
	}
	var r struct {
		Loading experience `json:"loadingExperience"`
		Origin  experience `json:"originLoadingExperience"`
		LH      struct {
			FinalURL   string `json:"finalUrl"`
			Categories struct {
				Performance struct {
					Score *float64 `json:"score"`
				} `json:"performance"`
			} `json:"categories"`
			Audits map[string]struct {
				NumericValue float64 `json:"numericValue"`
			} `json:"audits"`
		} `json:"lighthouseResult"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, nil, fmt.Errorf("pagespeed: parse: %w", err)
	}
	var field *types.CWVField
	for _, e := range []struct {
		exp   experience
		scope string
	}{{r.Origin, "origin"}, {r.Loading, "url"}} {
		f := &types.CWVField{FormFactor: "phone", Scope: e.scope}
		for _, k := range cwvMetricKeys {
			m, ok := e.exp.Metrics[k.psi]
			if !ok {
				continue
			}
			v := m.Percentile
			if k.id == "cls" {
				v /= 100 // PageSpeed reports CLS × 100
			}
			cm := types.CWVMetric{ID: k.id, P75: v}
			if len(m.Distributions) == 3 {
				cm.Good, cm.NI, cm.Poor = m.Distributions[0].Proportion, m.Distributions[1].Proportion, m.Distributions[2].Proportion
			}
			f.Metrics = append(f.Metrics, cm)
		}
		if len(f.Metrics) > 0 {
			field = f
			break
		}
	}
	var lab *types.CWVLab
	if s := r.LH.Categories.Performance.Score; s != nil {
		a := r.LH.Audits
		lab = &types.CWVLab{URL: pageURL, Score: *s * 100,
			LCP: a["largest-contentful-paint"].NumericValue, TBT: a["total-blocking-time"].NumericValue,
			CLS: a["cumulative-layout-shift"].NumericValue, FCP: a["first-contentful-paint"].NumericValue,
			SI: a["speed-index"].NumericValue, TTFB: a["server-response-time"].NumericValue}
	}
	return field, lab, nil
}

// setAPIKey sends a Google API key in the X-Goog-Api-Key header, never in
// the URL: URLs end up in error messages, logs and proxies.
func setAPIKey(req *http.Request, key string) {
	if key != "" {
		req.Header.Set("X-Goog-Api-Key", key)
	}
}

// scrubKey keeps the API key out of error messages, in case one ever
// quotes it.
func scrubKey(err error, key string) error {
	if err == nil || key == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(strings.ReplaceAll(err.Error(), url.QueryEscape(key), "***"), key, "***"))
}
