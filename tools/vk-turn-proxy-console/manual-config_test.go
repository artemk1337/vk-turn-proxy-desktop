// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManualConfigFlagsUseSharedWireGuardParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.conf")
	config := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.66.0.2/32\nDNS = 1.1.1.1\n[Peer]\nPublicKey = %s\nEndpoint = original.example:56000\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 0\n", testKey(1), testKey(2))
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"srtp", "dtls"} {
		o, err := parseFlags([]string{"-wg-config", path, "-vk-link", "https://vk.ru/call/join/test", "-peer", "relay.example:56000", "-server-transport", transport, "-turn-transport", "udp"})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := loadWireGuardConfig(o)
		if err != nil {
			t.Fatal(err)
		}
		server, err := profile.selectedServer("")
		if err != nil {
			t.Fatal(err)
		}
		settings := buildSettings(profile, server)
		if _, err := o.applyTo(&settings); err != nil {
			t.Fatal(err)
		}
		if err := settings.validate(); err != nil {
			t.Fatal(err)
		}
		if settings.UseSrtp != (transport == "srtp") || !settings.UseUDP || settings.PersistentKeepalive != 0 || settings.PeerAddress != "relay.example:56000" {
			t.Fatal("manual transport, endpoint or keepalive was lost")
		}
	}
}

func TestManualConfigFlagsRejectConflictingInputModes(t *testing.T) {
	for _, args := range [][]string{
		{"-wg-config", "secret.conf", "-config", "secret.json"},
		{"-wg-config", "secret.conf", "-wizard"},
		{"-server-transport", "dtls"},
		{"-vk-link-file", "call.txt"},
		{"-wg-config", "secret.conf", "-vk-link-file", "call.txt", "-vk-link", "https://vk.ru/call/join/test"},
		{"-wg-config", "secret.conf", "-server-transport", "udp"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Fatalf("accepted conflicting flags %v", args)
		}
	}
}

func TestManualConfigReadsPrivateLinkFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "connection.conf")
	config := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.66.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = relay.example:56000\nAllowedIPs = 0.0.0.0/0\n", testKey(1), testKey(2))
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "call.txt")
	if err := os.WriteFile(linkPath, []byte("https://vk.ru/call/join/private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	o, err := parseFlags([]string{"-wg-config", path, "-vk-link-file", linkPath})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := loadWireGuardConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	server, err := profile.selectedServer("")
	if err != nil {
		t.Fatal(err)
	}
	settings := buildSettings(profile, server)
	if len(settings.VKLinks) != 1 || settings.VKLinks[0] != "https://vk.ru/call/join/private" {
		t.Fatal("private VK link was lost")
	}
}
