package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/alerts"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// Alert emails: after each dashboard crawl, and for scheduled runs that are
// skipped or fail. A crawl is compared with the previous completed crawl of
// the same schedule (or, without one, the same start URL); see
// alerts.ShouldEmail for when a message is sent.

// reportChannels are the destinations of the crawl report.
type reportChannels struct {
	email         alerts.EmailConfig
	telegramToken string
	telegramChat  string
	slackURL      string
}

func (c reportChannels) any() bool {
	return c.email.Enabled() || (c.telegramToken != "" && c.telegramChat != "") || c.slackURL != ""
}

// channelSettings reads every report channel, the "every crawl" option and
// the dashboard address used in links.
func (m *CrawlManager) channelSettings() (ch reportChannels, always bool, dashboardURL string) {
	settings, err := m.store.GetSettings()
	if err != nil {
		return
	}
	str := func(k string) string { s, _ := settings[k].(string); return strings.TrimSpace(s) }
	ch.email, always, dashboardURL = m.emailSettings()
	if a, ok := settings["alertAlways"].(bool); ok {
		always = a
	}
	ch.telegramToken = str("alertTelegramToken")
	if ch.telegramToken == "" {
		ch.telegramToken = os.Getenv("MICELIO_TELEGRAM_TOKEN")
	}
	ch.telegramChat = str("alertTelegramChatId")
	ch.slackURL = str("alertSlackUrl")
	return
}

// sendReport delivers one report to every configured channel and returns
// the channels that failed.
func sendReport(ch reportChannels, info alerts.RunInfo) (sent []string, failed map[string]error) {
	failed = map[string]error{}
	if ch.email.Enabled() {
		subject, text, html := alerts.RenderEmail(info)
		if err := alerts.SendEmail(ch.email, subject, text, html); err != nil {
			failed["email"] = err
		} else {
			sent = append(sent, "email")
		}
	}
	if ch.telegramToken != "" && ch.telegramChat != "" {
		if err := alerts.SendTelegram(ch.telegramToken, ch.telegramChat, alerts.RenderTelegram(info)); err != nil {
			failed["telegram"] = err
		} else {
			sent = append(sent, "telegram")
		}
	}
	if ch.slackURL != "" {
		if err := alerts.SendSlack(ch.slackURL, alerts.RenderSlack(info)); err != nil {
			failed["slack"] = err
		} else {
			sent = append(sent, "slack")
		}
	}
	return sent, failed
}

// emailSettings reads the SMTP account and options from Settings.
func (m *CrawlManager) emailSettings() (cfg alerts.EmailConfig, always bool, dashboardURL string) {
	settings, err := m.store.GetSettings()
	if err != nil {
		return
	}
	str := func(k string) string { s, _ := settings[k].(string); return strings.TrimSpace(s) }
	cfg = alerts.EmailConfig{
		Host:     str("alertEmailHost"),
		Username: str("alertEmailUser"),
		Password: str("alertEmailPassword"),
		From:     str("alertEmailFrom"),
		To:       alerts.ParseRecipients(str("alertEmailTo")),
		Security: str("alertEmailSecurity"),
	}
	if cfg.Password == "" {
		cfg.Password = os.Getenv("MICELIO_SMTP_PASSWORD")
	}
	if p, ok := settings["alertEmailPort"].(float64); ok {
		cfg.Port = int(p)
	}
	always, _ = settings["alertEmailAlways"].(bool)
	dashboardURL = strings.TrimRight(str("dashboardUrl"), "/")
	if dashboardURL == "" {
		dashboardURL = "http://127.0.0.1:3100"
	}
	return
}

// emailCrawlResult emails a finished crawl when it is worth reading. It runs
// in the background and never fails the crawl.
func (m *CrawlManager) emailCrawlResult(job *CrawlJob, stats types.CrawlStats, pages []*types.PageData) {
	ch, always, base := m.channelSettings()
	if !ch.any() || job == nil {
		return
	}
	urls := make([]string, len(pages))
	for i, p := range pages {
		urls[i] = p.URL
	}
	m.background.Add(1)
	go func() {
		defer m.background.Done()
		info := alerts.RunInfo{
			Name: scheduleName(job.Config), SeedURL: job.SeedURL, Status: "completed",
			Pages: stats.TotalPages, Errors: errorResponses(stats.StatusCodes), DurationMs: stats.CrawlDurationMs,
			Link: base + "/#/results/" + job.ID, Alerts: stats.AlertSummary,
		}
		if prev := m.previousCrawl(job); prev != nil {
			if prevStats := m.loadTypedStats(prev.ID); prevStats != nil {
				info.HasPrevious = true
				info.PrevAlerts = map[string]bool{}
				if prevStats.AlertSummary != nil {
					for _, a := range prevStats.AlertSummary.Alerts {
						info.PrevAlerts[a.ID] = true
					}
				}
				info.Changes = alerts.CompareCrawls(
					alerts.CrawlSnapshot{Stats: prevStats, URLs: crawlDBURLs(prev.DBPath)},
					alerts.CrawlSnapshot{Stats: &stats, URLs: urls},
				)
			}
		}
		if !alerts.ShouldEmail(info, always) {
			return
		}
		sent, failed := sendReport(ch, info)
		for name, err := range failed {
			log.Printf("[alerts] %s report for crawl %s: %v", name, job.ID, err)
			m.store.AddCrawlEvent(job.ID, "report_failed", name+": "+err.Error())
		}
		if len(sent) > 0 {
			m.store.AddCrawlEvent(job.ID, "report_sent", strings.Join(sent, ", "))
		}
	}()
}

