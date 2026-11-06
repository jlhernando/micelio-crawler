package server

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/secrets"
)

// secretMask replaces a secret in API responses (same placeholder as the
// settings masking). A config sent back with it keeps the stored secret.
const secretMask = secrets.Mask

// Credential detection is shared with the crawl database (package secrets),
// which stores crawl configs without credentials.
var (
	redactURL   = secrets.RedactURL
	redactValue = secrets.Redact
	// dropConfigSecrets removes credentials from cfg in place (imported
	// configs, which never need them).
	dropConfigSecrets = secrets.Drop
)

// redactConfig returns a copy of a crawl config map with credentials masked,
// for API responses. The stored config is not changed.
func redactConfig(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}
	return redactValue(cfg, false).(map[string]any)
}

// maskError reports a masked secret the server cannot put back: the client
// edited it around the mask or there is no stored value. The request is
// refused (400) so the crawl never runs without it, or with the mask itself
// as a credential.
type maskError struct{ field string }

func (e *maskError) Error() string {
	switch e.field {
	case "seedUrl":
		return "the start URL contains a masked password or token (" + secretMask + "); type it again"
	case "proxy":
		return "the proxy URL contains a masked password (" + secretMask + ") that cannot be restored; re-enter the proxy password"
	}
	return e.field + " contains a masked secret (" + secretMask + ") that cannot be restored; re-enter it"
}

// scopeError reports a masked secret whose destination changed: the
// stored value was saved for another site or service, so it is not sent
// to the new one without being typed again.
type scopeError struct{ field, dest string }

func (e *scopeError) Error() string {
	return e.field + " (" + secretMask + ") was saved for " + e.dest +
		", which changed; re-enter " + e.field + " to use it with the new one"
}

// restoreConfigSecrets puts back the stored secrets a client sent masked
// (a config it read from the API, e.g. Recrawl or Resume with edits). A
// masked value whose stored counterpart masks to the same text gets the
// stored value; a URL whose masked user info or token parameters belong to
// the same scheme and host gets the stored ones; cookies sent as
// "name=" + mask get the stored cookie of that name. Any other mask is an
// error naming the field.
//
// A restored secret stays bound to where it was stored for: cookies and
// custom headers to the crawl's sites (start URL, URL list, sitemaps,
// allowed domains), a service key to its provider or host. If that
// destination changed, the request is refused and the secret must be typed
// again. orig may be nil.
func restoreConfigSecrets(in, orig map[string]any) error {
	r := &restorer{restored: map[string]bool{}}
	if err := r.restoreMap(in, orig, false, "", ""); err != nil {
		return err
	}
	return checkSecretScopes(in, orig, r.restored)
}

// restorer records which top-level fields got a stored secret back.
type restorer struct{ restored map[string]bool }

func (r *restorer) mark(field string) {
	top, _, _ := strings.Cut(field, ".")
	r.restored[top] = true
}

func (r *restorer) restoreMap(in, orig map[string]any, sensitive bool, path, parent string) error {
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
		sens := sensitive || secrets.ChildSensitive(parent, k, o) || secrets.ChildSensitive(parent, k, v)
		res, err := r.restoreValue(v, o, sens, field, k)
		if err != nil {
			return err
		}
		in[k] = res
	}
	return nil
}

func (r *restorer) restoreValue(v, orig any, sensitive bool, field, key string) (any, error) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, secretMask) {
			return t, nil
		}
		if ov, ok := orig.(string); ok && ov != "" && !strings.Contains(ov, secretMask) {
			if redactValue(ov, sensitive) == t {
				r.mark(field)
				return ov, nil
			}
			if res, ok := restoreURLs(t, ov); ok {
				r.mark(field)
				return res, nil
			}
			if k := field[strings.LastIndex(field, ".")+1:]; strings.EqualFold(k, "cookie") || strings.EqualFold(k, "cookies") {
				if res, ok := restoreCookies(t, ov); ok {
					r.mark(field)
					return res, nil
				}
			}
		}
		return nil, &maskError{field: field}
	case map[string]any:
		om, _ := orig.(map[string]any)
		return t, r.restoreMap(t, om, sensitive, field, key)
	case []any:
		ol, _ := orig.([]any)
		for i, x := range t {
			var ox any
			if i < len(ol) {
				ox = ol[i]
			}
			res, err := r.restoreValue(x, ox, sensitive, field, key)
			if err != nil {
				// A list edited around the entry (one removed or added
				// before it): any stored entry that fits.
				s, isStr := x.(string)
				if !isStr {
					return nil, err
				}
				found := false
				for _, cand := range ol {
					if c, ok := cand.(string); ok {
						if res2, err2 := r.restoreValue(s, c, sensitive, field, key); err2 == nil {
							res, found = res2, true
							break
						}
					}
				}
				if !found {
					return nil, err
				}
			}
			t[i] = res
		}
		return t, nil
	}
	return v, nil
}

