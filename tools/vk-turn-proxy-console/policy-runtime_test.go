// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
)

func TestRoutingPolicyInputIsBoundedAndStrict(t *testing.T) {
	for _, input := range []string{"", `{}`, `{"mode":"include","shell":"whoami"}`, `{"mode":"include"} {}`, `{"mode":"include","prefixes":["::/0"]}`} {
		if _, err := decodeRoutingPolicy([]byte(input)); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	path := filepath.Join(t.TempDir(), "routing.json")
	if err := os.WriteFile(path, []byte(`{"mode":"include","prefixes":["203.0.113.0/24"],"domains":[{"type":"domain","value":"example.org"}],"localDirect":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := loadPolicyRuntime(path)
	if err != nil || !runtime.matcher.MatchesDomain("cdn.example.org") || !runtime.matcher.MatchesIP(netip.MustParseAddr("203.0.113.7")) {
		t.Fatalf("policy was not compiled: %v", err)
	}
	if legacy, err := loadPolicyRuntime(""); err != nil || legacy != nil {
		t.Fatal("legacy connections unexpectedly acquired a routing policy")
	}
}

func TestPolicyPrefersProfileDNSOverNetwork(t *testing.T) {
	src := &dnsSource{}
	src.set([]string{"192.168.1.1", "1.1.1.1"})
	c := &console{src: src, dnsPlan: dnsPlan{servers: []string{"10.66.0.1", "1.1.1.1"}}}
	servers := c.policyUpstreams()
	if len(servers) != 3 || servers[0].address != "10.66.0.1" || !servers[0].tunnel || servers[1].address != "1.1.1.1" || !servers[1].tunnel || servers[2].address != "192.168.1.1" || servers[2].tunnel {
		t.Fatalf("profile DNS priority or transport was lost: %+v", servers)
	}
}

func TestPolicyDNSRoutingHonorsLocalAndStaticRules(t *testing.T) {
	policy := routingpolicy.Policy{Mode: "include", LocalDirect: true, Prefixes: []string{"203.0.113.0/24"}, Domains: []routingpolicy.Domain{{Type: "domain", Value: "example.org"}}}
	matcher, err := routingpolicy.Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	c := &console{routing: &policyRuntime{policy: policy, matcher: matcher, routed: map[netip.Addr]bool{}}}
	for _, test := range []struct{ name, address string }{
		{"example.org", "192.168.1.1"},
		{"example.org", "203.0.113.7"},
		{"other.org", "198.51.100.7"},
	} {
		if err := c.routeDNSAnswer(test.name, netip.MustParseAddr(test.address)); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.routing.routed) != 0 {
		t.Fatal("an unnecessary host route was added")
	}
	for i := range 8192 {
		c.routing.routed[netip.AddrFrom4([4]byte{198, byte(i >> 8), byte(i), 1})] = true
	}
	if err := c.routeDNSAnswer("example.org", netip.MustParseAddr("192.0.2.7")); err == nil {
		t.Fatal("DNS route limit was ignored")
	}
}

func TestPolicyRouteBatchCommandsAndBoundaries(t *testing.T) {
	batch := policyRouteBatch{Tunnel: "tun ' safe", Prefixes: []string{"203.0.113.0/24"}}
	for _, cmds := range []netCmds{linuxCmds{}, darwinCmds{}, windowsCmds{}} {
		add, del := policyBatchCommand(batch, true, cmds), policyBatchCommand(batch, false, cmds)
		if _, windows := cmds.(windowsCmds); windows {
			cleanup := decodePowerShell(del)
			if !strings.Contains(cleanup, "InterfaceAlias -eq 'tun '' safe'") || !strings.Contains(cleanup, "RouteMetric -eq 42757") {
				t.Fatal(cleanup)
			}
			if !strings.Contains(decodePowerShell(add), "New-NetRoute") {
				t.Fatal(add)
			}
		} else if !strings.Contains(add[2], shellQuote(batch.Tunnel)) || !strings.Contains(del[2], "status=1") {
			t.Fatal("missing shell quoting or cleanup failure propagation")
		}
	}
	var prefixes []string
	for i := range 130 {
		prefixes = append(prefixes, fmt.Sprintf("192.0.2.%d/32", i))
	}
	count := 0
	err := runPolicyBatch(policyRouteBatch{Tunnel: "tun0", Prefixes: prefixes}, true, linuxCmds{}, func([]string) error { count++; return nil })
	if err != nil || count != 3 {
		t.Fatalf("batching failed: calls=%d err=%v", count, err)
	}
	count = 0
	err = runPolicyBatch(policyRouteBatch{Tunnel: "tun0", Prefixes: prefixes}, true, linuxCmds{}, func([]string) error { count++; return context.Canceled })
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal("cancelled route installation continued")
	}
}

func TestPolicyRouteBatchJournalSurvivesPartialApplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	j := newJournal(path)
	batch := policyRouteBatch{Tunnel: "tun0", Prefixes: []string{"203.0.113.0/24", "198.51.100.0/24"}}
	if err := j.add(undoStep{Key: "route policy", PolicyRoutes: &batch}); err != nil {
		t.Fatal(err)
	}
	state, err := readState(path)
	if err != nil || len(state.Undo) != 1 || state.Undo[0].PolicyRoutes == nil || len(state.Undo[0].PolicyRoutes.Prefixes) != 2 {
		t.Fatalf("route plan was not persisted: %+v %v", state, err)
	}
	if err := recoverLeftovers(path, func(argv []string) error {
		if len(argv) == 0 {
			t.Fatal("missing batch cleanup command")
		}
		return nil
	}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful recovery retained the route journal")
	}
}
