package server

import (
	"net/url"
	"strings"

	"github.com/micelio/micelio/internal/scheduler"
)

// secretMask replaces a secret in API responses (same placeholder as the
// settings masking). A config sent back with it keeps the stored secret.
const secretMask = "••••••••"

// sensitiveConfigKey reports whether a config key or header name holds a
// credential: cookies, Authorization and API-key headers, API keys, key
// files, passwords, tokens and webhook URLs (their path is the secret).
func sensitiveConfigKey(k string) bool {
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

// redactURLUserinfo masks the password of a URL like a proxy
// "http://user:pass@host"; other strings are returned unchanged.
func redactURLUserinfo(s string) string {
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
	return strings.Replace(u.String(), "MICELIOMASK", secretMask, 1)
}

// redactValue returns v with its secrets masked. sensitive says the value
// sits under a credential key, so every non-empty string in it is masked.
func redactValue(v any, sensitive bool) any {
	switch t := v.(type) {
	case string:
		if sensitive && t != "" {
			return secretMask
		}
		return redactURLUserinfo(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = redactValue(x, sensitive || sensitiveConfigKey(k))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = redactValue(x, sensitive)
		}
		return out
	}
	return v
}

// redactConfig returns a copy of a crawl config map with credentials masked,
// for API responses. The stored config is not changed.
func redactConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	return redactValue(cfg, false).(map[string]any)
}

// dropConfigSecrets removes credentials from cfg in place (imported
// configs, which never need them): credential keys are deleted at any depth
// and URL passwords are masked.
func dropConfigSecrets(cfg map[string]any) {
	for k, v := range cfg {
		if sensitiveConfigKey(k) {
			delete(cfg, k)
			continue
		}
		switch t := v.(type) {
		case map[string]any:
			dropConfigSecrets(t)
		case []any:
			for i, x := range t {
				if m, ok := x.(map[string]any); ok {
					dropConfigSecrets(m)
				} else {
					t[i] = redactValue(x, false)
				}
			}
		default:
			cfg[k] = redactValue(v, false)
		}
	}
}

// maskError reports a masked secret the server cannot put back: the client
// edited it around the mask or there is no stored value. The request is
// refused (400) so the crawl never runs without it, or with the mask itself
// as a credential.
type maskError struct{ field string }

func (e *maskError) Error() string {
	switch e.field {
	case "seedUrl":
		return "the start URL contains a masked password (" + secretMask + "); type it again"
	case "proxy":
		return "the proxy URL contains a masked password (" + secretMask + ") that cannot be restored; re-enter the proxy password"
	}
	return e.field + " contains a masked secret (" + secretMask + ") that cannot be restored; re-enter it"
}

// restoreConfigSecrets puts back the stored secrets a client sent masked
// (a config it read from the API, e.g. Recrawl or Resume with edits). A
// masked value whose stored counterpart masks to the same text gets the
// stored value; a proxy URL whose masked password belongs to the same user
// gets the stored password even if host or port changed; cookies sent as
// "name=" + mask get the stored cookie of that name. Any other mask is an
// error naming the field. orig may be nil.
func restoreConfigSecrets(in, orig map[string]any) error {
	return restoreMap(in, orig, false, "")
}

func restoreMap(in, orig map[string]any, sensitive bool, path string) error {
	for k, v := range in {
		o, ok := orig[k]
		if !ok {
			// Header names may come back with other casing.
			for ok2, ov := range orig {
				if strings.EqualFold(ok2, k) {
					o = ov
					break
				}
			}
		}
		field := k
		if path != "" {
			field = path + "." + k
		}
		r, err := restoreValue(v, o, sensitive || sensitiveConfigKey(k), field)
		if err != nil {
			return err
		}
		in[k] = r
	}
	return nil
}

func restoreValue(v, orig any, sensitive bool, field string) (any, error) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, secretMask) {
			return t, nil
		}
		if ov, ok := orig.(string); ok && ov != "" && !strings.Contains(ov, secretMask) {
			if redactValue(ov, sensitive) == t {
				return ov, nil
			}
			if r, ok := restoreURLUserinfo(t, ov); ok {
				return r, nil
			}
			if k := field[strings.LastIndex(field, ".")+1:]; strings.EqualFold(k, "cookie") || strings.EqualFold(k, "cookies") {
				if r, ok := restoreCookies(t, ov); ok {
					return r, nil
				}
			}
		}
		return nil, &maskError{field: field}
	case map[string]any:
		om, _ := orig.(map[string]any)
		return t, restoreMap(t, om, sensitive, field)
	case []any:
		ol, _ := orig.([]any)
		for i, x := range t {
			var ox any
			if i < len(ol) {
				ox = ol[i]
			}
			r, err := restoreValue(x, ox, sensitive, field)
			if err != nil {
				return nil, err
			}
			t[i] = r
		}
		return t, nil
	}
	return v, nil
}

