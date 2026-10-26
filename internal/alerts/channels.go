package alerts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// The crawl report (RunInfo) goes to every configured channel: email,
// Telegram and Slack. Each gets the same content in its own format.

// Insights returns up to five one-line takeaways, most important first.
func Insights(r RunInfo) []string {
	var out []string
	switch r.Status {
	case "skipped", "failed", "stopped":
		out = append(out, fmt.Sprintf("The run was %s: %s.", r.Status, r.Reason))
	}
	if r.Alerts != nil {
		for _, a := range r.Alerts.Alerts {
			if a.Severity == "critical" {
				out = append(out, a.Message+".")
			}
		}
	}
	for _, c := range r.Changes {
		if !c.Important {
			continue
		}
		switch c.Label {
		case "New soft-404 candidates":
			out = append(out, fmt.Sprintf("%d new pages look like soft 404s.", len(c.Examples)+c.More))
		case "Indexable pages":
			out = append(out, fmt.Sprintf("Indexable pages went from %s to %s.", c.Before, c.After))
		case "Error responses (4xx/5xx)":
			out = append(out, fmt.Sprintf("Error responses went from %s to %s.", c.Before, c.After))
		case "Server errors (5xx)":
			out = append(out, fmt.Sprintf("Server errors (5xx) went from %s to %s.", c.Before, c.After))
		case "Pages crawled":
			out = append(out, fmt.Sprintf("Pages crawled went from %s to %s.", c.Before, c.After))
		case "URLs crawled last time but not this time":
			out = append(out, fmt.Sprintf("%s URLs from the last crawl were not found this time.", c.After))
		}
	}
	newSoft404 := false
	for _, c := range r.Changes {
		newSoft404 = newSoft404 || (c.Important && c.Label == "New soft-404 candidates")
	}
	if r.Alerts != nil {
		for _, a := range r.Alerts.Alerts {
			if a.ID == "soft-404-high" && newSoft404 {
				continue // already said by the soft-404 change
			}
			if a.Severity == "warning" && r.HasPrevious && !r.PrevAlerts[a.ID] {
				out = append(out, "New: "+a.Message+".")
			}
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// ── Telegram ──

// telegramAPI is the Bot API base; tests point it at a fake server.
var telegramAPI = "https://api.telegram.org"

// telegramMaxLen is Telegram's message limit.
const telegramMaxLen = 4096

// RenderTelegram builds a Telegram message (HTML parse mode). Long reports are
// cut and keep the link to the crawl.
func RenderTelegram(r RunInfo) string {
	esc := html.EscapeString
	subject, _, _ := RenderEmail(r)
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>\n", esc(strings.TrimPrefix(subject, "[Micelio] ")))
	if r.Status == "completed" {
		line := fmt.Sprintf("%d pages, %d errors", r.Pages, r.Errors)
		if r.DurationMs > 0 {
			line += ", " + fmtDuration(r.DurationMs)
		}
		fmt.Fprintf(&b, "%s\n", esc(line))
	}
	if ins := Insights(r); len(ins) > 0 {
		b.WriteString("\n<b>Key points</b>\n")
		for _, s := range ins {
			fmt.Fprintf(&b, "• %s\n", esc(s))
		}
	}
	if r.Alerts != nil && len(r.Alerts.Alerts) > 0 {
		b.WriteString("\n<b>Alerts</b>\n")
		for _, a := range r.Alerts.Alerts {
			fmt.Fprintf(&b, "%s %s\n", severityEmoji(a.Severity), esc(a.Message))
		}
	}
	if len(r.Changes) > 0 {
		b.WriteString("\n<b>Since the previous crawl</b>\n")
		for _, c := range r.Changes {
			if c.Before == "" {
				fmt.Fprintf(&b, "• %s: %s\n", esc(c.Label), esc(c.After))
			} else {
				fmt.Fprintf(&b, "• %s: %s → %s\n", esc(c.Label), esc(c.Before), esc(c.After))
			}
			for i, u := range c.Examples {
				if i == 3 {
					break
				}
				fmt.Fprintf(&b, "   %s\n", esc(u))
			}
			if rest := len(c.Examples) - 3 + c.More; rest > 0 && len(c.Examples) > 0 {
				fmt.Fprintf(&b, "   … and %d more\n", rest)
			}
		}
	}
	tail := ""
	if r.Link != "" {
		tail = fmt.Sprintf("\n<a href=\"%s\">Open the crawl</a>", esc(r.Link))
	}
	return truncateMessage(b.String(), tail, telegramMaxLen)
}

// SendTelegram posts a message to a chat, group or channel.
func SendTelegram(token, chatID, text string) error {
	if token == "" || chatID == "" {
		return errors.New("telegram needs a bot token and a chat ID")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	})
	return postJSON(telegramAPI+"/bot"+token+"/sendMessage", body, token)
}

// TelegramChat is a chat that recently wrote to the bot.
type TelegramChat struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type tgChat struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	Type      string `json:"type"`
}

type tgMessage struct {
	Chat tgChat `json:"chat"`
}

// TelegramChats lists chats from the bot's recent updates, so users can find
// their chat ID after sending the bot a message (or adding it to a group).
func TelegramChats(token string) ([]TelegramChat, error) {
	if token == "" {
		return nil, errors.New("telegram needs a bot token")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(telegramAPI + "/bot" + token + "/getUpdates")
	if err != nil {
		return nil, scrub(err, token)
	}
	defer resp.Body.Close()
	var data struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			Message      *tgMessage `json:"message"`
			ChannelPost  *tgMessage `json:"channel_post"`
			MyChatMember *tgMessage `json:"my_chat_member"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}
	if !data.OK {
		return nil, fmt.Errorf("telegram: %s", data.Description)
	}
	seen := map[int64]bool{}
	var out []TelegramChat
	for _, u := range data.Result {
		for _, m := range []*tgMessage{u.Message, u.ChannelPost, u.MyChatMember} {
			if m == nil || seen[m.Chat.ID] {
				continue
			}
			seen[m.Chat.ID] = true
			title := m.Chat.Title
			if title == "" {
				title = strings.TrimSpace(m.Chat.FirstName + " @" + m.Chat.Username)
			}
			out = append(out, TelegramChat{ID: m.Chat.ID, Title: title, Type: m.Chat.Type})
		}
	}
	return out, nil
}

// ── Slack ──

// RenderSlack builds a Slack message (mrkdwn) with the same report.
func RenderSlack(r RunInfo) string {
	subject, _, _ := RenderEmail(r)
	esc := slackEscape
	var b strings.Builder
	icon := ":white_check_mark:"
	if r.Status != "completed" {
		icon = ":warning:"
	}
	if r.Alerts != nil && r.Alerts.Critical > 0 {
		icon = ":rotating_light:"
	}
	fmt.Fprintf(&b, "%s *%s*\n", icon, esc(strings.TrimPrefix(subject, "[Micelio] ")))
	if r.Status == "completed" {
		line := fmt.Sprintf("%d pages, %d errors", r.Pages, r.Errors)
		if r.DurationMs > 0 {
			line += ", " + fmtDuration(r.DurationMs)
		}
		fmt.Fprintf(&b, "_%s_\n", esc(line))
	}
	if ins := Insights(r); len(ins) > 0 {
		b.WriteString("\n*Key points*\n")
		for _, s := range ins {
			fmt.Fprintf(&b, "• %s\n", esc(s))
		}
	}
	if r.Alerts != nil && len(r.Alerts.Alerts) > 0 {
		b.WriteString("\n*Alerts*\n")
		for _, a := range r.Alerts.Alerts {
			icon := ":information_source:"
			switch a.Severity {
			case "critical":
				icon = ":red_circle:"
			case "warning":
				icon = ":large_orange_circle:"
			}
			fmt.Fprintf(&b, "%s %s\n", icon, esc(a.Message))
		}
	}
	if len(r.Changes) > 0 {
		b.WriteString("\n*Since the previous crawl*\n")
		for _, c := range r.Changes {
			if c.Before == "" {
				fmt.Fprintf(&b, "• %s: %s\n", esc(c.Label), esc(c.After))
			} else {
				fmt.Fprintf(&b, "• %s: %s → %s\n", esc(c.Label), esc(c.Before), esc(c.After))
			}
			for i, u := range c.Examples {
				if i == 5 {
					break
				}
				fmt.Fprintf(&b, "    %s\n", esc(u))
			}
		}
	}
	if r.Link != "" {
		fmt.Fprintf(&b, "\n<%s|Open the crawl>", r.Link)
	}
	return b.String()
}

// SendSlack posts a message to a Slack incoming webhook.
func SendSlack(webhookURL, text string) error {
	if err := validateSlackURL(webhookURL); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	return postJSON(webhookURL, body, "")
}

// ── helpers ──

func postJSON(url string, body []byte, secret string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return scrub(err, secret)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// scrub removes a secret (the Telegram token is part of the URL) from errors.
func scrub(err error, secret string) error {
	if secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "<token>"))
}

// truncateMessage cuts body so body+tail fits max runes, at a line break.
func truncateMessage(body, tail string, max int) string {
	if len([]rune(body))+len([]rune(tail)) <= max {
		return body + tail
	}
	const cut = "\n…"
	r := []rune(body)
	limit := max - len([]rune(tail)) - len([]rune(cut))
	if limit < 0 {
		limit = 0
	}
	s := string(r[:limit])
	if i := strings.LastIndex(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s + cut + tail
}

func severityEmoji(s string) string {
	switch s {
	case "critical":
		return "🔴"
	case "warning":
		return "🟠"
	}
	return "ℹ️"
}

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