// restoreURLs restores a URL, or a list of URLs one per line (urlList),
// edited around its masks (see restoreURL).
func restoreURLs(s, stored string) (string, bool) {
	if !strings.Contains(s, "\n") && !strings.Contains(stored, "\n") {
		return restoreURL(s, stored)
	}
	storedLines := strings.Split(stored, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if !strings.Contains(l, secretMask) {
			continue
		}
		ok := false
		if i < len(storedLines) {
			if strings.TrimSpace(secrets.RedactURL(storedLines[i])) == strings.TrimSpace(l) {
				lines[i], ok = storedLines[i], true
			} else {
				lines[i], ok = restoreURL(l, storedLines[i])
			}
		}
		for j := 0; !ok && j < len(storedLines); j++ {
			if strings.TrimSpace(secrets.RedactURL(storedLines[j])) == strings.TrimSpace(l) {
				lines[i], ok = storedLines[j], true
			} else {
				lines[i], ok = restoreURL(l, storedLines[j])
			}
		}
		if !ok {
			return "", false
		}
	}
	return strings.Join(lines, "\n"), true
}

// restoreURL puts the stored credentials back into an edited URL (e.g. a
// seed URL with a new path): a masked user name or password gets the
// stored one (a masked password only for the same user name), a masked
// query or fragment parameter gets the stored value of that parameter.
// The scheme, host and port must be unchanged: otherwise anyone with API
// access could aim the stored credentials at a server they control.
func restoreURL(s, stored string) (string, bool) {
	const ph = secrets.Placeholder
	if !strings.Contains(s, "://") || strings.Contains(stored, ph) {
		return "", false
	}
	sp := strings.ReplaceAll(s, secretMask, ph)
	u, err := url.Parse(sp)
	if err != nil || u.Host == "" {
		return "", false
	}
	o, err := url.Parse(stored)
	if err != nil || !strings.EqualFold(u.Scheme, o.Scheme) || !strings.EqualFold(u.Host, o.Host) {
		return "", false
	}
	if u.User != nil && strings.Contains(u.User.String(), ph) {
		if o.User == nil {
			return "", false
		}
		user, ouser := u.User.Username(), o.User.Username()
		p, hasP := u.User.Password()
		op, hasOP := o.User.Password()
		switch {
		case user == ph:
			user = ouser
		case strings.Contains(user, ph):
			return "", false
		}
		switch {
		case !hasP && hasOP:
			return "", false // the stored password must not be dropped silently
		case !hasP:
			u.User = url.User(user)
		case p == ph:
			if !hasOP || user != ouser {
				return "", false
			}
			u.User = url.UserPassword(user, op)
		case strings.Contains(p, ph):
			return "", false
		default:
			u.User = url.UserPassword(user, p)
		}
		sp = u.String()
	}
	if strings.Contains(sp, ph) {
		storedParams := map[string][]string{}
		secrets.MapParams(stored, func(name, raw string) (string, bool) {
			storedParams[name] = append(storedParams[name], raw)
			return "", false
		})
		seen := map[string]int{}
		failed := false
		sp = secrets.MapParams(sp, func(name, raw string) (string, bool) {
			n := seen[name]
			seen[name]++
			if !strings.Contains(raw, ph) {
				return "", false
			}
			vals := storedParams[name]
			if raw != ph || n >= len(vals) {
				failed = true
				return "", false
			}
			return vals[n], true
		})
		if failed {
			return "", false
		}
	}
	if strings.Contains(sp, ph) {
		return "", false
	}
	return sp, true
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

// URL fields restore per URL (restoreURL checks scheme and host); these
// secrets only go to fixed Google endpoints or are destinations themselves.
var unscopedSecretFields = map[string]bool{
	"seedUrl": true, "url": true, "urls": true, "urlList": true, "sitemapUrls": true, "proxy": true,
	"psiKey": true, "cruxKey": true, "gscKeyFile": true, "ga4KeyFile": true,
}

// serviceSecretScopes binds a service credential to the fields that say
// where it is sent.
var serviceSecretScopes = map[string][]string{
	"plausibleApiKey": {"plausibleHost"},
	"aiKey":           {"aiProvider"},
	"embeddingKey":    {"embeddingProvider"},
	"torPassword":     {"torControlPort"},
}

// checkSecretScopes refuses restored secrets whose destination changed
// (see restoreConfigSecrets). Any secret not listed above (cookies, custom
// headers, unknown credential keys) is bound to the crawl's sites.
func checkSecretScopes(in, orig map[string]any, restored map[string]bool) error {
	for field := range restored {
		if unscopedSecretFields[field] {
			continue
		}
		if l := strings.ToLower(field); strings.Contains(l, "webhook") || strings.Contains(l, "slackurl") {
			continue
		}
		if dests, ok := serviceSecretScopes[field]; ok {
			for _, d := range dests {
				if scopeValue(in[d]) != scopeValue(orig[d]) {
					return &scopeError{field: field, dest: "another " + d}
				}
			}
			continue
		}
		if t := newCrawlTarget(in, orig); t != "" {
			return &scopeError{field: field, dest: "the sites of the original crawl, not " + t}
		}
	}
	return nil
}

func scopeValue(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fmt.Sprint(v))), "/")
}

