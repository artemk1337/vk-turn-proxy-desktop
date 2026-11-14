package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartupHandshakesWithoutPersistentKeepalive(t *testing.T) {
	client, key, sent := wgPairWithKeepalive(t, 0)
	if sent.n.Load() != 0 || !readWG(client).lastHandshake.IsZero() {
		t.Fatal("fixture initiated a handshake without traffic or keepalives")
	}
	console := console{dev: client, peer: findPeer(client, key)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !console.waitHandshake(ctx) {
		t.Fatal("startup waited for traffic before initiating its handshake")
	}
	if sent.n.Load() == 0 || readWG(client).lastHandshake.IsZero() {
		t.Fatal("startup did not complete the WireGuard handshake")
	}
	state, err := client.IpcGet()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "persistent_keepalive_interval=0") {
		t.Fatal("startup changed the profile keepalive")
	}
}
