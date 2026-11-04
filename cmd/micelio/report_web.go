package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/integration"
	"github.com/micelio/micelio/internal/netguard"
	"github.com/micelio/micelio/internal/report"
	"github.com/micelio/micelio/internal/types"
)

// reportWebChecks fetches Core Web Vitals (CrUX / PageSpeed key from the
// flags or MICELIO_CRUX_KEY / MICELIO_PSI_KEY) and probes the CDN for the
// PDF report and backlog.
func reportWebChecks(config types.CrawlConfig, seedURL string) report.PDFOptions {
	cruxKey, psiKey := config.CrUXKey, config.PSIKey
	if cruxKey == "" {
		cruxKey = os.Getenv("MICELIO_CRUX_KEY")
	}
	if psiKey == "" {
		psiKey = os.Getenv("MICELIO_PSI_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	client := netguard.HTTPClient(60 * time.Second)
	opts := report.PDFOptions{
		CWV: integration.FetchCWV(ctx, client, cruxKey, psiKey, seedURL),
		CDN: crawler.ProbeCDN(ctx, client, seedURL),
	}
	if opts.CWV.Missing == "no-key" {
		fmt.Fprintf(os.Stderr, "  [info] Core Web Vitals need a Google API key: --crux-key / --psi-key or MICELIO_CRUX_KEY\n")
	}
	return opts
}
