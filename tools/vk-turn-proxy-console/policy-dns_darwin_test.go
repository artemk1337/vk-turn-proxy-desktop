//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDarwinPolicyDNSFailuresAreFatal(t *testing.T) {
	for _, failed := range []string{"-getdnsservers", "-setdnsservers"} {
		t.Run(failed, func(t *testing.T) {
			dns := darwinDNS{j: newJournal(filepath.Join(t.TempDir(), "state.json")), logf: func(string, ...any) {}, run: func(argv []string) (string, error) {
				if argv[1] == "-listallnetworkservices" {
					return "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n", nil
				}
				if argv[1] == failed {
					return "", errors.New("networksetup failed")
				}
				return "There aren't any DNS Servers set on Wi-Fi.", nil
			}}
			if err := dns.apply("utun0", []string{"127.0.0.1"}); err == nil {
				t.Fatal("policy activation ignored a DNS configuration failure")
			}
		})
	}
}
