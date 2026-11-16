// SPDX-License-Identifier: MIT

package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/artemk1337/vk-turn-proxy-desktop/pkg/proxy/srtpwrap"
)

func TestHandshakeTimeoutDefaultsAndOverrides(t *testing.T) {
	for _, timeout := range []time.Duration{0, 60 * time.Second} {
		p := NewProxy(Config{
			SRTPHandshakeTimeout:  timeout,
			DTLSHandshakeTimeout:  timeout,
			WrapAHandshakeTimeout: timeout,
		})
		p.cancel()
		want := []time.Duration{srtpwrap.HandshakeTimeout, DefaultDTLSHandshakeTimeout, DefaultWrapAHandshakeTimeout}
		if timeout > 0 {
			want = []time.Duration{timeout, timeout, timeout}
		}
		got := []time.Duration{p.config.SRTPHandshakeTimeout, p.config.DTLSHandshakeTimeout, p.config.WrapAHandshakeTimeout}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("timeout %s: transport %d got %s, want %s", timeout, i, got[i], want[i])
			}
		}
	}
}

func TestTransportHandshakeTimeoutAndCancellation(t *testing.T) {
	for name, dial := range map[string]func(context.Context, net.PacketConn, *net.UDPAddr, time.Duration) (net.Conn, error){
		"DTLS":   dialDTLS,
		"WRAP-A": dialDTLSWrapA,
		"SRTP": func(ctx context.Context, transport net.PacketConn, peer *net.UDPAddr, timeout time.Duration) (net.Conn, error) {
			return srtpwrap.ClientWithHandshakeTimeout(ctx, transport, peer, nil, timeout)
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, canceled := range []bool{false, true} {
				transport, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = transport.Close() })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				timeout := 80 * time.Millisecond
				if canceled {
					timeout = time.Minute
					cancel()
				}
				started := time.Now()
				conn, err := dial(ctx, transport, silentPeer(t), timeout)
				elapsed := time.Since(started)
				if conn != nil {
					_ = conn.Close()
				}
				if err == nil {
					t.Fatal("silent peer completed the handshake")
				}
				if elapsed > 2*time.Second {
					t.Fatalf("handshake ignored its deadline or cancellation: %s", elapsed)
				}
				if !canceled && elapsed < timeout {
					t.Fatalf("handshake returned before its configured timeout: %s", elapsed)
				}
			}
		})
	}
}

func TestSRTPHandshakeCanWaitBeyondLegacyTimeout(t *testing.T) {
	transport, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Second)
	defer cancel()
	started := time.Now()
	conn, err := srtpwrap.ClientWithHandshakeTimeout(ctx, transport, silentPeer(t), nil, time.Minute)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("silent peer completed the handshake")
	}
	if elapsed := time.Since(started); elapsed < 11*time.Second {
		t.Fatalf("handshake ignored the longer timeout: %s", elapsed)
	}
}
