package proxy

import (
	"slices"
	"time"
)

// wakeCachedCredentials broadcasts when inherited quota clocks expire. These
// clocks have no markSaturated timer, but parked connections wait on the same
// pool channel. Capture every relay so overrides can change the dial target.
func (cp *credPool) wakeCachedCredentials() {
	cp.mu.Lock()
	var deadlines []time.Time
	now := time.Now()
	for slot := range cp.pool {
		for _, relay := range cp.candidatesLocked(slot) {
			if deadline := cp.clockLocked(slot, relay); deadline.After(now) {
				deadlines = append(deadlines, deadline)
			}
		}
	}
	cp.mu.Unlock()
	slices.SortFunc(deadlines, func(a, b time.Time) int { return a.Compare(b) })
	deadlines = slices.Compact(deadlines)
	if len(deadlines) == 0 {
		return
	}
	go func() {
		for _, deadline := range deadlines {
			timer := time.NewTimer(time.Until(deadline))
			select {
			case <-cp.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				if cp.ctx.Err() != nil {
					return
				}
				cp.broadcastSlotAvailable()
			}
		}
	}()
}
