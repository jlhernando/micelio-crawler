package extract

import (
	"regexp"
	"sync"
)

// Custom search patterns come from the crawl config and are the same for
// every page, so compile each one once instead of once per page per rule.
// The cache is capped so a long-running server with many distinct patterns
// cannot grow it without bound (patterns past the cap compile per call).
const maxCachedRegexps = 256

var (
	regexCacheMu sync.RWMutex
	regexCache   = map[string]*regexp.Regexp{} // nil value = invalid pattern
)

// compileCached returns the compiled pattern, or nil when it is invalid.
func compileCached(pattern string) *regexp.Regexp {
	regexCacheMu.RLock()
	re, ok := regexCache[pattern]
	regexCacheMu.RUnlock()
	if ok {
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	regexCacheMu.Lock()
	if len(regexCache) < maxCachedRegexps {
		regexCache[pattern] = re
	}
	regexCacheMu.Unlock()
	return re
}
