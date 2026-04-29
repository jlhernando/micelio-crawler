package crawler

import (
	"net"
	"testing"
)

func TestParseIPRangeJSON(t *testing.T) {
	data := []byte(`{
		"prefixes": [
			{"ipv4Prefix": "66.249.64.0/19"},
			{"ipv4Prefix": "66.249.96.0/19"},
			{"ipv6Prefix": "2001:4860:4801::/48"}
		]
	}`)

	nets, err := parseIPRangeJSON(data)
	if err != nil {
		t.Fatalf("parseIPRangeJSON failed: %v", err)
	}
	if len(nets) != 3 {
		t.Fatalf("expected 3 networks, got %d", len(nets))
	}

	// Verify first entry
	if nets[0].CIDR != "66.249.64.0/19" {
		t.Errorf("expected CIDR 66.249.64.0/19, got %s", nets[0].CIDR)
	}

	// Verify the network contains expected IPs
	ip := net.ParseIP("66.249.79.1")
	if !nets[0].Network.Contains(ip) {
		t.Error("expected 66.249.79.1 to be in 66.249.64.0/19")
	}

	ipOutside := net.ParseIP("8.8.8.8")
	if nets[0].Network.Contains(ipOutside) {
		t.Error("expected 8.8.8.8 to NOT be in 66.249.64.0/19")
	}

	// IPv6
	ip6 := net.ParseIP("2001:4860:4801::1")
	if !nets[2].Network.Contains(ip6) {
		t.Error("expected 2001:4860:4801::1 to be in 2001:4860:4801::/48")
	}
}

func TestParseIPRangeJSON_Invalid(t *testing.T) {
	_, err := parseIPRangeJSON([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseIPRangeJSON_EmptyPrefixes(t *testing.T) {
	data := []byte(`{"prefixes": [{"ipv4Prefix": ""}, {}]}`)
	nets, err := parseIPRangeJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 0 {
		t.Errorf("expected 0 networks for empty prefixes, got %d", len(nets))
	}
}

func TestVerifyGoogleBot_InvalidIP(t *testing.T) {
	result := VerifyGoogleBot("not-an-ip")
	if result.Error == "" {
		t.Error("expected error for invalid IP")
	}
	if result.IsVerified {
		t.Error("invalid IP should not be verified")
	}
}
