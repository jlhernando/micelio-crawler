package webhook

import (
	"bytes"
	"errors"
	"log"
	"net"
	"net/url"
	"strings"
	"testing"
)

// A failed delivery (connection refused, DNS, TLS, timeout) must not write
// the webhook URL's path or query to the log: they hold its token.
func TestSendFailureLogsNoToken(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close() // nothing listens: every attempt fails in client.Do

	hook := "http://" + addr + "/services/T00/B00/PATH-SECRET?token=QUERY-SECRET"
	if Send(Options{URL: hook, SkipValidation: true}, Payload{Event: "crawl_failed"}) {
		t.Fatal("expected failure")
	}
	out := buf.String()
	if !strings.Contains(out, "attempt 1 failed") {
		t.Fatalf("no failure logged: %q", out)
	}
	for _, s := range []string{"PATH-SECRET", "QUERY-SECRET"} {
		if strings.Contains(out, s) {
			t.Errorf("log holds %s: %q", s, out)
		}
	}
	// A URL that does not parse is not logged at all.
	buf.Reset()
	Send(Options{URL: "http://exa mple.com/hook?token=BAD-SECRET", SkipValidation: true}, Payload{})
	if strings.Contains(buf.String(), "BAD-SECRET") {
		t.Errorf("log holds the malformed URL: %q", buf.String())
	}
}

func TestRedactErrNestedURL(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://hooks.example.com/a/SECRET1",
		Err: errors.New(`redirect to "https://other.example.com/b/SECRET2" refused`)}
	got := redactErr(err)
	if strings.Contains(got, "SECRET") || !strings.Contains(got, "hooks.example.com") || !strings.Contains(got, "other.example.com") {
		t.Fatalf("redactErr = %q", got)
	}
}
