// SPDX-License-Identifier: MIT

package main

import (
	"image"
	"image/color"
	"math"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

type trayController struct {
	app                                     *application
	icon                                    *walk.NotifyIcon
	status, open, connect, disconnect, exit *walk.Action
	images                                  map[walk.Color]*walk.Icon
	color                                   walk.Color
	language, tooltip                       string
	exiting, disposed                       bool
}

func newTrayController(a *application) (*trayController, error) {
	icon, err := walk.NewNotifyIcon(a.window)
	if err != nil {
		return nil, err
	}
	t := &trayController{app: a, icon: icon, images: make(map[walk.Color]*walk.Icon)}
	t.status = walk.NewAction()
	t.status.SetEnabled(false)
	t.open = walk.NewAction()
	t.open.SetDefault(true)
	t.open.Triggered().Attach(t.showWindow)
	t.connect = walk.NewAction()
	t.connect.Triggered().Attach(func() {
		t.showWindow()
		a.start()
		t.update()
	})
	t.disconnect = walk.NewAction()
	t.disconnect.Triggered().Attach(func() {
		a.stop()
		t.update()
	})
	t.exit = walk.NewAction()
	t.exit.Triggered().Attach(t.requestExit)
	for _, action := range []*walk.Action{t.status, walk.NewSeparatorAction(), t.open, t.connect, t.disconnect, walk.NewSeparatorAction(), t.exit} {
		if err := icon.ContextMenu().Actions().Add(action); err != nil {
			t.dispose()
			return nil, err
		}
	}
	icon.MouseUp().Attach(func(_, _ int, button walk.MouseButton) {
		if button == walk.LeftButton {
			t.showWindow()
		}
	})
	if err := t.updateIcon(); err != nil {
		t.dispose()
		return nil, err
	}
	t.update()
	if err := icon.SetVisible(true); err != nil {
		t.dispose()
		return nil, err
	}
	a.window.Disposing().Attach(t.dispose)
	return t, nil
}

func (t *trayController) text(text string) string {
	if t.app.preferences.Language == "ru" {
		switch text {
		case "Open window":
			return "Открыть окно"
		case "Exit":
			return "Выход"
		}
	}
	return t.app.tr(text)
}

func (t *trayController) update() {
	if t.disposed {
		return
	}
	if t.language != t.app.preferences.Language {
		t.open.SetText(t.text("Open window"))
		t.connect.SetText(t.text("Connect"))
		t.disconnect.SetText(t.text("Disconnect"))
		t.exit.SetText(t.text("Exit"))
		t.language = t.app.preferences.Language
	}
	status := t.app.status.Text()
	t.status.SetText(status)
	tooltip := "VK TURN Proxy\n" + status
	if tooltip != t.tooltip {
		t.icon.SetToolTip(tooltip)
		t.tooltip = tooltip
	}
	t.connect.SetEnabled(t.app.command == nil && !t.exiting)
	t.disconnect.SetEnabled(t.app.command != nil && t.app.disconnect.Enabled() && !t.exiting)
	t.exit.SetEnabled(!t.exiting)
	if color := t.statusColor(); color != t.color {
		t.updateIcon()
	}
}

func (t *trayController) statusColor() walk.Color {
	switch t.app.connectionState {
	case "credentials", "relay", "handshake":
		return t.app.palette.accent
	case "connected":
		return t.app.palette.success
	case "failed":
		return t.app.palette.danger
	default:
		return t.app.palette.muted
	}
}

func (t *trayController) updateIcon() error {
	statusColor := t.statusColor()
	icon, exists := t.images[statusColor]
	if !exists {
		var err error
		icon, err = walk.NewIconFromImageForDPI(trayStatusImage(statusColor), 96)
		if err != nil {
			return err
		}
		t.images[statusColor] = icon
	}
	if err := t.icon.SetIcon(icon); err != nil {
		return err
	}
	t.color = statusColor
	return nil
}

func trayStatusImage(value walk.Color) *image.RGBA {
	icon := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			coverage := min(1.0, max(0.0, 14.5-math.Sqrt(dx*dx+dy*dy)))
			alpha := uint8(math.Round(255 * coverage))
			icon.SetRGBA(x, y, color.RGBA{
				R: uint8((uint32(value) & 255) * uint32(alpha) / 255),
				G: uint8((uint32(value) >> 8 & 255) * uint32(alpha) / 255),
				B: uint8((uint32(value) >> 16 & 255) * uint32(alpha) / 255),
				A: alpha,
			})
		}
	}
	return icon
}

func (t *trayController) showWindow() {
	if t.disposed {
		return
	}
	if win.IsIconic(t.app.window.Handle()) {
		win.ShowWindow(t.app.window.Handle(), win.SW_RESTORE)
	}
	t.app.window.Show()
	win.SetForegroundWindow(t.app.window.Handle())
}

// Closing the window keeps the connection available from its notification icon.
func (t *trayController) handleClose(canceled *bool) bool {
	if t.exiting || t.disposed || !t.icon.Visible() {
		return false
	}
	*canceled = true
	t.app.window.Hide()
	return true
}

func (t *trayController) requestExit() {
	if t.exiting || t.disposed {
		return
	}
	t.exiting = true
	t.app.closing = true
	if t.app.command != nil {
		t.app.stop()
		if !t.app.closing {
			t.exiting = false
			t.showWindow()
		}
		t.update()
		return
	}
	t.app.window.Close()
}

func (t *trayController) dispose() {
	if t.disposed {
		return
	}
	t.disposed = true
	t.icon.Dispose()
	t.icon.ContextMenu().Dispose()
	for _, icon := range t.images {
		icon.Dispose()
	}
}
