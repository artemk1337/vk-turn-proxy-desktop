// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run only in an isolated network namespace with CAP_NET_ADMIN and /dev/net/tun.
func TestLinuxRuntimeTunnelRoutesAndRecovery(t *testing.T) {
	if os.Getenv("VKTURN_LINUX_RUNTIME_TEST") != "1" {
		t.Skip("isolated Linux network runtime test is opt-in")
	}
	if os.Geteuid() != 0 {
		t.Fatal("runtime test requires root in an isolated network namespace")
	}
	if _, up := readDefaultRoute(); up {
		t.Fatal("runtime test requires an empty network namespace without a default route")
	}
	const physical = "vktp-smoke"
	if _, err := runCmd([]string{"ip", "link", "add", physical, "type", "dummy"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runCmd([]string{"ip", "link", "delete", physical}) })
	if err := runAll([][]string{
		{"ip", "address", "add", "192.0.2.2/24", "dev", physical},
		{"ip", "link", "set", physical, "up"},
		{"ip", "route", "add", "default", "via", "192.0.2.1", "dev", physical},
	}); err != nil {
		t.Fatal(err)
	}
	gw, up := readDefaultRoute()
	if !up || gw.IP != "192.0.2.1" || gw.Iface != physical {
		t.Fatalf("physical gateway = %v, up %v", gw, up)
	}
	device, name, err := openTUN("vktp-smoke-tun", 1280)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if err := runAll(hostCmds.addrUp(name, "10.66.0.2/24", 1280)); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	statePath := filepath.Join(root, "network-state.json")
	j := newJournal(statePath)
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { recoverLeftovers(statePath, quiet(runCmd), t.Logf) })
	for _, route := range hostCmds.splitRoutes(name) {
		if err := j.add(undoStep{Key: "route " + route.key, Argv: route.del}); err != nil {
			t.Fatal(err)
		}
		if _, err := runCmd(route.add); err != nil {
			t.Fatal(err)
		}
	}
	add, del := hostCmds.pin("198.51.100.3", gw)
	if err := j.add(undoStep{Key: "pin 198.51.100.3", Argv: del}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(add); err != nil {
		t.Fatal(err)
	}
	for address, iface := range map[string]string{"203.0.113.9": name, "198.51.100.3": physical} {
		out, err := runCmd([]string{"ip", "-4", "route", "get", address})
		if err != nil || !strings.Contains(out, "dev "+iface) {
			t.Fatalf("route to %s = %s, %v", address, out, err)
		}
	}
	resolvPath := filepath.Join(root, "resolv.conf")
	const original = "nameserver 192.0.2.53\nsearch example.test\n"
	if err := os.WriteFile(resolvPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	dns := newResolvConfDNS(resolvPath, j, t.Logf)
	if err := dns.apply(name, []string{"9.9.9.9"}); err != nil {
		t.Fatal(err)
	}
	// A fresh recovery instance reads the durable journal, as after a crash.
	if err := recoverLeftovers(statePath, quiet(runCmd), t.Logf); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(resolvPath)
	if err != nil || string(content) != original {
		t.Fatal("DNS was not restored", err)
	}
	for _, address := range []string{"203.0.113.9", "198.51.100.3"} {
		out, err := runCmd([]string{"ip", "-4", "route", "get", address})
		if err != nil || !strings.Contains(out, "dev "+physical) {
			t.Fatalf("restored route to %s = %s, %v", address, out, err)
		}
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("recovery left a network journal", err)
	}
}
