// SPDX-License-Identifier: MIT

package wgprofile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func testKey(seed byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

func testConfig() string {
	return "[Interface]\nPrivateKey = " + testKey(1) + "\nAddress = 10.0.0.2/24, fd00::2/64\nDNS = 1.1.1.1, 8.8.8.8\nMTU = 1280\n[Peer]\nPublicKey = " + testKey(2) + "\nPresharedKey = " + testKey(3) + "\nEndpoint = example.com:56000\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n"
}

func buildSettings(t *testing.T, o Options) map[string]any {
	t.Helper()
	raw, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Version  int            `json:"version"`
		Type     string         `json:"type"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Version != 1 || profile.Type != "connection" {
		t.Fatal("unexpected connection envelope")
	}
	return profile.Settings
}

func TestBuildPreservesWireGuardSettings(t *testing.T) {
	s := buildSettings(t, Options{Config: testConfig(), VKLink: "https://vk.com/call/join/token", UseSrtp: true})
	want := map[string]any{"privateKey": testKey(1), "peerPublicKey": testKey(2), "presharedKey": testKey(3), "tunnelAddress": "10.0.0.2/24", "peerAddress": "example.com:56000", "dnsServers": "1.1.1.1, 8.8.8.8", "tunnelMTU": float64(1280), "persistentKeepalive": float64(25), "useSrtp": true, "useDTLS": true, "useUDP": false, "vkLink": "https://vk.ru/call/join/token"}
	for key, value := range want {
		if s[key] != value {
			t.Errorf("%s differs", key)
		}
	}
}

func TestBuildAcceptsWireGuardFormattingAndEndpointOverride(t *testing.T) {
	config := "\ufeff# profile\r\n" + strings.ReplaceAll(testConfig(), "\n", "\r\n")
	config = strings.Replace(config, "PrivateKey =", "\tPrivateKey\t=", 1)
	config = strings.Replace(config, "MTU = 1280", "MTU = 1280 # tunnel size", 1)
	config = strings.Replace(config, "PersistentKeepalive = 25", "PersistentKeepalive = 0 ; disabled", 1)
	s := buildSettings(t, Options{Config: config, VKLink: "m.vk.ru/call/join/token?source=app", PeerAddress: "[2001:db8::1]:56000"})
	if s["peerAddress"] != "[2001:db8::1]:56000" || s["persistentKeepalive"] != float64(0) || s["useSrtp"] != false {
		t.Fatal("override, zero keepalive, or legacy transport was lost")
	}
}

func TestBuildDefaultsOptionalFields(t *testing.T) {
	config := "[Interface]\nPrivateKey=" + testKey(1) + "\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=" + testKey(2)
	s := buildSettings(t, Options{Config: config, VKLink: "https://vk.me/join/token", PeerAddress: "192.0.2.1:56000", UseSrtp: true})
	for _, field := range []string{"presharedKey", "dnsServers", "tunnelMTU"} {
		if _, exists := s[field]; exists {
			t.Errorf("absent %s was emitted", field)
		}
	}
	if s["persistentKeepalive"] != float64(0) {
		t.Fatal("WireGuard's absent keepalive must stay disabled")
	}
}

func TestBuildAcceptsDisabledKeepalive(t *testing.T) {
	config := strings.Replace(testConfig(), "PersistentKeepalive = 25", "PersistentKeepalive = off", 1)
	s := buildSettings(t, Options{Config: config, VKLink: "https://vk.ru/call/join/token", UseSrtp: true})
	if s["persistentKeepalive"] != float64(0) {
		t.Fatal("disabled keepalive was changed")
	}
}

func TestBuildRejectsInvalidOrUnsupportedConfig(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"private key", testKey(1), "SECRET_INVALID_PRIVATE_KEY"},
		{"public key", testKey(2), "invalid"},
		{"preshared key", testKey(3), "invalid"},
		{"invalid address", "10.0.0.2/24", "not-an-address"},
		{"ipv6 only", "10.0.0.2/24, fd00::2/64", "fd00::2/64"},
		{"narrow routes", "0.0.0.0/0, ::/0", "10.0.0.0/8"},
		{"ipv6 routes only", "0.0.0.0/0, ::/0", "::/0"},
		{"DNS search domain", "1.1.1.1, 8.8.8.8", "home.internal"},
		{"IPv6 DNS", "1.1.1.1, 8.8.8.8", "2606:4700:4700::1111"},
		{"bad port", "example.com:56000", "example.com:0"},
		{"service port", "example.com:56000", "example.com:https"},
		{"endpoint URL", "example.com:56000", "https://example.com:56000"},
		{"multiple peers", "[Peer]", "[Peer]\n[Peer]"},
		{"duplicate setting", "MTU = 1280", "MTU = 1280\nMTU = 1200"},
		{"hook", "MTU = 1280", "PostUp = SECRET_COMMAND"},
		{"listen port", "MTU = 1280", "ListenPort = 51820"},
		{"MTU too large", "MTU = 1280", "MTU = 1420"},
		{"MTU too small", "MTU = 1280", "MTU = 999"},
		{"negative keepalive", "PersistentKeepalive = 25", "PersistentKeepalive = -1"},
		{"overflow keepalive", "PersistentKeepalive = 25", "PersistentKeepalive = 65536"},
		{"NUL", "MTU = 1280", "MTU = 1280\x00"},
		{"unknown section", "[Peer]", "[SecretSection]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := strings.Replace(testConfig(), tc.old, tc.replacement, 1)
			_, err := Build(Options{Config: config, VKLink: "https://vk.ru/call/join/token", UseSrtp: true})
			if err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			for _, secret := range []string{testKey(1), testKey(2), testKey(3), "SECRET_COMMAND", "SECRET_INVALID_PRIVATE_KEY"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("error exposed configuration content")
				}
			}
		})
	}
}

func TestVKCallLinks(t *testing.T) {
	for _, link := range []string{"https://vk.com/call/join/token", "http://m.vk.ru/call/join/token", "vk.me/join/token", "https://vk.ru/call/join/token?source=app#section"} {
		if _, err := callLink(link); err != nil {
			t.Errorf("supported VK link was rejected: %v", err)
		}
	}
	for _, link := range []string{"", "https://evil.test/call/join/token", "https://vk.com.evil.test/call/join/token", "https://evil@vk.com/call/join/token", "https://vk.com/id1", "https://vk.com/call/join/", "https://vk.com/call/join/one/two", "https://vk.com/call/join/token%0a", "https://vk.com/call/join/token%00", "https://vk.me/join/%2f"} {
		if _, err := callLink(link); err == nil {
			t.Errorf("invalid VK link was accepted")
		}
	}
}
