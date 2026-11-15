// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPolicyDNSPreservesDHCPAndStaticSettings(t *testing.T) {
	j := newJournal(filepath.Join(t.TempDir(), "state.json"))
	var scripts []string
	dns := windowsDNS{j: j, run: func(argv []string) (string, error) {
		script := decodePowerShell(argv)
		scripts = append(scripts, script)
		if strings.Contains(script, "$states =") {
			if len(scripts) > 1 {
				return `[{"guid":"{dhcp-guid}","alias":"Ethernet","servers":["127.0.0.1"],"static":false},{"guid":"{static-guid}","alias":"WiFi","servers":["127.0.0.1"],"static":false}]`, nil
			}
			return `[{"guid":"{dhcp-guid}","alias":"Ethernet","servers":["192.168.1.1"],"static":false},{"guid":"{static-guid}","alias":"WiFi","servers":["9.9.9.9"],"static":true}]`, nil
		}
		return "", nil
	}}
	if err := dns.apply("tun0", []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if len(j.doc.Undo) != 2 || len(scripts) != 3 {
		t.Fatalf("unexpected policy DNS changes: undo=%d scripts=%d", len(j.doc.Undo), len(scripts))
	}
	dhcp := decodePowerShell(j.doc.Undo[0].Argv)
	static := decodePowerShell(j.doc.Undo[1].Argv)
	if !strings.Contains(dhcp, "ResetServerAddresses") || !strings.Contains(static, "@('9.9.9.9')") {
		t.Fatal("DHCP/static DNS distinction was not preserved")
	}
	if !strings.Contains(scripts[1], "AddressFamily IPv4") || !strings.Contains(scripts[1], "-InputObject $dns") || !strings.Contains(scripts[1], "@('127.0.0.1')") {
		t.Fatal("IPv4 DNS was not redirected to the policy forwarder")
	}
	if err := dns.apply("tun0", []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if len(j.doc.Undo) != 2 || len(scripts) != 4 {
		t.Fatal("repeat application overwrote the original DNS settings")
	}
}

func TestWindowsPolicyDNSReappliesAfterExternalChange(t *testing.T) {
	j := newJournal(filepath.Join(t.TempDir(), "state.json"))
	snapshots, changes := 0, 0
	dns := windowsDNS{j: j, run: func(argv []string) (string, error) {
		script := decodePowerShell(argv)
		if strings.Contains(script, "$states =") {
			snapshots++
			if snapshots == 1 {
				return `[{"guid":"{guid}","alias":"Ethernet","servers":["192.168.1.1"],"static":false}]`, nil
			}
			return `[{"guid":"{guid}","alias":"Ethernet","servers":["9.9.9.10"],"static":true}]`, nil
		}
		changes++
		return "", nil
	}}
	for range 2 {
		if err := dns.apply("tun0", []string{"127.0.0.1"}); err != nil {
			t.Fatal(err)
		}
	}
	if changes != 2 || len(j.doc.Undo) != 1 || !strings.Contains(decodePowerShell(j.doc.Undo[0].Argv), "@('9.9.9.10')") {
		t.Fatal("changed DNS was not preserved before reapplying the local forwarder")
	}
}
