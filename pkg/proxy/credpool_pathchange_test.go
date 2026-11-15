package proxy

// Cached credentials remain usable when the physical network changes.

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pathChangedPool provides valid cached credentials with an optional empty slot.
func pathChangedPool(t *testing.T, slot3Empty bool) (*credPool, *atomic.Int32) {
	t.Helper()
	var mints atomic.Int32
	fetch := func(_ bool, slot int) (string, *TURNCreds, error) {
		mints.Add(1)
		addr := "203.0.113.11:19302"
		return addr, &TURNCreds{Username: fmt.Sprintf("%d:minted-%d", time.Now().Add(8*time.Hour).Unix(), slot),
			Password: "p", Address: addr, Addresses: []string{addr}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cp := newCredPool(ctx, 12, 2*time.Minute, "", fetch)
	cp.mu.Lock()
	for len(cp.pool) < cp.size {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	now := time.Now()
	for i := range cp.pool {
		creds := &TURNCreds{Username: fmt.Sprintf("%d:loaded-%d", now.Add(8*time.Hour).Unix(), i), Password: "p",
			Address: "203.0.113.11:19302", Addresses: []string{"203.0.113.11:19302"}}
		switch i {
		case 0, 4, 5:
			cp.pool[i] = credPoolEntry{addr: creds.Address, creds: creds, ts: now.Add(-3 * time.Minute)}
		case 3:
			if slot3Empty {
				cp.pool[i] = credPoolEntry{}
			} else {
				cp.pool[i] = credPoolEntry{addr: creds.Address, creds: creds, ts: now.Add(-5 * time.Minute)}
			}
		default:
			cp.pool[i] = credPoolEntry{addr: creds.Address, creds: creds, ts: now.Add(-5 * time.Minute)}
		}
	}
	cp.mu.Unlock()
	return cp, &mints
}

func TestPathChangeReusesCachedCredentialsBeforeMinting(t *testing.T) {
	cp, mints := pathChangedPool(t, true)
	cp.MarkInUseSlotsForPathChange()
	cp.mu.Lock()
	cp.pauseAcquireUntil = time.Time{}
	cp.mu.Unlock()
	_, creds, slot, err := cp.get(0, false)
	if err != nil || creds == nil || slot != 0 || !strings.HasSuffix(creds.Username, "loaded-0") || mints.Load() != 0 {
		t.Fatalf("path change retry = slot %d, credentials %v, mints %d, error %v", slot, creds != nil, mints.Load(), err)
	}
}

func TestPathChangeKeepsCredentialsWithActiveSeats(t *testing.T) {
	cp, mints := pathChangedPool(t, false)
	cp.mu.Lock()
	var names []string
	for i := range cp.pool {
		names = append(names, cp.pool[i].creds.Username)
		seatHolders(cp, i, connsPerSlot)
	}
	cp.mu.Unlock()
	cp.MarkInUseSlotsForPathChange()
	cp.mu.Lock()
	cp.pauseAcquireUntil = time.Time{}
	cp.mu.Unlock()
	if _, _, _, err := cp.get(0, false); err == nil {
		t.Fatal("full active credential pool admitted another seat")
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for i := range cp.pool {
		if cp.pool[i].creds.Username != names[i] || cp.pool[i].active != connsPerSlot {
			t.Fatalf("active credential in slot %d was replaced", i)
		}
	}
	if mints.Load() != 0 {
		t.Fatal("path change minted over active credentials")
	}
}
