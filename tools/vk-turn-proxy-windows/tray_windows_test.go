// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func TestTrayStatusColors(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		palette := paletteFor(theme)
		app := &application{palette: palette}
		tray := &trayController{app: app}
		for _, entry := range []struct {
			state string
			color walk.Color
		}{
			{"disconnected", palette.muted},
			{"credentials", palette.accent},
			{"relay", palette.accent},
			{"handshake", palette.accent},
			{"connected", palette.success},
			{"disconnecting", palette.muted},
			{"failed", palette.danger},
		} {
			app.connectionState = entry.state
			if tray.statusColor() != entry.color {
				t.Fatalf("%s/%s used the wrong status color", theme, entry.state)
			}
			icon := trayStatusImage(entry.color)
			center := icon.RGBAAt(16, 16)
			if center.R != uint8(entry.color) || center.G != uint8(entry.color>>8) || center.B != uint8(entry.color>>16) || center.A != 255 {
				t.Fatal("notification icon changed its status color")
			}
			if icon.RGBAAt(0, 0).A != 0 {
				t.Fatal("notification icon has an opaque rectangular background")
			}
		}
	}
}

func TestTrayHidesAndRestoresWindow(t *testing.T) {
	app := newPreferencesWindow(t, "en", "light")
	tray, err := newTrayController(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tray.dispose)
	if !tray.icon.Visible() {
		t.Fatal("notification icon was not registered as visible")
	}
	canceled := false
	if !tray.handleClose(&canceled) || !canceled || win.IsWindowVisible(app.window.Handle()) {
		t.Fatal("closing did not hide the window to the notification icon")
	}
	if app.closing {
		t.Fatal("hiding the window requested application shutdown")
	}
	tray.showWindow()
	if !win.IsWindowVisible(app.window.Handle()) {
		t.Fatal("notification icon did not restore the window")
	}
	tray.exiting = true
	canceled = false
	if tray.handleClose(&canceled) || canceled {
		t.Fatal("explicit exit was intercepted by close-to-tray")
	}
}

func TestTrayActionsTrackConnectionAndLanguage(t *testing.T) {
	app := newPreferencesWindow(t, "en", "light")
	tray, err := newTrayController(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tray.dispose)
	if !tray.connect.Enabled() || tray.disconnect.Enabled() || tray.exit.Text() != "Exit" {
		t.Fatal("idle notification actions were incorrect")
	}
	app.command = &exec.Cmd{}
	t.Cleanup(func() { app.command = nil })
	app.setRunning(true)
	app.setConnectionState("connected")
	app.preferences.Language = "ru"
	app.applyLanguage()
	tray.update()
	if tray.connect.Enabled() || !tray.disconnect.Enabled() {
		t.Fatal("connected notification actions were incorrect")
	}
	if tray.open.Text() != "Открыть окно" || tray.disconnect.Text() != "Отключить" || tray.exit.Text() != "Выход" {
		t.Fatal("notification menu did not switch its language")
	}
	if !strings.Contains(tray.icon.ToolTip(), "Подключено") || tray.color != app.palette.success {
		t.Fatal("notification status did not match the active connection")
	}
}

func TestTrayExitRequestsNormalBackendShutdown(t *testing.T) {
	app := newPreferencesWindow(t, "en", "light")
	tray, err := newTrayController(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tray.dispose)
	app.command = &exec.Cmd{}
	t.Cleanup(func() { app.command = nil })
	app.stopFile = filepath.Join(t.TempDir(), "stop")
	app.setRunning(true)
	app.setConnectionState("connected")
	tray.requestExit()
	if _, err := os.Stat(app.stopFile); err != nil {
		t.Fatal("notification exit did not request normal backend shutdown")
	}
	if !app.closing || !tray.exiting || app.connectionState != "disconnecting" {
		t.Fatal("notification exit did not wait for network cleanup")
	}
	if app.window.IsDisposed() || tray.exit.Enabled() {
		t.Fatal("notification exit closed the parent before backend cleanup")
	}
}
