// SPDX-License-Identifier: MIT

package routingpolicy

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestTunnelPrefixesAggregateAndComplement(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		input []string
		want  []string
	}{
		{"empty include", "include", nil, nil},
		{"empty exclude", "exclude", nil, []string{"0.0.0.0/1", "128.0.0.0/1"}},
		{"full include", "include", []string{"0.0.0.0/0"}, []string{"0.0.0.0/1", "128.0.0.0/1"}},
		{"full exclude", "exclude", []string{"0.0.0.0/0"}, nil},
		{"adjacent", "include", []string{"192.0.2.128/25", "192.0.2.0/25"}, []string{"192.0.2.0/24"}},
		{"nested", "include", []string{"192.0.2.0/24", "192.0.2.5/32", "192.0.2.0/24"}, []string{"192.0.2.0/24"}},
		{"mask host bits", "include", []string{"192.0.2.77/24"}, []string{"192.0.2.0/24"}},
		{"exclude half", "exclude", []string{"0.0.0.0/1"}, []string{"128.0.0.0/1"}},
		{"maximum address", "include", []string{"255.255.255.255/32"}, []string{"255.255.255.255/32"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefixes, err := TunnelPrefixes(Policy{Mode: test.mode, Prefixes: test.input})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, prefix := range prefixes {
				got = append(got, prefix.String())
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestTunnelPrefixesLocalDirect(t *testing.T) {
	for _, mode := range []string{"include", "exclude"} {
		t.Run(mode, func(t *testing.T) {
			policy := Policy{Mode: mode, LocalDirect: true}
			if mode == "include" {
				policy.Prefixes = []string{"0.0.0.0/0"}
			}
			prefixes, err := TunnelPrefixes(policy)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{"10.2.3.4", "127.0.0.1", "169.254.1.2", "172.16.0.1", "192.168.9.1", "224.0.0.1", "255.255.255.255"} {
				addr := netip.MustParseAddr(raw)
				for _, prefix := range prefixes {
					if prefix.Contains(addr) {
						t.Fatalf("local address %s routed by %s", addr, prefix)
					}
				}
			}
			for _, raw := range []string{"8.8.8.8", "100.64.1.1", "172.15.255.255", "192.169.0.1", "255.255.255.254"} {
				addr := netip.MustParseAddr(raw)
				found := false
				for _, prefix := range prefixes {
					found = found || prefix.Contains(addr)
				}
				if !found {
					t.Fatalf("public address %s omitted", addr)
				}
			}
		})
	}
}

func TestTunnelPrefixesComplementBoundaryHosts(t *testing.T) {
	for _, raw := range []string{"0.0.0.0", "255.255.255.255"} {
		prefixes, err := TunnelPrefixes(Policy{Mode: "exclude", Prefixes: []string{raw + "/32"}})
		if err != nil {
			t.Fatal(err)
		}
		var size uint64
		for _, prefix := range prefixes {
			if prefix.Contains(netip.MustParseAddr(raw)) || prefix.Bits() == 0 {
				t.Fatalf("invalid complement prefix %s for %s", prefix, raw)
			}
			size += uint64(1) << (32 - prefix.Bits())
		}
		if size != ipv4Space-1 {
			t.Fatalf("complement size = %d, want %d", size, ipv4Space-1)
		}
	}
}

func TestTunnelPrefixesSubtractLocalFromNarrowInclude(t *testing.T) {
	prefixes, err := TunnelPrefixes(Policy{Mode: "include", Prefixes: []string{"10.0.0.0/8", "192.0.2.0/24", "192.168.0.0/15"}, LocalDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.169.0.0/16")}
	if !reflect.DeepEqual(prefixes, want) {
		t.Fatalf("got %v, want %v", prefixes, want)
	}
}

func TestTunnelPrefixesRejectInvalidInput(t *testing.T) {
	for _, policy := range []Policy{{Mode: "invalid"}, {Mode: "include", Prefixes: []string{"bad"}}, {Mode: "exclude", Prefixes: []string{"::/0"}}, {Mode: "include", Prefixes: []string{"::ffff:192.0.2.0/120"}}} {
		if _, err := TunnelPrefixes(policy); err == nil {
			t.Fatalf("accepted %+v", policy)
		}
	}
}

func TestIsLocal(t *testing.T) {
	for _, raw := range []string{"10.1.2.3", "127.0.0.1", "169.254.0.1", "172.31.255.255", "192.168.0.1", "224.0.0.1", "255.255.255.255", "::ffff:10.1.2.3"} {
		if !IsLocal(netip.MustParseAddr(raw)) {
			t.Errorf("%s should be local", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "100.64.0.1", "172.32.0.0", "192.169.0.0", "::1"} {
		if IsLocal(netip.MustParseAddr(raw)) {
			t.Errorf("%s should not be IPv4 local", raw)
		}
	}
}
