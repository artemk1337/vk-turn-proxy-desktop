// SPDX-License-Identifier: MIT

package main

import (
	"net/netip"
	"slices"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func serviceBindingResource(kind dnsmessage.Type, binding dnsmessage.SVCBResource) dnsmessage.Resource {
	resource := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{
		Name: dnsmessage.MustNewName("example.org."), Type: kind, Class: dnsmessage.ClassINET,
	}}
	if kind == dnsmessage.TypeHTTPS {
		resource.Body = &dnsmessage.HTTPSResource{SVCBResource: binding}
	} else {
		resource.Body = &binding
	}
	return resource
}

func TestPolicySVCBNativeHints(t *testing.T) {
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeSVCB, dnsmessage.TypeHTTPS} {
		binding := dnsmessage.SVCBResource{Priority: 1, Target: dnsmessage.MustNewName("CDN.Example.org."), Params: []dnsmessage.SVCParam{
			{Key: dnsmessage.SVCParamALPN, Value: []byte{2, 'h', '2'}},
			{Key: dnsmessage.SVCParamIPv4Hint, Value: []byte{192, 0, 2, 1, 198, 51, 100, 2}},
			{Key: dnsmessage.SVCParamIPv6Hint, Value: make([]byte, 16)},
		}}
		target, addrs, err := policySVCB(serviceBindingResource(kind, binding))
		want := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.2")}
		if err != nil || target != "cdn.example.org." || !slices.Equal(addrs, want) {
			t.Fatalf("kind %v: target=%q addrs=%v err=%v", kind, target, addrs, err)
		}
	}
}

func TestPolicySVCBAliasAndRootTarget(t *testing.T) {
	alias := dnsmessage.SVCBResource{Target: dnsmessage.MustNewName("alias.example.org."), Params: []dnsmessage.SVCParam{
		{Key: dnsmessage.SVCParamIPv4Hint, Value: []byte{192, 0, 2, 1}},
	}}
	target, addrs, err := policySVCB(serviceBindingResource(dnsmessage.TypeHTTPS, alias))
	if err != nil || target != "alias.example.org." || len(addrs) != 0 {
		t.Fatalf("AliasMode hints must be ignored: %q %v %v", target, addrs, err)
	}
	service := dnsmessage.SVCBResource{Priority: 1, Target: dnsmessage.MustNewName(".")}
	target, addrs, err = policySVCB(serviceBindingResource(dnsmessage.TypeHTTPS, service))
	if err != nil || target != "." || len(addrs) != 0 {
		t.Fatalf("root target lost: %q %v %v", target, addrs, err)
	}
}

func TestPolicySVCBUnknownWire(t *testing.T) {
	// Priority 1, root target, ipv4hint=192.0.2.1.
	data := []byte{0, 1, 0, 0, 4, 0, 4, 192, 0, 2, 1}
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeSVCB, dnsmessage.TypeHTTPS} {
		resource := dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("example.org."), Type: kind, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.UnknownResource{Type: kind, Data: data},
		}
		target, addrs, err := policySVCB(resource)
		if err != nil || target != "." || len(addrs) != 1 || addrs[0].String() != "192.0.2.1" {
			t.Fatalf("unknown kind %v: %q %v %v", kind, target, addrs, err)
		}
	}
}

func TestPolicySVCBRejectsMalformedWire(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0, 1},
		{0, 1, 3, 'a'},
		{0, 1, 0xc0, 0},
		{0, 1, 0, 0},
		{0, 1, 0, 0, 4, 0, 4, 192},
		{0, 1, 0, 0, 4, 0, 3, 192, 0, 2},
		{0, 1, 0, 0, 4, 0, 0},
		{0, 1, 0, 0, 4, 0, 4, 192, 0, 2, 1, 0, 4, 0, 4, 192, 0, 2, 2},
	} {
		resource := dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("example.org."), Type: dnsmessage.TypeHTTPS, Class: dnsmessage.ClassINET},
			Body:   &dnsmessage.UnknownResource{Type: dnsmessage.TypeHTTPS, Data: data},
		}
		if _, _, err := policySVCB(resource); err == nil {
			t.Errorf("accepted malformed wire %v", data)
		}
	}
}

func TestPolicySVCBRejectsMalformedNative(t *testing.T) {
	for _, binding := range []dnsmessage.SVCBResource{
		{Priority: 1},
		{Priority: 1, Target: dnsmessage.MustNewName("example.org."), Params: []dnsmessage.SVCParam{
			{Key: dnsmessage.SVCParamIPv4Hint, Value: []byte{192, 0, 2, 1}},
			{Key: dnsmessage.SVCParamPort, Value: []byte{1, 187}},
		}},
	} {
		if _, _, err := policySVCB(serviceBindingResource(dnsmessage.TypeHTTPS, binding)); err == nil {
			t.Fatal("accepted malformed native service binding")
		}
	}
	if target, addrs, err := policySVCB(dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Type: dnsmessage.TypeA}}); err != nil || target != "" || addrs != nil {
		t.Fatalf("unrelated record was parsed: %q %v %v", target, addrs, err)
	}
}
