// Package csvsafe writes CSV files that are safe to open in a spreadsheet.
//
// Crawled titles, descriptions, URLs and headings come from the crawled
// sites. A cell that starts with = + - @ (or a tab or carriage return) is
// read as a formula by Excel, LibreOffice and Google Sheets, so a hostile
// page could run =WEBSERVICE(...) or =HYPERLINK(...) on the analyst's
// machine when an export is opened. encoding/csv quotes separators but
// leaves formulas alone. Cell prefixes such a value with an apostrophe
// (OWASP "CSV injection"); numbers like -3 or +1.5e3 stay numbers.
package csvsafe

import (
	"encoding/csv"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// number matches a plain decimal number: a spreadsheet reads it as a
// number, never as a formula, so it keeps its sign.
var number = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// trigger reports whether a cell starting with r is read as a formula.
// The full-width forms (U+FF1D, U+FF0B, U+FF0D, U+FF20) are included:
// some spreadsheet locales accept them.
func trigger(r rune) bool {
	switch r {
	case '=', '+', '-', '@', '\t', '\r', '\n', 0xff1d, 0xff0b, 0xff0d, 0xff20:
		return true
	}
	return false
}

// Cell returns s ready for a CSV cell: a value a spreadsheet would
// evaluate as a formula gets a leading apostrophe ('), which spreadsheets
// show as plain text. Leading spaces do not hide a formula.
func Cell(s string) string {
	t := strings.TrimLeft(s, " ")
	if t == "" {
		return s
	}
	r, _ := utf8.DecodeRuneInString(t)
	if !trigger(r) {
		return s
	}
	if (r == '-' || r == '+') && t == s && number.MatchString(s) {
		return s
	}
	return "'" + s
}

// Row applies Cell to every cell of a record. It returns rec itself when
// nothing changes (the common case) and a copy otherwise.
func Row(rec []string) []string {
	var out []string
	for i, s := range rec {
		c := Cell(s)
		if c == s {
			continue
		}
		if out == nil {
			out = append([]string(nil), rec...)
		}
		out[i] = c
	}
	if out == nil {
		return rec
	}
	return out
}

// Writer is a csv.Writer whose Write and WriteAll neutralise formulas in
// every cell. Every Micelio CSV export goes through it.
type Writer struct{ *csv.Writer }

// NewWriter returns a formula-safe CSV writer on w.
func NewWriter(w io.Writer) *Writer { return &Writer{csv.NewWriter(w)} }

// Write writes one record with every cell passed through Cell.
func (w *Writer) Write(rec []string) error { return w.Writer.Write(Row(rec)) }

// WriteAll writes the records with Write and flushes.
func (w *Writer) WriteAll(records [][]string) error {
	for _, rec := range records {
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
