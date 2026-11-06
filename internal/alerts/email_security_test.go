package alerts

import (
	"strings"
	"testing"
)

// An unknown security setting (a typo such as "startls", or "plain") must
// not fall back to a plain connection that sends the password in clear.
func TestSendEmailRejectsUnknownSecurity(t *testing.T) {
	for _, sec := range []string{"startls", "plain", "SSL3", "yes"} {
		srv := startFakeSMTP(t, false)
		cfg := EmailConfig{Host: "127.0.0.1", Port: srv.port(), Username: "u", Password: "p",
			From: "m@example.com", To: []string{"a@example.com"}, Security: sec}
		err := SendEmail(cfg, "s", "t", "")
		if err == nil || !strings.Contains(err.Error(), "unknown SMTP security") {
			t.Errorf("%q: err = %v, want an unknown-security error", sec, err)
		}
		srv.mu.Lock()
		if srv.auth != "" {
			t.Errorf("%q: the password was sent over a plain connection", sec)
		}
		srv.mu.Unlock()
	}
}

func TestNormalizeSecurity(t *testing.T) {
	for in, want := range map[string]string{
		"": "starttls", "starttls": "starttls", " STARTTLS ": "starttls",
		"tls": "tls", "ssl": "tls", "SSL": "tls", "none": "none",
	} {
		if got, err := NormalizeSecurity(in); err != nil || got != want {
			t.Errorf("NormalizeSecurity(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeSecurity("starttsl"); err == nil {
		t.Error("typo accepted")
	}
}
