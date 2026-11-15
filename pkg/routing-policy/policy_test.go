// SPDX-License-Identifier: MIT
package routingpolicy

import (
	"net/netip"
	"testing"
)

func TestParseCustom(t *testing.T) {
	prefixes, domains, err := ParseCustom("192.0.2.3\n198.51.100.19/24, *.Example.org\nпример.рф\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 || prefixes[0] != "192.0.2.3/32" || prefixes[1] != "198.51.100.0/24" {
		t.Fatalf("unexpected prefixes: %v", prefixes)
	}
	if len(domains) != 2 || domains[0].Value != "example.org" || domains[1].Value != "xn--e1afmkfd.xn--p1ai" {
		t.Fatalf("unexpected domains: %v", domains)
	}
	for _, value := range []string{"::1", "2001:db8::/32", "https://example.org", "192.0.2.0/33", "bad..example", "-bad.example", "example.org:443", "exam_ple.org", "foo bar"} {
		if _, _, err := ParseCustom(value); err == nil {
			t.Errorf("accepted invalid entry %q", value)
		}
	}
}

func TestDomainMatcher(t *testing.T) {
	matcher, err := Compile(Policy{Mode: "include", Prefixes: []string{"192.0.2.19/24"}, Domains: []Domain{
		{Type: "domain", Value: "example.org"}, {Type: "full", Value: "exact.example.net"},
		{Type: "plain", Value: "keyword"}, {Type: "regex", Value: `^service\d+\.example\.com$`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"example.org": true, "SUB.EXAMPLE.ORG.": true, "notexample.org": false,
		"exact.example.net": true, "sub.exact.example.net": false,
		"a-keyword.example": true, "service123.example.com": true, "sub.service123.example.com": false,
	} {
		if got := matcher.MatchesDomain(name); got != want {
			t.Errorf("MatchesDomain(%q) = %v, want %v", name, got, want)
		}
	}
	if !matcher.MatchesIP(netip.MustParseAddr("192.0.2.250")) || matcher.MatchesIP(netip.MustParseAddr("192.0.3.1")) {
		t.Fatal("prefix matching failed")
	}
}

func TestPolicyRejectsInvalidRules(t *testing.T) {
	for _, policy := range []Policy{
		{}, {Mode: "bad"}, {Mode: "include", Prefixes: []string{"::/0"}},
		{Mode: "include", Domains: []Domain{{Type: "regex", Value: "["}}},
		{Mode: "include", Domains: []Domain{{Type: "unknown", Value: "example.org"}}},
		{Mode: "include", Domains: []Domain{{Type: "domain", Value: "https://example.org"}}},
	} {
		if err := policy.Validate(); err == nil {
			t.Errorf("accepted invalid policy: %+v", policy)
		}
	}
	if err := (Settings{}).Validate(); err != nil {
		t.Fatalf("legacy defaults rejected: %v", err)
	}
	if err := (Settings{Custom: "unfinished/"}).Validate(); err != nil {
		t.Fatalf("inactive routing draft blocked full-tunnel mode: %v", err)
	}
}
