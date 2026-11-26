package storage

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/micelio/micelio/internal/types"
)

// imageFold gets SummarizeImages of the "images" array that the analysis
// trim takes out (~1/4 of a page's JSON, which encoding/json scans twice).
// Anything the scanner does not mirror exactly (escaped, non-ASCII or folded
// keys, wrong types, repeated "images") makes the page decode as before.
type imageFold struct {
	sum  *types.ImageSummary
	seen bool
}

var imageKeys = [...]string{"src", "alt", "width", "height", "inlineBytes"}

// summarizeImagesJSON returns SummarizeImages of the decoded array, or
// ok=false when only encoding/json can say what it decodes to. data must
// be valid JSON (json.Valid).
func summarizeImagesJSON(data []byte) (sum *types.ImageSummary, ok bool) {
	i := skipWS(data, 0)
	if bytes.Equal(data[i:], []byte("null")) {
		return nil, true
	}
	if i >= len(data) || data[i] != '[' {
		return nil, false
	}
	var s types.ImageSummary
	for i = skipWS(data, i+1); i < len(data) && data[i] != ']'; {
		switch data[i] {
		case 'n': // null element: a zero ImageData
			if !bytes.HasPrefix(data[i:], []byte("null")) {
				return nil, false
			}
			s.NoAltAttr++
			s.NoDims++
			i += 4
		case '{':
			if i = summarizeImageObject(data, i, &s); i < 0 {
				return nil, false
			}
		default:
			return nil, false
		}
		s.Total++
		if i = skipWS(data, i); i < len(data) && data[i] == ',' {
			i = skipWS(data, i+1)
		}
	}
	if i >= len(data) {
		return nil, false
	}
	if s.Total == 0 {
		return nil, true
	}
	return &s, true
}

// summarizeImageObject adds the image object at data[i] to s and returns the
// index after it, or -1 for the slow path.
func summarizeImageObject(data []byte, i int, s *types.ImageSummary) int {
	var src, alt []byte // last string values
	var hasW, hasH bool
	for i = skipWS(data, i+1); i < len(data) && data[i] != '}'; {
		ke := skipString(data, i) // valid JSON: key, ws, ':', ws, value
		key := data[i+1 : ke-1]
		i = skipWS(data, skipWS(data, ke)+1)
		ve := skipValue(data, i)
		if !plainASCII(key) || ve < 0 {
			return -1
		}
		v := data[i:ve]
		isStr, isNull := v[0] == '"', string(v) == "null"
		switch k := string(key); k {
		case "src", "alt", "width", "height":
			if !isStr && !isNull {
				return -1
			}
			switch k[0] {
			case 's':
				if isStr { // null keeps the old src
					src = v
				}
			case 'a':
				if alt = nil; isStr {
					alt = v
				}
			case 'w':
				hasW = isStr
			default:
				hasH = isStr
			}
		case "inlineBytes":
			if !isNull {
				if _, err := strconv.ParseInt(string(v), 10, strconv.IntSize); err != nil {
					return -1
				}
			}
		default:
			// Keep nested unknown values on the full decoder. Validating the
			// images value alone omits the enclosing page's nesting level;
			// removing it could otherwise hide a document depth error.
			if v[0] == '{' || v[0] == '[' {
				return -1
			}
			for _, k := range imageKeys {
				if bytes.EqualFold(key, []byte(k)) {
					return -1 // json would match it case-insensitively
				}
			}
		}
		if i = skipWS(data, ve); i < len(data) && data[i] == ',' {
			i = skipWS(data, i+1)
		}
	}
	if i >= len(data) {
		return -1
	}
	switch {
	case alt == nil:
		s.NoAltAttr++
	case len(alt) == 2:
		s.EmptyAlt++
	default:
		n := len(alt) - 2
		if !plainASCII(alt[1 : len(alt)-1]) {
			a, ok := jsonString(alt)
			if !ok {
				return -1
			}
			n = len(a)
		}
		if n > types.AltTooLongLimit {
			sv, ok := "", true
			if src != nil {
				if sv, ok = jsonString(src); !ok {
					return -1
				}
			}
			s.LongAlt = append(s.LongAlt, types.ImageAltIssue{Src: sv, AltLength: n})
		}
	}
	if !hasW || !hasH {
		s.NoDims++
	}
	return i + 1
}

// plainASCII reports a key with no escapes and no non-ASCII bytes (json
// unescapes keys and folds K and ſ into k and s before matching fields).
func plainASCII(b []byte) bool {
	for _, c := range b {
		if c == '\\' || c >= 0x80 {
			return false
		}
	}
	return true
}

// jsonString decodes a quoted JSON string; json.Unmarshal for escapes and
// non-ASCII bytes (it replaces invalid UTF-8 with U+FFFD).
func jsonString(q []byte) (string, bool) {
	if raw := q[1 : len(q)-1]; plainASCII(raw) {
		return string(raw), true
	}
	var s string
	return s, json.Unmarshal(q, &s) == nil
}
