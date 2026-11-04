// Package secrets finds and masks credentials in crawl configs (cookies,
// auth headers, API keys, passwords, tokens, webhook URLs, URL passwords).
// The dashboard API masks them in responses; crawl databases store a copy
// without them, so a shared or imported crawl file carries no credentials.
package secrets

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Mask replaces a secret (same placeholder as the settings masking).
const Mask = "••••••••"

// SensitiveKey reports whether a config key or header name holds a
// credential: cookies, Authorization and API-key headers, API keys, key
// files, passwords, tokens and webhook URLs (their path is the secret).
func SensitiveKey(k string) bool {
	l := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(k))
	switch {
	case l == "cookie" || l == "cookies" || l == "setcookie":
		return true
	case strings.HasSuffix(l, "key") || strings.HasSuffix(l, "keyfile") || strings.HasSuffix(l, "keys"):
		return true
	case strings.Contains(l, "authoriz"), strings.Contains(l, "auth") && !strings.Contains(l, "author"):
		return true
	}
	for _, s := range []string{"password", "passwd", "secret", "token", "credential", "session", "signature", "webhook", "slackurl"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// RedactURLUserinfo masks the password of a URL like a proxy
// "http://user:pass@host"; other strings are returned unchanged.
func RedactURLUserinfo(s string) string {
	if !strings.Contains(s, "@") || !strings.Contains(s, "://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "MICELIOMASK")
	} else {
		u.User = url.User("MICELIOMASK")
	}
	return strings.Replace(u.String(), "MICELIOMASK", Mask, 1)
}

// Redact returns v (a decoded JSON value) with its secrets masked.
// sensitive says the value sits under a credential key, so every non-empty
// string in it is masked.
func Redact(v any, sensitive bool) any {
	switch t := v.(type) {
	case string:
		if sensitive && t != "" {
			return Mask
		}
		return RedactURLUserinfo(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = Redact(x, sensitive || SensitiveKey(k))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Redact(x, sensitive)
		}
		return out
	}
	return v
}

// Drop removes credentials from cfg in place: credential keys are deleted
// at any depth and URL passwords are masked.
func Drop(cfg map[string]any) {
	for k, v := range cfg {
		if SensitiveKey(k) {
			delete(cfg, k)
			continue
		}
		switch t := v.(type) {
		case map[string]any:
			Drop(t)
		case []any:
			for i, x := range t {
				if m, ok := x.(map[string]any); ok {
					Drop(m)
				} else {
					t[i] = Redact(x, false)
				}
			}
		default:
			cfg[k] = Redact(v, false)
		}
	}
}

// ScrubJSON returns a JSON object (such as a marshalled crawl config)
// without its credentials (see Drop).
func ScrubJSON(data []byte) ([]byte, error) {
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	Drop(m)
	return json.Marshal(m)
}
