// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func policyDNSQuery(t *testing.T, kind dnsmessage.Type) []byte {
	t.Helper()
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 71, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("example.org."), Type: kind, Class: dnsmessage.ClassINET}}}
	data, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func policyDNSResponse(t *testing.T, data []byte) []byte {
	t.Helper()
	var query dnsmessage.Message
	if err := query.Unpack(data); err != nil {
		t.Fatal(err)
	}
	query.Response, query.RecursionAvailable = true, true
	query.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 7}}}}
	answer, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestPolicyDNSBothTransportsRouteBeforeReply(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			routed := make(chan netip.Addr, 1)
			dns, err := startPolicyDNS(context.Background(), "127.0.0.1:0", func(_ context.Context, actual string, query []byte) ([]byte, error) {
				if actual != network {
					t.Errorf("upstream transport %s, want %s", actual, network)
				}
				return policyDNSResponse(t, query), nil
			}, func(name string, addr netip.Addr) error {
				if name != "example.org." {
					t.Errorf("unexpected question %s", name)
				}
				routed <- addr
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer dns.close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			answer, err := exchangePolicyDNS(ctx, network, dns.udp.LocalAddr().String(), policyDNSQuery(t, dnsmessage.TypeA))
			if err != nil {
				t.Fatal(err)
			}
			var message dnsmessage.Message
			if err := message.Unpack(answer); err != nil || message.RCode != dnsmessage.RCodeSuccess || len(message.Answers) != 1 {
				t.Fatalf("invalid answer: %v, %+v", err, message)
			}
			select {
			case addr := <-routed:
				if addr != netip.MustParseAddr("203.0.113.7") {
					t.Fatal(addr)
				}
			default:
				t.Fatal("DNS reply arrived before its destination route")
			}
		})
	}
}

func TestPolicyDNSRejectsUnsafeReplies(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     func(*dnsmessage.Message)
		routeError bool
	}{
		{name: "ID mismatch", change: func(m *dnsmessage.Message) { m.ID++ }},
		{name: "question mismatch", change: func(m *dnsmessage.Message) { m.Questions[0].Name = dnsmessage.MustNewName("other.org.") }},
		{name: "route failure", change: func(*dnsmessage.Message) {}, routeError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := policyDNSQuery(t, dnsmessage.TypeA)
			dns := policyDNS{exchange: func(context.Context, string, []byte) ([]byte, error) {
				var answer dnsmessage.Message
				_ = answer.Unpack(policyDNSResponse(t, query))
				test.change(&answer)
				return answer.Pack()
			}, route: func(string, netip.Addr) error {
				if !test.routeError {
					t.Error("invalid reply reached routing")
				}
				return errors.New("route refused")
			}}
			var answer dnsmessage.Message
			if err := answer.Unpack(dns.answer(context.Background(), "udp", query)); err != nil || answer.RCode != dnsmessage.RCodeServerFailure {
				t.Fatalf("expected SERVFAIL, got %+v (%v)", answer, err)
			}
		})
	}
}

func TestPolicyDNSIPv6IsSuppressed(t *testing.T) {
	dns := policyDNS{exchange: func(context.Context, string, []byte) ([]byte, error) {
		t.Fatal("AAAA query reached upstream")
		return nil, nil
	}}
	var answer dnsmessage.Message
	if err := answer.Unpack(dns.answer(context.Background(), "udp", policyDNSQuery(t, dnsmessage.TypeAAAA))); err != nil || answer.RCode != dnsmessage.RCodeSuccess || len(answer.Answers) != 0 {
		t.Fatalf("IPv6 answer was not suppressed: %+v (%v)", answer, err)
	}
}

func TestPolicyDNSCNAMEChainIgnoresUnrelatedAddresses(t *testing.T) {
	name := dnsmessage.MustNewName("example.org.")
	alias := dnsmessage.MustNewName("cdn.example.net.")
	var names []string
	dns := policyDNS{route: func(value string, addr netip.Addr) error {
		if addr != netip.MustParseAddr("203.0.113.7") {
			t.Fatalf("unrelated answer created a route to %s", addr)
		}
		names = append(names, value)
		return nil
	}}
	answers := []dnsmessage.Resource{
		{Header: dnsmessage.ResourceHeader{Name: alias}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 7}}},
		{Header: dnsmessage.ResourceHeader{Name: alias}, Body: &dnsmessage.CNAMEResource{CNAME: name}},
		{Header: dnsmessage.ResourceHeader{Name: name}, Body: &dnsmessage.CNAMEResource{CNAME: alias}},
		{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("unrelated.org.")}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 9}}},
	}
	if err := dns.routeAnswer(name.String(), answers); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"example.org.", "cdn.example.net."}) {
		t.Fatal(names)
	}
}

func TestPolicyDNSTCPBindFailureReleasesUDP(t *testing.T) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	if dns, err := startPolicyDNS(context.Background(), tcp.Addr().String(), nil, nil); err == nil {
		dns.close()
		t.Fatal("occupied TCP DNS port was accepted")
	}
	udp, err := net.ListenPacket("udp4", tcp.Addr().String())
	if err != nil {
		t.Fatalf("failed setup leaked its UDP socket: %v", err)
	}
	udp.Close()
}

func TestPolicyDNSServiceHintsAndTargetAddresses(t *testing.T) {
	var routes []string
	dns := policyDNS{route: func(name string, addr netip.Addr) error {
		routes = append(routes, name+" "+addr.String())
		return nil
	}}
	binding := serviceBindingResource(dnsmessage.TypeHTTPS, dnsmessage.SVCBResource{
		Priority: 1, Target: dnsmessage.MustNewName("cdn.example.net."),
		Params: []dnsmessage.SVCParam{{Key: dnsmessage.SVCParamIPv4Hint, Value: []byte{203, 0, 113, 7}}},
	})
	addresses := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("cdn.example.net."), Type: dnsmessage.TypeA}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 8}}}
	if err := dns.routeAnswer("example.org.", []dnsmessage.Resource{binding, addresses}); err != nil {
		t.Fatal(err)
	}
	want := []string{"example.org. 203.0.113.7", "cdn.example.net. 203.0.113.7", "example.org. 203.0.113.8", "cdn.example.net. 203.0.113.8"}
	if !slices.Equal(routes, want) {
		t.Fatalf("service hints or additional target addresses were not routed: %v", routes)
	}
}
