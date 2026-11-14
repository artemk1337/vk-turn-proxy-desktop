// SPDX-License-Identifier: MIT

package main

// What the proxy is told about the network: the monitor's events in the
// credential pool's terms.
//
// A path change marks identities whose allocations remain on the old network.
// Two path changes 0.5-90 seconds apart are treated as a cascade and pause new
// acquisitions for 30 seconds. Polling can report one desktop handover as
// separate lost and restored events, so the monitor coalesces those events.
//
// So ONE handover is ONE path change:
//
//   - the network is gone: a path change — the identities in use are marked
//     now, while every session still counts as theirs;
//   - still gone: the acquires are held (there is nothing to acquire on, and
//     the end of the hold is what wakes a connection that went dormant
//     meanwhile); nothing is marked, nothing counts as an event;
//   - a network is back: the VK client dials on it and the sessions are
//     rebuilt — NOT a second path change;
//   - one network replaced by another between two readings: the whole
//     handover in one event — a path change and the rebuild.
//
// A second handover within the pool's window is still a cascade: it begins
// with its own "gone" (or its own replacement), a path change of its own.

// pathSink is the proxy's side of it — *proxy.Proxy itself in production: the
// names are the proxy's own, so that nothing stands between an event and the
// method it means.
type pathSink interface {
	WakeHealthCheck()  // a wake after a sleep: look at every session
	OnPathChange()     // a handover: mark the identities in use, judge a cascade
	OnPathTransition() // no usable path yet: hold the acquires, mark nothing
	OnPathUp()         // a network is there: rebuild the sessions on it
}

// tellPath tells the proxy what one step of the monitor saw. rotateVK is
// proxy.RotateVKSessionClient: OnPathChange rotates the VK client itself, and
// a network that comes back without a path change of its own needs the same.
func tellPath(p pathSink, rotateVK func(), ev netEvents) {
	if ev.slept > 0 {
		p.WakeHealthCheck()
	}
	switch {
	case ev.changed && !ev.up: // the network is gone — the handover begins
		p.OnPathChange()
	case !ev.changed && !ev.up: // still gone
		p.OnPathTransition()
	case ev.changed && ev.prevUp: // one network replaced by another in one reading
		p.OnPathChange()
		p.OnPathUp()
	case ev.changed: // back after "gone": the second half of that handover
		rotateVK()
		p.OnPathUp()
	}
}
