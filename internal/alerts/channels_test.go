package alerts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func sampleRun() RunInfo {
	return RunInfo{
		Name: "shop · daily", SeedURL: "https://s.example/", Status: "completed", Pages: 1000, Errors: 40,
		Link: "http://127.0.0.1:3100/#/results/x", HasPrevious: true, PrevAlerts: map[string]bool{},
		Alerts: &types.AlertSummary{Critical: 1, Alerts: []types.AlertResult{
			{ID: "high-5xx-rate", Severity: "critical", Message: "6.0% of pages return 5xx"},
		}},
		Changes: []Change{
			{Label: "Indexable pages", Before: "95.0%", After: "90.0%", Important: true},
			{Label: "New soft-404 candidates", Before: "1", After: "13", Important: true,
				Examples: []string{"https://s.example/a", "https://s.example/b", "https://s.example/c", "https://s.example/d"}, More: 8},
		},
	}
}

func TestInsights(t *testing.T) {
	got := Insights(sampleRun())
	want := []string{
		"6.0% of pages return 5xx.",
		"Indexable pages went from 95.0% to 90.0%.",
		"12 new pages look like soft 404s.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("insights = %q", got)
	}
	skipped := Insights(RunInfo{Status: "skipped", Reason: "network check failed"})
	if len(skipped) != 1 || !strings.Contains(skipped[0], "skipped") {
		t.Errorf("skipped insights = %q", skipped)
	}
}

func TestTelegramSendAndRender(t *testing.T) {
	var got map[string]interface{}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	old := telegramAPI
	telegramAPI = srv.URL
	defer func() { telegramAPI = old }()

	text := RenderTelegram(sampleRun())
	if err := SendTelegram("123:SECRET", "-1001", text); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:SECRET/sendMessage" || got["chat_id"] != "-1001" || got["parse_mode"] != "HTML" {
		t.Errorf("request path=%s body=%v", path, got)
	}
	for _, want := range []string{"<b>shop · daily: 1 alert, +12 soft-404</b>", "Key points", "🔴", "95.0% → 90.0%", "… and 9 more", "Open the crawl</a>"} {
		if !strings.Contains(text, want) {
			t.Errorf("telegram text lacks %q:\n%s", want, text)
		}
	}
}

func TestTelegramErrorsHideToken(t *testing.T) {
	old := telegramAPI
	telegramAPI = "http://127.0.0.1:1"
	defer func() { telegramAPI = old }()
	err := SendTelegram("123:SECRET", "1", "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("err = %v (must not contain the token)", err)
	}
}

func TestTelegramLongReportIsCut(t *testing.T) {
	r := sampleRun()
	for i := 0; i < 400; i++ {
		r.Changes = append(r.Changes, Change{Label: strings.Repeat("x", 30), Before: "1", After: "2"})
	}
	text := RenderTelegram(r)
	if n := len([]rune(text)); n > telegramMaxLen {
		t.Errorf("message has %d runes, limit %d", n, telegramMaxLen)
	}
	if !strings.HasSuffix(text, "Open the crawl</a>") || !strings.Contains(text, "\n…") {
		t.Error("cut message should keep the link and mark the cut")
	}
}

func TestTelegramChats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[
			{"message":{"chat":{"id":42,"first_name":"Ana","username":"ana","type":"private"}}},
			{"message":{"chat":{"id":42,"first_name":"Ana","username":"ana","type":"private"}}},
			{"my_chat_member":{"chat":{"id":-1007,"title":"SEO alerts","type":"group"}}}]}`))
	}))
	defer srv.Close()
	old := telegramAPI
	telegramAPI = srv.URL
	defer func() { telegramAPI = old }()
	chats, err := TelegramChats("t")
	if err != nil || len(chats) != 2 || chats[1].Title != "SEO alerts" || chats[0].ID != 42 {
		t.Errorf("chats = %+v, %v", chats, err)
	}
}

func TestSlackRenderAndURLCheck(t *testing.T) {
	text := RenderSlack(sampleRun())
	for _, want := range []string{":rotating_light: *shop · daily", "*Key points*", "95.0% → 90.0%", "<http://127.0.0.1:3100/#/results/x|Open the crawl>"} {
		if !strings.Contains(text, want) {
			t.Errorf("slack text lacks %q:\n%s", want, text)
		}
	}
	if err := SendSlack("https://evil.example/hook", text); err == nil {
		t.Error("non-Slack webhook URL accepted")
	}
}