// emailScheduleRun emails a scheduled run that did not produce a crawl to
// analyse (skipped or failed). Completed runs are emailed by the crawl.
func (m *CrawlManager) emailScheduleRun(sc scheduler.Config, res scheduler.RunResult) {
	if res.Status != "skipped" && res.Status != "failed" {
		return
	}
	ch, _, base := m.channelSettings()
	if !ch.any() {
		return
	}
	info := alerts.RunInfo{Name: sc.Name, SeedURL: sc.URL, Status: res.Status, Reason: res.Reason, Link: base + "/#/schedules"}
	_, failed := sendReport(ch, info)
	for name, err := range failed {
		log.Printf("[alerts] %s report for schedule %s: %v", name, sc.Name, err)
	}
}

// previousCrawl is the latest completed crawl before job with the same
// schedule name, or the same start URL when job is not scheduled.
func (m *CrawlManager) previousCrawl(job *CrawlJob) *CrawlJob {
	jobs, err := m.store.ListCrawlJobs() // newest first
	if err != nil {
		return nil
	}
	name := scheduleName(job.Config)
	for _, j := range jobs {
		if j.ID == job.ID || j.Status != "completed" || j.StartedAt >= job.StartedAt {
			continue
		}
		if name != "" {
			if scheduleName(j.Config) == name {
				return j
			}
			continue
		}
		if j.SeedURL == job.SeedURL && scheduleName(j.Config) == "" {
			return j
		}
	}
	return nil
}

func (m *CrawlManager) loadTypedStats(crawlID string) *types.CrawlStats {
	raw, err := m.store.LoadCrawlStats(crawlID)
	if err != nil || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var st types.CrawlStats
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func crawlDBURLs(dbPath string) []string {
	if dbPath == "" {
		return nil
	}
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()
	urls, _ := cs.GetVisitedURLs()
	return urls
}

// scheduleName is the schedule a crawl job ran for ("" if none).
func scheduleName(config map[string]interface{}) string {
	sc, _ := config["schedule"].(map[string]interface{})
	if sc == nil {
		return ""
	}
	if n, _ := sc["scheduleName"].(string); n != "" {
		return n
	}
	u, _ := sc["scheduleUrl"].(string)
	c, _ := sc["scheduleCron"].(string)
	if u == "" {
		return ""
	}
	if host := hostOnly(u); host != "" {
		u = host
	}
	return u + " · " + c
}

func hostOnly(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

func errorResponses(codes map[int]int) int {
	n := 0
	for code, c := range codes {
		if code >= 400 {
			n += c
		}
	}
	return n
}

// registerEmailRoutes adds POST /api/alerts/email/test, which sends a sample
// email with the saved settings and reports the SMTP error, if any.
func registerEmailRoutes(mux *http.ServeMux, manager *CrawlManager) {
	sample := func(base string) alerts.RunInfo {
		return alerts.RunInfo{
			Name: "Test message", SeedURL: "https://example.com/", Status: "completed", Pages: 1200, Errors: 3,
			DurationMs: int64(25 * time.Minute / time.Millisecond), Link: base, HasPrevious: true,
			Alerts: &types.AlertSummary{Alerts: []types.AlertResult{{
				ID: "soft-404-high", Name: "Soft-404 Candidates", Severity: "warning",
				Message: "This is a sample alert: Micelio reports reach this channel",
			}}},
			Changes: []alerts.Change{{Label: "Indexable pages", Before: "91.0%", After: "88.5%", Important: true}},
		}
	}
	// Test Telegram or Slack with the saved settings.
	mux.HandleFunc("POST /api/alerts/{channel}/test", func(w http.ResponseWriter, r *http.Request) {
		ch, _, base := manager.channelSettings()
		info := sample(base)
		var err error
		switch r.PathValue("channel") {
		case "email":
			if !ch.email.Enabled() {
				jsonError(w, "set the SMTP host, sender and recipients first", http.StatusBadRequest)
				return
			}
			subject, text, html := alerts.RenderEmail(info)
			err = alerts.SendEmail(ch.email, subject, text, html)
		case "telegram":
			if ch.telegramToken == "" || ch.telegramChat == "" {
				jsonError(w, "set the Telegram bot token and chat ID first", http.StatusBadRequest)
				return
			}
			err = alerts.SendTelegram(ch.telegramToken, ch.telegramChat, alerts.RenderTelegram(info))
		case "slack":
			if ch.slackURL == "" {
				jsonError(w, "set the Slack webhook URL first", http.StatusBadRequest)
				return
			}
			err = alerts.SendSlack(ch.slackURL, alerts.RenderSlack(info))
		default:
			jsonError(w, "unknown channel", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		jsonOK(w, map[string]interface{}{"sent": true, "to": ch.email.To})
	})

	// Chats that recently wrote to the bot, to find the chat ID.
	mux.HandleFunc("POST /api/alerts/telegram/chats", func(w http.ResponseWriter, r *http.Request) {
		ch, _, _ := manager.channelSettings()
		chats, err := alerts.TelegramChats(ch.telegramToken)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		jsonOK(w, map[string]interface{}{"chats": chats})
	})
}
