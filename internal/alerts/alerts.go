// Package alerts evaluates built-in alert rules against crawl stats.
package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// NotifyConfig holds notification channel configuration.
type NotifyConfig struct {
	WebhookURL string
	SlackURL   string
}

// Evaluate runs all built-in alert rules against the given crawl stats.
func Evaluate(stats *types.CrawlStats) *types.AlertSummary {
	if stats == nil {
		return nil
	}

	summary := &types.AlertSummary{
		Alerts:    []types.AlertResult{},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	for _, check := range builtinRules {
		if result := check(stats); result != nil {
			summary.Alerts = append(summary.Alerts, *result)
			switch result.Severity {
			case "critical":
				summary.Critical++
			case "warning":
				summary.Warnings++
			case "info":
				summary.Info++
			}
		}
	}

	if len(summary.Alerts) == 0 {
		return nil
	}
	return summary
}

type ruleFunc func(stats *types.CrawlStats) *types.AlertResult

var builtinRules = []ruleFunc{
	checkHighErrorRate,
	checkHigh5xxRate,
	checkLowIndexability,
	checkSlowResponseTime,
	checkDuplicateTitles,
	checkDuplicateDescriptions,
	checkMissingTitles,
	checkMissingDescriptions,
	checkOrphanPages,
	checkLowActiveRate,
}

func checkHighErrorRate(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.StatusCodes == nil {
		return nil
	}
	var errorCount int
	for code, count := range stats.StatusCodes {
		if code >= 400 {
			errorCount += count
		}
	}
	rate := float64(errorCount) / float64(stats.TotalPages) * 100
	if rate > 10 {
		return &types.AlertResult{
			ID: "high-error-rate", Name: "High Error Rate", Severity: "critical",
			Metric: "errorRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages returned errors (%d/%d)", rate, errorCount, stats.TotalPages),
		}
	}
	return nil
}

func checkHigh5xxRate(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.StatusCodes == nil {
		return nil
	}
	var count5xx int
	for code, count := range stats.StatusCodes {
		if code >= 500 && code < 600 {
			count5xx += count
		}
	}
	rate := float64(count5xx) / float64(stats.TotalPages) * 100
	if rate > 2 {
		return &types.AlertResult{
			ID: "high-5xx-rate", Name: "High Server Error Rate", Severity: "critical",
			Metric: "5xxRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages returned server errors (%d pages)", rate, count5xx),
		}
	}
	return nil
}

func checkLowIndexability(stats *types.CrawlStats) *types.AlertResult {
	if stats.SeoFunnelStats == nil || stats.SeoFunnelStats.Crawled == 0 {
		return nil
	}
	pct := stats.SeoFunnelStats.PctIndexable
	if pct < 70 {
		return &types.AlertResult{
			ID: "low-indexability", Name: "Low Indexability Rate", Severity: "warning",
			Metric: "indexabilityRate", Value: pct,
			Message: fmt.Sprintf("Only %.1f%% of crawled URLs are indexable (%d/%d)", pct, stats.SeoFunnelStats.Indexable, stats.SeoFunnelStats.Crawled),
		}
	}
	return nil
}

func checkSlowResponseTime(stats *types.CrawlStats) *types.AlertResult {
	p90 := stats.ResponseTimePercentiles.P90
	if p90 == 0 {
		return nil
	}
	if p90 > 3000 {
		return &types.AlertResult{
			ID: "slow-response-time", Name: "Slow P90 Response Time", Severity: "warning",
			Metric: "p90ResponseTimeMs", Value: p90,
			Message: fmt.Sprintf("P90 response time is %.0fms (threshold: 3000ms)", p90),
		}
	}
	return nil
}

func checkDuplicateTitles(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.DuplicateTitleCount == 0 {
		return nil
	}
	rate := float64(stats.DuplicateTitleCount) / float64(stats.TotalPages) * 100
	if rate > 10 {
		return &types.AlertResult{
			ID: "duplicate-titles", Name: "High Duplicate Title Rate", Severity: "warning",
			Metric: "duplicateTitleRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages have duplicate titles (%d pages)", rate, stats.DuplicateTitleCount),
		}
	}
	return nil
}

func checkDuplicateDescriptions(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.DuplicateDescriptionCount == 0 {
		return nil
	}
	rate := float64(stats.DuplicateDescriptionCount) / float64(stats.TotalPages) * 100
	if rate > 10 {
		return &types.AlertResult{
			ID: "duplicate-descriptions", Name: "High Duplicate Description Rate", Severity: "warning",
			Metric: "duplicateDescriptionRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages have duplicate descriptions (%d pages)", rate, stats.DuplicateDescriptionCount),
		}
	}
	return nil
}

func checkMissingTitles(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.PagesWithoutTitle == 0 {
		return nil
	}
	rate := float64(stats.PagesWithoutTitle) / float64(stats.TotalPages) * 100
	if rate > 5 {
		return &types.AlertResult{
			ID: "missing-titles", Name: "Missing Page Titles", Severity: "warning",
			Metric: "missingTitleRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages have no title tag (%d pages)", rate, stats.PagesWithoutTitle),
		}
	}
	return nil
}

