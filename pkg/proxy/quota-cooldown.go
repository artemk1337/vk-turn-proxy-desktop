package proxy

import (
	"fmt"
	"time"
)

const credSaturationCooldown = 10 * time.Minute

// QuotaCooldownError reports uncertainty about allocations left by a previous
// process. It carries a local deadline, not a fabricated TURN server response.
type QuotaCooldownError struct {
	Until time.Time
}

func (e *QuotaCooldownError) Error() string {
	return fmt.Sprintf("previous VK allocations may still be active; quota cooldown remaining %s", max(0, time.Until(e.Until)).Round(time.Second))
}

type quotaHistoryKey struct {
	username string
	relay    string
}

func (cp *credPool) quotaHistoryKey(creds *TURNCreds, relay string) quotaHistoryKey {
	username := creds.Username
	if cp.mode == credCacheModeCookie {
		username = ""
	}
	return quotaHistoryKey{username: username, relay: relay}
}

// restoreQuotaHistoryLocked retains uncertainty across interrupted restarts.
// Cookie credentials share quota on a relay even when VK issues a new username.
func (cp *credPool) restoreQuotaHistoryLocked(file credCacheFile, released bool, now time.Time) {
	cp.cacheCooldowns = make(map[quotaHistoryKey]time.Time)
	cp.relayUsedAt = make(map[string]time.Time)
	if released {
		return
	}
	restore := func(creds *TURNCreds, relay string, unix int64) {
		if relay == "" || unix <= 0 {
			return
		}
		used := time.Unix(unix, 0)
		if used.After(now) {
			used = now
		}
		until := used.Add(credSaturationCooldown)
		if !until.After(now) {
			return
		}
		key := cp.quotaHistoryKey(creds, relay)
		if until.After(cp.cacheCooldowns[key]) {
			cp.cacheCooldowns[key] = until
		}
		if until.After(cp.inheritedUntil) {
			cp.inheritedUntil = until
		}
		if cp.mode == credCacheModeCookie && used.After(cp.relayUsedAt[relay]) {
			cp.relayUsedAt[relay] = used
		}
	}
	if cp.mode == credCacheModeCookie && len(file.RelayUsedAt) > 0 {
		for relay, at := range file.RelayUsedAt {
			restore(&TURNCreds{}, relay, at)
		}
		return
	}
	for _, entry := range file.Creds {
		creds := &TURNCreds{Username: entry.Username}
		if len(entry.UsedOn) > 0 {
			for relay, at := range entry.UsedOn {
				restore(creds, relay, at)
			}
		} else {
			relay := "*"
			if cp.mode == credCacheModeCookie {
				relay = entry.Address
			}
			restore(creds, relay, entry.LastUsedAt)
		}
	}
}

func (cp *credPool) restoreEntryHistoryLocked(entry credCacheEntry, released bool) {
	if released || cp.mode == credCacheModeCookie {
		return
	}
	for relay, at := range entry.UsedOn {
		if relay == "*" {
			cp.pool[entry.Slot].lastUsedAt = time.Unix(at, 0)
		} else {
			cp.histPtrLocked(entry.Slot, relay).lastUsedAt = time.Unix(at, 0)
		}
	}
	if len(entry.UsedOn) == 0 && entry.LastUsedAt > 0 {
		cp.pool[entry.Slot].lastUsedAt = time.Unix(entry.LastUsedAt, 0)
	}
}

func (cp *credPool) quotaReadyAtLocked(slot int, relay string) time.Time {
	if slot < 0 || slot >= len(cp.pool) || cp.pool[slot].creds == nil || cp.override.unlimited(relay) {
		return time.Time{}
	}
	key := cp.quotaHistoryKey(cp.pool[slot].creds, relay)
	until := cp.cacheCooldowns[key]
	key.relay = "*"
	if wildcard := cp.cacheCooldowns[key]; wildcard.After(until) {
		until = wildcard
	}
	return until
}

func (cp *credPool) nextQuotaReadyLocked(connIdx int) time.Time {
	var earliest time.Time
	now := time.Now()
	for slot := range cp.pool {
		if !entryCredValid(cp.pool[slot]) {
			continue
		}
		until := cp.quotaReadyAtLocked(slot, cp.peekAddrLocked(slot, connIdx))
		if until.After(now) && (earliest.IsZero() || until.Before(earliest)) {
			earliest = until
		}
	}
	return earliest
}

// Release history is persisted, while a live process keeps using its own
// confirmed lease accounting instead of delaying healthy connections.
func (cp *credPool) noteQuotaReleaseLocked(creds *TURNCreds, relay string, at time.Time) {
	if cp.mode != credCacheModeCookie {
		return
	}
	if relay == "" {
		relay = creds.Address
	}
	if relay == "" {
		return
	}
	if cp.relayUsedAt == nil {
		cp.relayUsedAt = make(map[string]time.Time)
	}
	if at.After(cp.relayUsedAt[relay]) {
		cp.relayUsedAt[relay] = at
	}
}

func (cp *credPool) noteRelayUsedLocked(slot int, at time.Time) {
	cp.noteQuotaReleaseLocked(cp.pool[slot].creds, cp.dialNowLocked(slot), at)
}
