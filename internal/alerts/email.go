package alerts

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// EmailConfig is an SMTP account used to send alert emails.
type EmailConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	// Security is "starttls" (default, port 587), "tls" (implicit TLS, port
	// 465) or "none" (plain text; only for local relays).
	Security string
}

// Enabled reports whether enough is set to send.
func (c EmailConfig) Enabled() bool {
	return c.Host != "" && c.From != "" && len(c.To) > 0
}

// ParseRecipients splits a comma/semicolon/space separated address list.
func ParseRecipients(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// dialTimeout bounds every SMTP step; tests shorten it.
var dialTimeout = 20 * time.Second

// SendEmail sends one message with a plain-text and an HTML part.
func SendEmail(c EmailConfig, subject, text, html string) error {
	if !c.Enabled() {
		return errors.New("email is not configured (host, from and to are required)")
	}
	for _, a := range append([]string{c.From}, c.To...) {
		if strings.ContainsAny(a, "\r\n<>") || !strings.Contains(a, "@") {
			return fmt.Errorf("invalid address %q", a)
		}
	}
	security := strings.ToLower(c.Security)
	port := c.Port
	if port == 0 {
		port = 587
		if security == "tls" {
			port = 465
		}
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(port))
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: dialTimeout}
	if security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	conn.SetDeadline(time.Now().Add(3 * dialTimeout))
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer client.Close()

	if security == "" || security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("the server does not offer STARTTLS; use security \"tls\" or a local relay with \"none\"")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if c.Username != "" {
		if err := client.Auth(plainAuth{username: c.Username, password: c.Password}); err != nil {
			return fmt.Errorf("login: %w", err)
		}
	}
	if err := client.Mail(c.From); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	for _, to := range c.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("to %s: %w", to, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMessage(c.From, c.To, subject, text, html)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// plainAuth is SMTP AUTH PLAIN. Unlike net/smtp.PlainAuth it also works over
// security "none" (the caller chose that for a local relay); with STARTTLS
// or TLS the connection is already encrypted.
type plainAuth struct{ username, password string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

func buildMessage(from string, to []string, subject, text, html string) []byte {
	var b bytes.Buffer
	boundary := randomToken()
	host := "micelio.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		host = from[i+1:]
	}
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", from)
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+randomToken()+"@"+host+">")
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	part := func(contentType, body string) {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, contentType)
		qp := quotedprintable.NewWriter(&b)
		qp.Write([]byte(body))
		qp.Close()
		b.WriteString("\r\n")
	}
	part("text/plain", text)
	if html != "" {
		part("text/html", html)
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func randomToken() string {
	buf := make([]byte, 12)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
