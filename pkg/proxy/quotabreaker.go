package proxy

import (
	"log"
	"time"
)

// quotaBreaker retains refusal statistics. Retry timing is owned by the
// connection loop and never disables cached credentials or fresh fetches.
type quotaBreaker struct {
	refusals int64
}

// noteAllocated records a successful allocation on creds, the credential the
// caller leased from slot — the evidence that the relay accepts this
// identity, so a later 486 on it is its quota, not a refusal. Native calls it
// the moment Allocate() succeeds (runTURN, setupSRTPSession — before
// CreatePermission and any handshake); csqtt through Credential.Allocated
// right after the relay dial. The slot number alone does not name the
// credential (the doc block above): the mark is checked under the lock
// against the credential the slot holds NOW, and a late mark for one the
// slot no longer holds is dropped with a log line. Nil-safe: the transport
// helpers run in tests on a Proxy without a pool.
func (cp *credPool) noteAllocated(slot int, creds *TURNCreds) {
	if cp == nil || slot < 0 || creds == nil || creds.Username == "" {
		return
	}
	late := false
	cp.mu.Lock()
	if slot < cp.size && slot < len(cp.pool) {
		if now := cp.pool[slot].creds; now != nil && now.Username == creds.Username {
			cp.pool[slot].allocated++
		} else {
			late = true
		}
	}
	cp.mu.Unlock()
	if late {
		log.Printf("credpool: a late allocation success on slot %d names a credential the slot no longer holds — not counted (the slot was refilled while the relay's answer was in flight)", slot)
	}
}

// noteStaleQuotaRefusalLocked counts a response for a replaced credential.
// Caller holds cp.mu.
func (cp *credPool) noteStaleQuotaRefusalLocked() {
	cp.quota.refusals++
}

// noteQuotaRefusalLocked records an actual TURN error without a local pause.
// Caller holds cp.mu.
func (cp *credPool) noteQuotaRefusalLocked(_ int, _ time.Time) {
	cp.quota.refusals++
}

// quotaSnapshot preserves the statistics contract. There is no minting pause.
func (cp *credPool) quotaSnapshot() (refusals int64, paused time.Duration) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.quota.refusals, 0
}
