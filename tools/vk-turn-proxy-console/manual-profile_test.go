package main

import (
	"fmt"
	"testing"

	wgprofile "github.com/artemk1337/vk-turn-proxy-desktop/pkg/wireguard-profile"
)

func TestManualWireGuardProfileReachesConsoleSettings(t *testing.T) {
	config := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.66.0.2/32\nDNS = 1.1.1.1\nMTU = 1280\n[Peer]\nPublicKey = %s\nEndpoint = example.com:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 30\n", testKey(1), testKey(2))
	for _, srtp := range []bool{true, false} {
		raw, err := wgprofile.Build(wgprofile.Options{Config: config, VKLink: "https://vk.ru/call/join/test", PeerAddress: "example.com:56000", UseSrtp: srtp})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := parseProfile(raw)
		if err != nil {
			t.Fatal(err)
		}
		server, err := profile.selectedServer("")
		if err != nil {
			t.Fatal(err)
		}
		settings := buildSettings(profile, server)
		if err := settings.validate(); err != nil {
			t.Fatal(err)
		}
		if settings.PersistentKeepalive != 30 || settings.PeerAddress != "example.com:56000" || settings.UseSrtp != srtp || !settings.UseDTLS || settings.MTU != 1280 {
			t.Fatal("manual profile settings were lost")
		}
	}
}
