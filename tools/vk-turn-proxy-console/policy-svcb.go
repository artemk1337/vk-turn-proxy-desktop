// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"net/netip"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// policySVCB extracts service targets and IPv4 hints before DNS answers leave
// the forwarder. A root target remains "." for the caller to resolve to the
// owner name in ServiceMode. AliasMode parameters are ignored per RFC 9460.
func policySVCB(resource dnsmessage.Resource) (string, []netip.Addr, error) {
	if resource.Header.Type != dnsmessage.TypeSVCB && resource.Header.Type != dnsmessage.TypeHTTPS {
		return "", nil, nil
	}
	if unknown, ok := resource.Body.(*dnsmessage.UnknownResource); ok {
		if unknown.Type != resource.Header.Type || len(unknown.Data) < 3 {
			return "", nil, errors.New("invalid service binding resource")
		}
		// TargetName compression is prohibited by RFC 9460 section 2.2.
		end := 2
		for {
			if end >= len(unknown.Data) {
				return "", nil, errors.New("truncated service binding target")
			}
			length := int(unknown.Data[end])
			end++
			if length > 63 || end+length > len(unknown.Data) || end+length-2 > 255 {
				return "", nil, errors.New("invalid service binding target")
			}
			end += length
			if length == 0 {
				break
			}
		}
		// Reuse dnsmessage's SVCB parser for the parameter wire format.
		packed, err := (&dnsmessage.Message{Answers: []dnsmessage.Resource{resource}}).Pack()
		if err != nil {
			return "", nil, err
		}
		var message dnsmessage.Message
		if err := message.Unpack(packed); err != nil {
			return "", nil, err
		}
		resource = message.Answers[0]
	}
	var binding dnsmessage.SVCBResource
	switch body := resource.Body.(type) {
	case *dnsmessage.SVCBResource:
		binding = *body
	case *dnsmessage.HTTPSResource:
		binding = body.SVCBResource
	default:
		return "", nil, errors.New("invalid service binding body")
	}
	target := strings.ToLower(binding.Target.String())
	if target == "" || !strings.HasSuffix(target, ".") {
		return "", nil, errors.New("invalid service binding target")
	}
	if binding.Priority == 0 {
		return target, nil, nil
	}
	var addrs []netip.Addr
	for i, param := range binding.Params {
		if i > 0 && param.Key <= binding.Params[i-1].Key {
			return "", nil, errors.New("service binding parameters are out of order")
		}
		if param.Key != dnsmessage.SVCParamIPv4Hint {
			continue
		}
		if len(param.Value) == 0 || len(param.Value)%4 != 0 {
			return "", nil, errors.New("invalid service binding IPv4 hints")
		}
		for offset := 0; offset < len(param.Value); offset += 4 {
			addrs = append(addrs, netip.AddrFrom4([4]byte(param.Value[offset:offset+4])))
		}
	}
	return target, addrs, nil
}
