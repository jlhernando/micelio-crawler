package urlutil

import "testing"

func TestEscapeQuery(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"a=1&b=2":                      "a=1&b=2",
		"specialty=Encimera extraíble": "specialty=Encimera%20extra%C3%ADble",
		"q=a%20b&c=d|e":                "q=a%20b&c=d%7Ce",
	} {
		if got := EscapeQuery(in); got != want {
			t.Errorf("EscapeQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