// restoreURLUserinfo puts the stored URL password back into an edited URL
// (e.g. a seed URL with a new path) when the password is the mask and the
// user name is unchanged, or the stored user-only credential when the user
// name is the mask. The host and port must be unchanged: otherwise anyone
// with API access could aim the stored password at a server they control.
func restoreURLUserinfo(s, stored string) (string, bool) {
	const ph = "MICELIOMASK"
	if !strings.Contains(s, "://") || !strings.Contains(s, "@") {
		return "", false
	}
	u, err := url.Parse(strings.ReplaceAll(s, secretMask, ph))
	if err != nil || u.User == nil {
		return "", false
	}
	o, err := url.Parse(stored)
	if err != nil || o.User == nil || !strings.EqualFold(u.Host, o.Host) {
		return "", false
	}
	p, hasP := u.User.Password()
	op, hasOP := o.User.Password()
	switch {
	case hasP && p == ph && hasOP && u.User.Username() == o.User.Username():
		u.User = url.UserPassword(o.User.Username(), op)
	case !hasP && u.User.Username() == ph && !hasOP:
		u.User = o.User
	default:
		return "", false
	}
	out := u.String()
	if strings.Contains(out, ph) {
		return "", false
	}
	return out, true
}

// restoreCookies restores a cookie string edited around the mask: a part
// that is the mask alone stands for the whole stored string (cookies are
// masked as one value), "name=" + mask gets the stored cookie of that name.
func restoreCookies(s, stored string) (string, bool) {
	byName := map[string]string{}
	for _, c := range strings.Split(stored, ";") {
		c = strings.TrimSpace(c)
		if name, _, ok := strings.Cut(c, "="); ok {
			byName[strings.TrimSpace(name)] = c
		}
	}
	var out []string
	for _, c := range strings.Split(s, ";") {
		c = strings.TrimSpace(c)
		switch {
		case c == "":
			continue
		case c == secretMask:
			c = strings.TrimSpace(stored)
		case strings.Contains(c, secretMask):
			name, val, ok := strings.Cut(c, "=")
			sc, found := byName[strings.TrimSpace(name)]
			if !ok || strings.TrimSpace(val) != secretMask || !found {
				return "", false
			}
			c = sc
		}
		out = append(out, c)
	}
	return strings.Join(out, "; "), true
}

// restoreFromCrawl fills the masked secrets of a start request from the
// crawl named in its "fromCrawl" field (Recrawl sends the config it read
// from the API). A mask it cannot fill is an error (HTTP 400).
func restoreFromCrawl(store *UiStore, config map[string]any) error {
	var orig map[string]any
	if id, _ := config["fromCrawl"].(string); id != "" {
		if job, err := store.GetCrawlJob(id); err == nil && job != nil {
			orig = job.Config
		}
	}
	delete(config, "fromCrawl")
	return restoreConfigSecrets(config, orig)
}

// redactedJob returns a copy of job safe to send to API clients.
func redactedJob(job *CrawlJob) *CrawlJob {
	if job == nil {
		return nil
	}
	c := *job
	c.Config = redactConfig(job.Config)
	c.SeedURL = redactURLUserinfo(job.SeedURL)
	return &c
}

func redactedJobs(jobs []*CrawlJob) []*CrawlJob {
	out := make([]*CrawlJob, len(jobs))
	for i, j := range jobs {
		out[i] = redactedJob(j)
	}
	return out
}

func redactedPreset(p *SavedPreset) *SavedPreset {
	if p == nil {
		return nil
	}
	c := *p
	c.Config = redactConfig(p.Config)
	return &c
}

// redactedSchedule returns a schedule state safe to send to API clients:
// crawl config credentials, the webhook URL (its path or query is the
// secret), the CLI precheck command and its output are masked.
func redactedSchedule(s scheduler.State) scheduler.State {
	s.CrawlConfig = redactConfig(s.CrawlConfig)
	if s.Webhook != "" {
		if u, err := url.Parse(s.Webhook); err == nil && u.Host != "" {
			s.Webhook = u.Scheme + "://" + u.Host + "/" + secretMask
		} else {
			s.Webhook = secretMask
		}
	}
	if s.Precheck != nil && s.Precheck.Command != "" {
		pc := *s.Precheck
		pc.Command = secretMask
		s.Precheck = &pc
		if s.LastCheck != nil && s.LastCheck.Reason != "" {
			lc := *s.LastCheck
			lc.Reason = "precheck command failed"
			s.LastCheck = &lc
		}
	}
	return s
}
