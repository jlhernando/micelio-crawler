// Package intern shares the backing data of strings that repeat across
// decoded records (menu links, resource types, logo URLs) and trims the
// spare capacity decoders leave on slices. Only memory changes: every value
// stays equal.
package intern

// DefaultMax bounds a Table so mostly-unique strings (content links, JSON-LD
// blobs) cannot grow it without limit: ~50 B of map overhead per entry, so
// 1M entries cost ~50-100 MB at most. Strings that repeat show up early
// (they are on most pages), so they are in the table long before it fills.
const DefaultMax = 1 << 20

// Table interns strings up to a fixed number of entries. Once full it still
// returns the shared copy of strings already in it and passes others
// through unchanged. Not safe for concurrent use; one Table per load.
type Table struct {
	m   map[string]string
	max int
}

// New returns a Table that holds at most max entries (DefaultMax if max <= 0).
func New(max int) *Table {
	if max <= 0 {
		max = DefaultMax
	}
	return &Table{m: map[string]string{}, max: max}
}

// Str returns the shared copy of s ("" is returned as is).
func (t *Table) Str(s string) string {
	if s == "" {
		return s
	}
	if v, ok := t.m[s]; ok {
		return v
	}
	if len(t.m) < t.max {
		t.m[s] = s
	}
	return s
}

// Len reports how many strings the table holds.
func (t *Table) Len() int { return len(t.m) }

// Clip returns s with cap == len, copying only when the decoder
// over-allocated. nil and empty slices are returned as they are.
func Clip[T any](s []T) []T {
	if len(s) == 0 || cap(s) == len(s) {
		return s
	}
	return append(make([]T, 0, len(s)), s...)
}
