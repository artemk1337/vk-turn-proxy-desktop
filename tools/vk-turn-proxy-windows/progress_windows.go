// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
)

func (a *application) setConnectionState(state string) {
	a.connectionState = state
	a.updateConnectionState()
}

func (a *application) updateConnectionState() {
	status := "Disconnected"
	phase := ""
	color := a.palette.muted
	switch a.connectionState {
	case "credentials":
		status = "Connecting…"
		phase = "Fetching VK access"
		color = a.palette.accent
	case "relay":
		status = "Connecting…"
		phase = "Connecting to VK relay"
		color = a.palette.accent
	case "handshake":
		status = "Connecting…"
		phase = "Establishing WireGuard tunnel"
		color = a.palette.accent
	case "connected":
		status = "Connected"
		color = a.palette.success
	case "disconnecting":
		status = "Disconnecting…"
		phase = "Restoring network settings"
	case "failed":
		status = "Connection failed"
		color = a.palette.danger
	}
	a.status.SetText(a.tr(status))
	a.status.SetTextColor(color)
	a.phaseLabel.SetText(a.tr(phase))
	a.phaseLabel.SetTextColor(a.palette.muted)
	a.progress.Invalidate()
	if a.tray != nil {
		a.tray.update()
	}
}

func (a *application) progressFromLog(line string) {
	if !a.disconnect.Enabled() {
		return
	}
	switch {
	case strings.Contains(line, "DEFAULT ROUTE → the tunnel"):
		a.setConnectionState("connected")
	case strings.Contains(line, "proxy: first connection up"), strings.Contains(line, "tunnel:"):
		a.setConnectionState("handshake")
	case strings.Contains(line, "TURN relay allocated:"):
		if a.connectionState == "credentials" {
			a.setConnectionState("relay")
		}
	}
}

func (a *application) animateProgress() {
	done := make(chan struct{})
	a.window.Disposing().Attach(func() { close(done) })
	var pending atomic.Bool
	go func() {
		timer := time.NewTicker(60 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
				if !pending.CompareAndSwap(false, true) {
					continue
				}
				a.window.Synchronize(func() {
					defer pending.Store(false)
					select {
					case <-done:
						return
					default:
					}
					switch a.connectionState {
					case "credentials", "relay", "handshake", "disconnecting":
						a.progressFrame++
						a.progress.Invalidate()
					}
				})
			}
		}
	}()
}

func (a *application) paintProgress(canvas *walk.Canvas, _ walk.Rectangle) error {
	bounds := a.progress.ClientBoundsPixels()
	track, _ := walk.NewSolidColorBrush(a.palette.border)
	defer track.Dispose()
	canvas.FillRoundedRectanglePixels(track, bounds, walk.Size{Width: 6, Height: 6})
	if a.connectionState == "" || a.connectionState == "disconnected" || a.connectionState == "failed" {
		return nil
	}
	color := a.palette.accent
	segment := bounds
	if a.connectionState == "connected" {
		color = a.palette.success
	} else {
		segment.Width = max(40, bounds.Width/4)
		cycle := max(1, 2*(bounds.Width-segment.Width))
		offset := (a.progressFrame * 9) % cycle
		if offset > bounds.Width-segment.Width {
			offset = cycle - offset
		}
		segment.X = offset
	}
	fill, _ := walk.NewSolidColorBrush(color)
	defer fill.Dispose()
	return canvas.FillRoundedRectanglePixels(fill, segment, walk.Size{Width: 6, Height: 6})
}