// crawlOrigin is a site a crawl sends its cookies and headers to. host has
// no "www." prefix, so a host and its www twin compare equal.
type crawlOrigin struct {
	scheme, host, port string
	defaultPort        bool // no port in the URL
}

func originOf(raw string) (crawlOrigin, bool) {
	u, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(raw), secretMask, secrets.Placeholder))
	if err != nil || u.Hostname() == "" {
		return crawlOrigin{}, false
	}
	o := crawlOrigin{scheme: strings.ToLower(u.Scheme), host: strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), "www."), port: u.Port()}
	if o.port == "" {
		o.defaultPort = true
		o.port = map[string]string{"http": "80", "https": "443"}[o.scheme]
	}
	return o, true
}

// covers reports whether the crawl's credentials, stored for o, may go to
// t: the same host or its www twin, on the same port (an http -> https
// upgrade with default ports counts as the same), never from https to http.
func (o crawlOrigin) covers(t crawlOrigin) bool {
	if o.host != t.host || (o.scheme == "https" && t.scheme != "https") {
		return false
	}
	return t.port == o.port || (o.defaultPort && t.defaultPort)
}

// newCrawlTarget returns a site or allowed domain of in that orig does not
// cover ("" if none). Covered means one of orig's hosts (seed, URL list,
// sitemaps, allowed domains) or its www twin, on the same port, never moved
// from https to http: the rule the crawler uses to send credentials (see
// crawler.credentialsAllowed). Other subdomains are not covered: on shared
// hosting or SaaS platforms (tenant.example.com) they belong to someone else.
func newCrawlTarget(in, orig map[string]any) string {
	var origins []crawlOrigin
	for _, s := range crawlURLs(orig) {
		if o, ok := originOf(s); ok {
			origins = append(origins, o)
		}
	}
	origDomains := allowedDomainsOf(orig)
	for _, s := range crawlURLs(in) {
		t, ok := originOf(s)
		if !ok {
			return secrets.RedactURL(s)
		}
		covered := false
		for _, o := range origins {
			covered = covered || o.covers(t)
		}
		for _, d := range origDomains {
			// An allowed domain has no scheme or port: only default ports.
			covered = covered || (d == t.host && t.defaultPort)
		}
		if !covered {
			return t.scheme + "://" + t.host
		}
	}
	for _, d := range allowedDomainsOf(in) {
		covered := false
		for _, o := range origins {
			covered = covered || o.host == d
		}
		for _, od := range origDomains {
			covered = covered || od == d
		}
		if !covered {
			return d
		}
	}
	return ""
}

func allowedDomainsOf(cfg map[string]any) []string {
	var out []string
	add := func(s string) {
		for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
			if d = strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), ".")), "www."); d != "" {
				out = append(out, d)
			}
		}
	}
	switch v := cfg["allowedDomains"].(type) {
	case string:
		add(v)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range v {
			add(s)
		}
	}
	return out
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
	c.SeedURL = redactURL(job.SeedURL)
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

// precheckCommandReason is what API clients see instead of the output of a
// failed CLI precheck command (it may print secrets).
const precheckCommandReason = "check command failed"

// redactCommandReason cuts the command output from a schedule reason
// ("paused: check command failed: exit status 1 <output>", as saved by
// older versions), keeping what comes before it.
func redactCommandReason(r string) string {
	if i := strings.Index(r, precheckCommandReason); i >= 0 {
		return r[:i] + precheckCommandReason
	}
	return r
}

// redactedSchedule returns a schedule state safe to send to API clients:
// URL and crawl config credentials, the webhook URL (its path or query is
// the secret), the CLI precheck command and its output are masked.
func redactedSchedule(s scheduler.State) scheduler.State {
	s.URL = redactURL(s.URL)
	s.CrawlConfig = redactConfig(s.CrawlConfig)
	if s.Webhook != "" {
		if u, err := url.Parse(s.Webhook); err == nil && u.Host != "" {
			s.Webhook = u.Scheme + "://" + u.Host + "/" + secretMask
		} else {
			s.Webhook = secretMask
		}
	}
	s.LastReason = redactCommandReason(s.LastReason)
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

// crawlURLs lists the start URLs of a dashboard crawl config: seed, list
// and sitemap URLs.
func crawlURLs(config map[string]interface{}) []string {
	var out []string
	add := func(v interface{}) {
		switch x := v.(type) {
		case string:
			for _, line := range strings.Split(x, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					out = append(out, line)
				}
			}
		case []interface{}:
			for _, e := range x {
				if s, ok := e.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		case []string:
			out = append(out, x...)
		}
	}
	for _, k := range []string{"seedUrl", "url", "urls", "urlList", "sitemapUrls"} {
		add(config[k])
	}
	return out
}
