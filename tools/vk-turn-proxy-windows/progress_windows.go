// SPDX-License-Identifier: MIT

package main

import (
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
		phase = "Ready"
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
	next := progressStateFromLog(a.connectionState, line)
	if next != a.connectionState {
		a.setConnectionState(next)
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
	scale := a.progress.IntFrom96DPI
	gap := scale(8)
	width := max(1, (bounds.Width-3*gap)/4)
	stage := connectionStage(a.connectionState)
	labels := []string{"API", "Relay", "WireGuard", "Ready"}
	if a.preferences.Language == "ru" {
		labels = []string{"API", "Ретранслятор", "WireGuard", "Готово"}
	}
	for index, label := range labels {
		segment := walk.Rectangle{X: index * (width + gap), Y: scale(8), Width: width, Height: scale(6)}
		color := a.palette.border
		textColor := a.palette.muted
		if index < stage || a.connectionState == "connected" {
			color, textColor = a.palette.success, a.palette.success
		} else if index == stage {
			color, textColor = a.palette.accent, a.palette.accent
		}
		brush, err := walk.NewSolidColorBrush(color)
		if err != nil {
			return err
		}
		err = canvas.FillRoundedRectanglePixels(brush, segment, walk.Size{Width: scale(6), Height: scale(6)})
		brush.Dispose()
		if err != nil {
			return err
		}
		if index == stage && a.connectionState != "connected" {
			// A small moving highlight keeps the active stage readable while waiting.
			highlight := segment
			highlight.Width = max(scale(10), width/5)
			travel := max(1, width-highlight.Width)
			offset := (a.progressFrame * scale(2)) % (2 * travel)
			if offset > travel {
				offset = 2*travel - offset
			}
			highlight.X += offset
			glow, err := walk.NewSolidColorBrush(progressHighlight(color))
			if err != nil {
				return err
			}
			err = canvas.FillRoundedRectanglePixels(glow, highlight, walk.Size{Width: scale(6), Height: scale(6)})
			glow.Dispose()
			if err != nil {
				return err
			}
		}
		textBounds := walk.Rectangle{X: segment.X, Y: scale(22), Width: width, Height: scale(24)}
		if err := canvas.DrawTextPixels(label, a.progress.Font(), textColor, textBounds, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine|walk.TextNoPrefix); err != nil {
			return err
		}
	}
	return nil
}

func progressHighlight(color walk.Color) walk.Color {
	return walk.RGB(byte(uint32(color)&255)/2+127, byte((uint32(color)>>8)&255)/2+127, byte((uint32(color)>>16)&255)/2+127)
}
