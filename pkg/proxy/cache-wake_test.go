package proxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedCredentialsWakeBeforeSaturatedSlot(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cp := newCredPool(ctx, 2, time.Minute, "", nil)
			cp.mode = mode
			deadline := time.Now().Add(50 * time.Millisecond)
			cp.mu.Lock()
			cp.pool[0] = credPoolEntry{addr: relayA, creds: &TURNCreds{Address: relayA, Username: futureUser("saturated"), Password: "p"}, ts: time.Now(), relayHist: relayHist{saturatedUntil: time.Now().Add(11 * time.Minute)}}
			cp.pool[1] = credPoolEntry{addr: relayB, creds: &TURNCreds{Address: relayB, Username: futureUser("pending"), Password: "p"}, ts: time.Now(), relayHist: relayHist{availableAt: deadline}}
			cp.mu.Unlock()
			wake := cp.slotAvailableChannel()
			cp.wakeCachedCredentials()
			select {
			case <-wake:
				if time.Now().Before(deadline) {
					t.Fatal("quota clock bypassed")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("pending credential did not wake the pool")
			}
			cp.mu.Lock()
			ready := cp.availableLocked(1)
			blocked := cp.availableLocked(0)
			cp.mu.Unlock()
			if !ready || blocked {
				t.Fatalf("pending ready %v, saturated ready %v", ready, blocked)
			}
			_, _, slot, err := cp.get(0, false)
			if err != nil || slot != 1 {
				t.Fatalf("retry acquired slot %d: %v", slot, err)
			}
		})
	}
}

func TestCachedCredentialWakeStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cp := newCredPool(ctx, 1, time.Minute, "", nil)
	cp.pool[0] = credPoolEntry{addr: relayA, creds: &TURNCreds{Address: relayA, Username: futureUser("pending"), Password: "p"}, ts: time.Now(), relayHist: relayHist{availableAt: time.Now().Add(30 * time.Millisecond)}}
	wake := cp.slotAvailableChannel()
	cp.wakeCachedCredentials()
	cancel()
	select {
	case <-wake:
		t.Fatal("cancelled pool broadcast readiness")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLoadedCacheSchedulesCredentialWake(t *testing.T) {
	for _, mode := range []string{credCacheModeAnon, credCacheModeCookie} {
		t.Run(mode, func(t *testing.T) {
			previousMode := cookieAuthEnabled.Swap(mode == credCacheModeCookie)
			defer cookieAuthEnabled.Store(previousMode)
			// Cache timestamps have second precision. Leave a full second after load.
			readyAt := time.Now().Truncate(time.Second).Add(2 * time.Second)
			usedAt := readyAt.Add(-credSaturationCooldown).Unix()
			file := credCacheFile{Version: credCacheVersion, Mode: mode, SavedAt: time.Now().Unix(), Creds: []credCacheEntry{
				{Slot: 0, Address: relayA, Addresses: []string{relayA}, Username: futureUser("ready"), Password: "p"},
				{Slot: 1, Address: relayB, Addresses: []string{relayB}, Username: futureUser("pending"), Password: "p", LastUsedAt: usedAt, UsedOn: map[string]int64{relayB: usedAt}},
			}}
			if mode == credCacheModeCookie {
				file.RelayUsedAt = map[string]int64{relayB: usedAt}
			}
			data, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cp := newCredPool(ctx, 2, time.Minute, path, nil)
			defer func() { cancel(); <-cp.saverDone }()
			wake := cp.slotAvailableChannel()
			cp.markSaturated(0, cp.pool[0].creds, relayA)
			select {
			case <-wake:
				if time.Now().Before(readyAt) {
					t.Fatal("woke before cached quota expired")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("constructor did not schedule pending cache wake")
			}
			cp.mu.Lock()
			ready := cp.availableLocked(1)
			cp.mu.Unlock()
			if !ready {
				t.Fatal("cached credential still unavailable")
			}
		})
	}
}
