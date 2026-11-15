package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A pool whose fetcher mints a fresh credential on demand and counts the mints.
func breakerPool(t *testing.T, mints *atomic.Int32) *credPool {
	t.Helper()
	const relay = "203.0.113.11:19302"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cp := newCredPool(ctx, 12, 2*time.Minute, "", func(_ bool, slot int) (string, *TURNCreds, error) {
		n := mints.Add(1)
		return relay, &TURNCreds{Username: fmt.Sprintf("%d:mint-%d-slot-%d", time.Now().Add(8*time.Hour).Unix(), n, slot),
			Password: "p", Address: relay, Addresses: []string{relay}}, nil
	})
	cp.mu.Lock()
	for len(cp.pool) < cp.size {
		cp.pool = append(cp.pool, credPoolEntry{})
	}
	cp.mu.Unlock()
	return cp
}

// heldBy is the credential slot holds now — what a holder that leased it names
// when it tells the pool of a 486 or a 401 on it (the pool acts only on a
// credential the slot still holds).
func heldBy(cp *credPool, slot int) *TURNCreds {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if slot < 0 || slot >= len(cp.pool) {
		return nil
	}
	return cp.pool[slot].creds
}

// mintFor is get() for connIdx when it is expected to MINT: the slot it
// landed on, or a fatal.
func mintFor(t *testing.T, cp *credPool, connIdx int) int {
	t.Helper()
	_, _, slot, err := cp.get(connIdx, false)
	if err != nil {
		t.Fatalf("get(%d) = %v, want a mint", connIdx, err)
	}
	return slot
}

func TestQuotaRefusalsKeepCredentialsImmediatelyAvailable(t *testing.T) {
	for _, age := range []time.Duration{0, 2 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			var mints atomic.Int32
			cp := breakerPool(t, &mints)
			slot := mintFor(t, cp, 0)
			creds := heldBy(cp, slot)
			cp.release(slot, creds, "")
			cp.mu.Lock()
			cp.pool[slot].ts = time.Now().Add(-age)
			cp.mu.Unlock()
			for i := 0; i < 20; i++ {
				if delay := cp.markSaturated(slot, creds, ""); delay != 0 {
					t.Fatalf("quota refusal imposed local delay %s", delay)
				}
			}
			if refusals, pause := cp.quotaSnapshot(); refusals != 20 || pause != 0 {
				t.Fatalf("quota statistics = %d refusals, pause %s", refusals, pause)
			}
			_, next, nextSlot, err := cp.get(0, false)
			if err != nil || nextSlot != slot || next.Username != creds.Username || mints.Load() != 1 {
				t.Fatalf("retry reused slot %d, mints %d, error %v", nextSlot, mints.Load(), err)
			}
		})
	}
}

func TestQuotaRefusalsPreserveActiveSeatAccounting(t *testing.T) {
	var mints atomic.Int32
	cp := breakerPool(t, &mints)
	slot := mintFor(t, cp, 0)
	creds := heldBy(cp, slot)
	for conn := 1; conn < connsPerSlot; conn++ {
		if got := mintFor(t, cp, conn); got != slot {
			t.Fatalf("connection %d used slot %d", conn, got)
		}
	}
	cp.markSaturated(slot, creds, "")
	cp.markSaturated(slot, creds, "")
	cp.mu.Lock()
	active := cp.pool[slot].active
	cp.mu.Unlock()
	if active != connsPerSlot {
		t.Fatalf("active seats = %d, want %d", active, connsPerSlot)
	}
	if next := mintFor(t, cp, connsPerSlot); next == slot || mints.Load() != 2 {
		t.Fatalf("full slot %d acquired again as %d, mints %d", slot, next, mints.Load())
	}
}

// The success marks sit at the two native ALLOCATION sites — pinned by
// spelling. Build 391 put them at the four "session established" lines, and
// the user's control stand showed what that measures: session readiness, not
// the relay's acceptance — a real TURN accepting nine allocations per
// credential and refusing the tenth, the SRTP handshake delayed: allocated
// 0,0 and a 30-second pause for a plain quota (and direct's line comes
// BEFORE its allocation: a mark with nothing behind it). proxy.go allocates
// in exactly two places, runTURN (DTLS, direct, WRAP-A) and setupSRTPSession
// (SRTP); each Allocate() is followed by the mark before its "TURN relay
// allocated" line and before CreatePermission; no "session established" line
// carries one. Since 2026-10-19 the mark is relayAllocatedAt(slot, creds,
// turnAddr) — noteAllocated plus the relay health's evidence (relayhealth.go)
// and the relay IP for the next connection.
// Sabotage seen red: either mark dropped; the marks moved back to "session
// established"; a mark placed after CreatePermission.
func TestTheSuccessMarksSitAtTheTURNAllocationSites(t *testing.T) {
	src, err := os.ReadFile("proxy.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	var allocs []int
	for i := 0; ; {
		j := strings.Index(code[i:], ".Allocate()")
		if j < 0 {
			break
		}
		allocs = append(allocs, i+j)
		i += j + 1
	}
	if len(allocs) != 2 {
		t.Fatalf("proxy.go has %d .Allocate() sites, want 2 (runTURN, setupSRTPSession) — a new one needs the mark too", len(allocs))
	}
	for _, a := range allocs {
		after := code[a:]
		logAt := strings.Index(after, "TURN relay allocated")
		if logAt < 0 {
			t.Fatalf("no \"TURN relay allocated\" line after the Allocate at byte %d", a)
		}
		span := after[:logAt]
		if n := strings.Count(span, "p.relayAllocatedAt("); n != 1 {
			t.Errorf("Allocate at byte %d: %d relayAllocatedAt calls between it and its \"TURN relay allocated\" line, want exactly 1 — the mark is the allocation", a, n)
		}
		if perm := strings.Index(span, "CreatePermission("); perm >= 0 && strings.Index(span, "p.relayAllocatedAt(") > perm {
			t.Errorf("Allocate at byte %d: the mark sits after CreatePermission — a permission failure is not the relay refusing the identity", a)
		}
	}
	if n := strings.Count(code, "p.relayAllocatedAt("); n != 2 {
		t.Errorf("proxy.go: %d relayAllocatedAt calls, want 2 — one per allocation site, none at a session line", n)
	}
	if !strings.Contains(code, "p.credPool.noteAllocatedAt(slot, creds, turnAddr, attempt)") || strings.Count(code, "noteAllocated(") != 0 {
		t.Error("proxy.go: the mark reaches the pool through relayAllocatedAt → noteAllocatedAt (the credential AND the relay that answered), nowhere else")
	}
	// The mark carries the LEASED credential, not the slot number alone: the
	// slot may have been refilled while the relay's answer was in flight
	// (the user's stand on 392; allocmark_identity_test.go).
	for _, call := range []string{"relayAllocatedAt(slotIdx, creds, turnAddr, attempt)", "relayAllocatedAt(credSlot, creds, turnAddr, attempt)"} {
		if !strings.Contains(code, call) {
			t.Errorf("proxy.go lacks %q — the mark must name the credential the session leased", call)
		}
	}
	for _, lit := range []string{"DTLS+TURN session established", "direct TURN session established", "WRAP-A+TURN session established", "SRTP+TURN session established"} {
		i := strings.Index(code, lit)
		if i < 0 {
			t.Fatalf("proxy.go no longer logs %q", lit)
		}
		if strings.Contains(code[max(0, i-300):i], "noteAllocated(") {
			t.Errorf("%q is preceded by a noteAllocated call — that is session readiness, not the allocation (391's mistake)", lit)
		}

	}
}
