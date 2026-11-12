package storage

import (
	"encoding/json"
	"strings"
)

// pageLinks is what IterateInternalLinks needs from a stored page.
type pageLinks struct {
	URL           string   `json:"url"`
	InternalLinks []string `json:"internalLinks"`
}

// decodePageLinks fills only url and internalLinks from a page blob. A page
// is ~38 KB of JSON (anchors, images, structured data) and encoding/json
// scans every byte of it twice (validation, then decode) even when the
// target struct has two fields: 19 s for 100K pages. Here the other values
// are skipped by bracket and quote counting, and the two wanted values are
// decoded with encoding/json from their own bytes, so they come out exactly
// as a full Unmarshal gives them (last key wins). Anything unusual (escaped
// key, a key that only case-folds to a wanted one, broken structure) falls
// back to json.Unmarshal of the whole blob, errors included.
//
// What "broken structure" covers: keys, colons, commas, quotes, bracket
// nesting and every top-level scalar (true/false/null/number grammar) are
// checked. Tokens nested inside a skipped object or array are only checked
// for quote and bracket balance, so a blob that is invalid only there (e.g.
// "x":[tru]) is read instead of rejected. Blobs come from json.Marshal and
// zstd (checksummed), so that case means a bug elsewhere, not disk damage;
// a full json.Valid pass would cost half of what this saves.
func decodePageLinks(data []byte, out *pageLinks) error {
	if fastPageLinks(data, out) {
		return nil
	}
	*out = pageLinks{}
	return json.Unmarshal(data, out)
}

func fastPageLinks(data []byte, out *pageLinks) bool {
	i := skipWS(data, 0)
	if i >= len(data) || data[i] != '{' {
		return false
	}
	i = skipWS(data, i+1)
	if i < len(data) && data[i] == '}' {
		return skipWS(data, i+1) == len(data)
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return false
		}
		end := i + 1
		for end < len(data) && data[end] != '"' {
			if data[end] == '\\' {
				return false // escaped key: let encoding/json decide
			}
			end++
		}
		if end >= len(data) {
			return false
		}
		key := string(data[i+1 : end])
		i = skipWS(data, end+1)
		if i >= len(data) || data[i] != ':' {
			return false
		}
		i = skipWS(data, i+1)
		vEnd := skipValue(data, i)
		if vEnd < 0 {
			return false
		}
		switch {
		case key == "url":
			if json.Unmarshal(data[i:vEnd], &out.URL) != nil {
				return false
			}
		case key == "internalLinks":
			if json.Unmarshal(data[i:vEnd], &out.InternalLinks) != nil {
				return false
			}
		case strings.EqualFold(key, "url") || strings.EqualFold(key, "internalLinks"):
			return false // encoding/json matches keys case-insensitively
		}
		i = skipWS(data, vEnd)
		if i >= len(data) {
			return false
		}
		if data[i] == '}' {
			return skipWS(data, i+1) == len(data)
		}
		if data[i] != ',' {
			return false
		}
		i = skipWS(data, i+1)
	}
}

func skipWS(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// skipString returns the index after the closing quote of the string that
// starts at data[i] == '"', or -1.
func skipString(data []byte, i int) int {
	for i++; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// skipValue returns the index right after the JSON value at data[i], or -1.
// Scalars end at the next delimiter; the wanted values are checked by
// encoding/json, the skipped ones only for balanced brackets and quotes.
func skipValue(data []byte, i int) int {
	if i >= len(data) {
		return -1
	}
	switch data[i] {
	case '"':
		return skipString(data, i)
	case '{', '[':
		var buf [32]byte
		stack := buf[:0]
		for ; i < len(data); i++ {
			switch c := data[i]; c {
			case '"':
				j := skipString(data, i)
				if j < 0 {
					return -1
				}
				i = j - 1
			case '{', '[':
				stack = append(stack, c)
			case '}', ']':
				open := byte('{')
				if c == ']' {
					open = '['
				}
				if len(stack) == 0 || stack[len(stack)-1] != open {
					return -1
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return i + 1
				}
			}
		}
		return -1
	default:
		j := i
		for j < len(data) {
			switch data[j] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				if !validScalar(data[i:j]) {
					return -1
				}
				return j
			}
			j++
		}
		if !validScalar(data[i:j]) {
			return -1
		}
		return j
	}
}

// validScalar reports whether tok is a JSON literal or number.
func validScalar(tok []byte) bool {
	switch string(tok) {
	case "true", "false", "null":
		return true
	}
	i := 0
	if i < len(tok) && tok[i] == '-' {
		i++
	}
	digits := func() int {
		n := 0
		for i < len(tok) && tok[i] >= '0' && tok[i] <= '9' {
			i++
			n++
		}
		return n
	}
	start := i
	if n := digits(); n == 0 || (n > 1 && tok[start] == '0') {
		return false
	}
	if i < len(tok) && tok[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < len(tok) && (tok[i] == 'e' || tok[i] == 'E') {
		i++
		if i < len(tok) && (tok[i] == '+' || tok[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(tok)
}
