package storage

import (
	"encoding/json"
	"strings"
	"sync"
)

// pageFilterBuf holds the trimmed page JSON between rows. encoding/json
// copies every string it decodes, so nothing decoded points into it.
var pageFilterBuf = sync.Pool{New: func() any { b := make([]byte, 0, 64<<10); return &b }}

// unreadPageKeys lists the top-level keys a reload with these options never
// decodes (skipJSON shadows in decodeAnalysisPageOpt). The link arrays are
// only counted, so they are handled apart.
func unreadPageKeys(o pageLoadOpts) []string {
	var keys []string
	if o.skipAnchors {
		keys = append(keys, "anchors")
	}
	if o.cacheOnly && o.dropSchemaRaw {
		keys = append(keys, "images", "openGraph", "twitterCard", "schemaValidation", "renderDiffs")
	}
	return keys
}

// trimUnreadPageJSON copies the page object into dst without the values a
// reload never decodes, and counts the internal and external link arrays
// from their bytes. A page is ~38 KB of JSON, mostly anchors, images and
// links; encoding/json scans every skipped byte twice (validation, then
// skip), so the decode of 100K pages spends most of its time on values it
// throws away. The kept values are copied verbatim and decoded by
// encoding/json as before, and the same counts come out (last key wins).
// Skipped values and counted arrays are validated before removal; retained
// values are validated by encoding/json. Anything unusual (escaped key, a
// key that only case-folds to a skipped one, broken structure, a link array
// the counter rejects) returns ok=false and decodes the whole blob.
//
// With fold non-nil the "images" value is taken out too, validated the same
// way and folded into fold.sum (summarizeImagesJSON); a repeated or folded
// "images" key, or an array only encoding/json can judge, returns ok=false.
func trimUnreadPageJSON(data, dst []byte, unread []string, fold *imageFold) (out []byte, in, ex stringArrayCount, ok bool) {
	i := skipWS(data, 0)
	if i >= len(data) || data[i] != '{' {
		return nil, 0, 0, false
	}
	dst = append(dst, '{')
	first := true
	i = skipWS(data, i+1)
	if i < len(data) && data[i] == '}' {
		return append(dst, '}'), 0, 0, skipWS(data, i+1) == len(data)
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return nil, 0, 0, false
		}
		end := i + 1
		for end < len(data) && data[end] != '"' {
			if data[end] == '\\' {
				return nil, 0, 0, false
			}
			end++
		}
		if end >= len(data) {
			return nil, 0, 0, false
		}
		keyStart, key := i, string(data[i+1:end])
		i = skipWS(data, end+1)
		if i >= len(data) || data[i] != ':' {
			return nil, 0, 0, false
		}
		i = skipWS(data, i+1)
		vEnd := skipValue(data, i)
		if vEnd < 0 {
			return nil, 0, 0, false
		}
		k := pageKeyKind(key, unread)
		if fold != nil && k == keyKeep && strings.EqualFold(key, "images") {
			if key != "images" || fold.seen || !json.Valid(data[i:vEnd]) {
				return nil, 0, 0, false
			}
			if fold.sum, ok = summarizeImagesJSON(data[i:vEnd]); !ok {
				return nil, 0, 0, false
			}
			fold.seen, k = true, keySkip
		}
		switch k {
		case keyFolded:
			return nil, 0, 0, false
		case keySkip:
			if (fold == nil || key != "images") && !json.Valid(data[i:vEnd]) {
				return nil, 0, 0, false
			}
		case keyInternal, keyExternal:
			// The counter normally runs after encoding/json validates the
			// entire document. Keep that guarantee when calling it directly.
			if !json.Valid(data[i:vEnd]) {
				return nil, 0, 0, false
			}
			var n stringArrayCount
			if n.UnmarshalJSON(data[i:vEnd]) != nil {
				return nil, 0, 0, false
			}
			if k == keyInternal {
				in = n
			} else {
				ex = n
			}
		case keyKeep:
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = append(dst, data[keyStart:vEnd]...)
		}
		i = skipWS(data, vEnd)
		if i >= len(data) {
			return nil, 0, 0, false
		}
		if data[i] == '}' {
			if skipWS(data, i+1) != len(data) {
				return nil, 0, 0, false
			}
			return append(dst, '}'), in, ex, true
		}
		if data[i] != ',' {
			return nil, 0, 0, false
		}
		i = skipWS(data, i+1)
	}
}

const (
	keyKeep = iota
	keySkip
	keyInternal
	keyExternal
	keyFolded // case-folds to a handled key: encoding/json would match it
)

func pageKeyKind(key string, unread []string) int {
	switch key {
	case "internalLinks":
		return keyInternal
	case "externalLinks":
		return keyExternal
	}
	for _, u := range unread {
		if key == u {
			return keySkip
		}
	}
	if strings.EqualFold(key, "internalLinks") || strings.EqualFold(key, "externalLinks") {
		return keyFolded
	}
	for _, u := range unread {
		if strings.EqualFold(key, u) {
			return keyFolded
		}
	}
	return keyKeep
}