func checkMissingDescriptions(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || stats.PagesWithoutDescription == 0 {
		return nil
	}
	rate := float64(stats.PagesWithoutDescription) / float64(stats.TotalPages) * 100
	if rate > 10 {
		return &types.AlertResult{
			ID: "missing-descriptions", Name: "Missing Meta Descriptions", Severity: "info",
			Metric: "missingDescriptionRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages have no meta description (%d pages)", rate, stats.PagesWithoutDescription),
		}
	}
	return nil
}

func checkOrphanPages(stats *types.CrawlStats) *types.AlertResult {
	if stats.TotalPages == 0 || len(stats.OrphanPages) == 0 {
		return nil
	}
	orphans := len(stats.OrphanPages)
	rate := float64(orphans) / float64(stats.TotalPages) * 100
	if rate > 5 {
		return &types.AlertResult{
			ID: "orphan-pages", Name: "Orphan Pages Detected", Severity: "warning",
			Metric: "orphanPageRate", Value: rate,
			Message: fmt.Sprintf("%.1f%% of pages have no internal links pointing to them (%d pages)", rate, orphans),
		}
	}
	return nil
}

func checkLowActiveRate(stats *types.CrawlStats) *types.AlertResult {
	// Only fire when analytics integrations are connected (GSC/GA4/Plausible)
	hasAnalytics := stats.GscStats != nil || stats.Ga4Stats != nil || stats.PlausibleStats != nil
	if !hasAnalytics || stats.SeoFunnelStats == nil {
		return nil
	}
	pct := stats.SeoFunnelStats.PctActive
	if pct < 5 {
		return &types.AlertResult{
			ID: "low-active-rate", Name: "Low Active URL Rate", Severity: "info",
			Metric: "activeUrlRate", Value: pct,
			Message: fmt.Sprintf("Only %.1f%% of crawled URLs receive organic traffic", pct),
		}
	}
	return nil
}

// Notify sends alert notifications to configured channels.
func Notify(cfg NotifyConfig, summary *types.AlertSummary, seedURL string) {
	if summary == nil || len(summary.Alerts) == 0 {
		return
	}
	if cfg.WebhookURL != "" {
		if err := validateWebhookURL(cfg.WebhookURL); err != nil {
			log.Printf("[alerts] webhook URL rejected: %v", err)
		} else {
			sendWebhook(cfg.WebhookURL, summary, seedURL)
		}
	}
	if cfg.SlackURL != "" {
		if err := validateSlackURL(cfg.SlackURL); err != nil {
			log.Printf("[alerts] slack URL rejected: %v", err)
		} else {
			sendSlack(cfg.SlackURL, summary, seedURL)
		}
	}
}

// validateWebhookURL checks that the URL is safe to send requests to.
func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if isPrivateHost(host) {
		return fmt.Errorf("private/loopback addresses not allowed: %s", host)
	}
	return nil
}

// validateSlackURL validates a Slack incoming webhook URL.
func validateSlackURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("slack webhooks must use HTTPS")
	}
	if !strings.HasSuffix(u.Hostname(), "slack.com") {
		return fmt.Errorf("slack webhook must be on slack.com domain")
	}
	return nil
}

// isPrivateHost returns true if the host resolves to a private/loopback address.
func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Try resolving hostname
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = ips[0]
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func sendWebhook(target string, summary *types.AlertSummary, seedURL string) {
	payload := map[string]interface{}{
		"event":     "crawl_alerts",
		"url":       seedURL,
		"timestamp": summary.Timestamp,
		"critical":  summary.Critical,
		"warnings":  summary.Warnings,
		"info":      summary.Info,
		"alerts":    summary.Alerts,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[alerts] webhook marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[alerts] webhook send error: %v", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	log.Printf("[alerts] webhook sent (%d alerts) → HTTP %d", len(summary.Alerts), resp.StatusCode)
}

func sendSlack(webhookURL string, summary *types.AlertSummary, seedURL string) {
	icon := ":white_check_mark:"
	if summary.Critical > 0 {
		icon = ":rotating_light:"
	} else if summary.Warnings > 0 {
		icon = ":warning:"
	}

	text := fmt.Sprintf("%s *Micelio Alert* — %s\n", icon, seedURL)
	text += fmt.Sprintf("_%d critical, %d warnings, %d info_\n\n", summary.Critical, summary.Warnings, summary.Info)

	for _, a := range summary.Alerts {
		severityIcon := ":information_source:"
		if a.Severity == "critical" {
			severityIcon = ":red_circle:"
		} else if a.Severity == "warning" {
			severityIcon = ":large_orange_circle:"
		}
		text += fmt.Sprintf("%s %s\n", severityIcon, a.Message)
	}

	payload := map[string]string{"text": text}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[alerts] slack marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[alerts] slack send error: %v", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	log.Printf("[alerts] slack sent (%d alerts) → HTTP %d", len(summary.Alerts), resp.StatusCode)
}
