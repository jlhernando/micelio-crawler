// Package secrets finds and masks credentials in crawl configs (cookies,
// custom headers, API keys, passwords, tokens, webhook URLs, URL user info
// and credential query parameters). The dashboard API masks them in
// responses; crawl databases store a copy without them, so a shared or
// imported crawl file carries no credentials.
package secrets

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Mask replaces a secret (same placeholder as the settings masking).
const Mask = "••••••••"

// Placeholder stands for Mask while a URL is parsed or printed (the
// bullets would be percent-encoded).
const Placeholder = "MICELIOMASK"

// SensitiveKey reports whether a config key or header name holds a
// credential: cookies, Authorization and API-key headers, API keys, key
// files, passwords, tokens and webhook URLs (their path is the secret).
func SensitiveKey(k string) bool {
	l := normKey(k)
	switch {
	case l == "cookie" || l == "cookies" || l == "setcookie":
		return true
	case strings.HasSuffix(l, "key") || strings.HasSuffix(l, "keyfile") || strings.HasSuffix(l, "keys") || strings.HasSuffix(l, "keyid"):
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

func normKey(k string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(k)))
}

// HeaderMapKey reports whether a config key holds request headers
// (customHeaders). Every value in it is a secret unless SafeHeader says
// otherwise: credential headers have too many names to guess them
// (X-JFrog-Art-Api, X-Goog-IAP-JWT-Assertion, ...).
func HeaderMapKey(k string) bool {
	switch normKey(k) {
	case "customheaders", "headers", "extraheaders", "httpheaders", "requestheaders":
		return true
	}
	return false
}

// SafeHeader reports the request headers whose values are shown as they
// are: standard browser headers that never carry a credential.
func SafeHeader(name string) bool {
	l := strings.ToLower(strings.TrimSpace(name))
	switch l {
	case "user-agent", "accept", "accept-language", "accept-encoding", "cache-control",
		"pragma", "referer", "dnt", "upgrade-insecure-requests", "sec-gpc", "save-data":
		return true
	}
	return strings.HasPrefix(l, "sec-ch-") || strings.HasPrefix(l, "sec-fetch-")
}

// SensitiveAt reports whether the value of key is a credential, given the
// key of the map that holds it (parent, "" at the top level).
func SensitiveAt(parent, key string) bool {
	if HeaderMapKey(parent) {
		return !SafeHeader(key)
	}
	return SensitiveKey(key)
}

// SensitiveParam reports whether a URL query parameter carries a
// credential (access_token, api_key, X-Amz-Signature, sig, ...).
func SensitiveParam(name string) bool {
	if SensitiveKey(name) {
		return true
	}
	switch normKey(name) {
	case "sig", "pwd", "pass", "jwt", "hmac", "apikey", "accesskey", "otp":
		return true
	}
	return false
}

// RedactURLUserinfo masks the user info of a URL like a proxy
// "http://user:pass@host" (user name and password); other strings are
// returned unchanged.
func RedactURLUserinfo(s string) string {
	if !strings.Contains(s, "@") || !strings.Contains(s, "://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(Placeholder, Placeholder)
	} else {
		u.User = url.User(Placeholder)
	}
	return strings.ReplaceAll(u.String(), Placeholder, Mask)
}

// RedactURL masks the credentials of an absolute URL: its user info and
// the values of credential query (or fragment) parameters. A string with
// several lines (a URL list) is redacted line by line. Other strings are
// returned unchanged.
func RedactURL(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	if strings.Contains(s, "\n") {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = RedactURL(l)
		}
		return strings.Join(lines, "\n")
	}
	s = RedactURLUserinfo(s)
	if !strings.ContainsAny(s, "?#") {
		return s
	}
	return MapParams(s, func(name, _ string) (string, bool) {
		if SensitiveParam(name) {
			return Mask, true
		}
		return "", false
	})
}

// MapParams rewrites the query and fragment parameters of URL s: repl gets
// the decoded name and raw value of each one with a value and may return a
// new raw value. The rest of the URL is kept byte for byte.
func MapParams(s string, repl func(name, raw string) (string, bool)) string {
	rest, frag, hasFrag := strings.Cut(s, "#")
	base, query, hasQuery := strings.Cut(rest, "?")
	fix := func(part string) string {
		if !strings.Contains(part, "=") {
			return part
		}
		kv := strings.Split(part, "&")
		for i, p := range kv {
			name, val, ok := strings.Cut(p, "=")
			if !ok || val == "" {
				continue
			}
			n, err := url.QueryUnescape(name)
			if err != nil {
				n = name
			}
			if v, ok := repl(n, val); ok {
				kv[i] = name + "=" + v
			}
		}
		return strings.Join(kv, "&")
	}
	out := base
	if hasQuery {
		out += "?" + fix(query)
	}
	if hasFrag {
		out += "#" + fix(frag)
	}
	return out
}

// Redact returns v (a decoded JSON value) with its secrets masked.
// sensitive says the value sits under a credential key, so every non-empty
// string in it is masked.
func Redact(v any, sensitive bool) any {
	return redact(v, sensitive, "")
}

func redact(v any, sensitive bool, parent string) any {
	switch t := v.(type) {
	case string:
		if sensitive && t != "" {
			return Mask
		}
		return RedactURL(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = redact(x, sensitive || ChildSensitive(parent, k, x), k)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = redact(x, sensitive, parent)
		}
		return out
	}
	return v
}

// ChildSensitive reports whether the value v of key (in a map held under
// parent) is a credential as a whole: a credential key, a custom header
// not listed by SafeHeader, or headers in a form other than a name→value
// map.
func ChildSensitive(parent, key string, v any) bool {
	if SensitiveAt(parent, key) {
		return true
	}
	if HeaderMapKey(key) {
		_, isMap := v.(map[string]any)
		return !isMap
	}
	return false
}

// Drop removes credentials from cfg in place: credential keys (and every
// custom header not listed by SafeHeader) are deleted at any depth and URL
// credentials are masked.
func Drop(cfg map[string]any) {
	drop(cfg, "")
}

func drop(cfg map[string]any, parent string) {
	for k, v := range cfg {
		if ChildSensitive(parent, k, v) {
			delete(cfg, k)
			continue
		}
		switch t := v.(type) {
		case map[string]any:
			drop(t, k)
		case []any:
			for i, x := range t {
				if m, ok := x.(map[string]any); ok {
					drop(m, k)
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
