// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func newPreferencesWindow(t *testing.T, language, theme string) *application {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	a := &application{preferences: preferences{Language: language, Theme: theme, LogLines: defaultLogLines}, preferencesPath: filepath.Join(t.TempDir(), "preferences.json")}
	if err := a.createWindow(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.window.Dispose()
		for _, brush := range a.themeBrushes {
			brush.Dispose()
		}
		win.DeleteObject(win.HGDIOBJ(a.nativeInputBrush))
	})
	a.window.Show()
	return a
}

func TestLanguageAndThemeChangePreserveConnectionAndInputs(t *testing.T) {
	a := newPreferencesWindow(t, "en", "light")
	a.profiles = profileStore{Profiles: []savedProfile{{ID: "test", Name: "Example", Mode: "link", ProfileText: "vkturnproxy://import?data=example"}}, ActiveID: "test"}
	a.refreshProfiles()
	a.vkCookieText = "remixsid=test-session; p=test-token"
	a.setRunning(true)
	a.setConnectionState("connected")
	a.preferences.Language = "ru"
	a.preferences.Theme = "dark"
	a.applyLanguage()
	a.applyTheme()
	if a.connect.Text() != "Подключить" || a.status.Text() != "Подключено" || a.tabs.Pages().At(2).Title() != "Настройки" {
		t.Fatal("language did not update existing controls")
	}
	if a.log.TextColor() != walk.RGB(235, 238, 243) {
		t.Fatal("dark editor retained unreadable light-theme foreground")
	}
	for _, binding := range a.textBindings {
		if binding.source == "Paste your connection link" && binding.control.(*walk.Label).TextColor() != walk.RGB(235, 238, 243) {
			t.Fatal("dark heading retained navy foreground")
		}
	}
	if a.palette.window != walk.RGB(28, 30, 34) || a.profiles.Profiles[0].ProfileText != "vkturnproxy://import?data=example" || a.connectionState != "connected" {
		t.Fatal("theme change modified connection state or profile")
	}
	if a.vkCookieText != "remixsid=test-session; p=test-token" {
		t.Fatal("language or theme change modified cookie input")
	}
}

func TestConnectionProgressUsesBackendPhases(t *testing.T) {
	a := newPreferencesWindow(t, "en", "light")
	a.setRunning(true)
	a.setConnectionState("credentials")
	a.progressFromLog("proxy: TURN credentials failed, retrying")
	if a.connectionState != "credentials" {
		t.Fatal("credential error advanced progress")
	}
	a.progressFromLog("proxy: [conn 1] TURN relay allocated: 192.0.2.1")
	if a.connectionState != "relay" {
		t.Fatal("allocated relay did not advance progress")
	}
	a.progressFromLog("proxy: first connection up in 1s")
	if a.connectionState != "handshake" {
		t.Fatal("relay readiness did not advance the state")
	}
	a.progressFromLog("DEFAULT ROUTE → the tunnel")
	if a.connectionState != "connected" {
		t.Fatal("routing readiness did not finish progress")
	}
	a.stopFile = filepath.Join(t.TempDir(), "stop")
	a.disconnect.SetEnabled(false)
	a.progressFromLog("DEFAULT ROUTE → the tunnel")
	a.setConnectionState("disconnected")
	if a.phaseLabel.Text() != "" {
		t.Fatal("idle state retained a progress message")
	}
}

func TestCaptureProgressWindows(t *testing.T) {
	directory := os.Getenv("VKTURN_UI_CAPTURE_DIR")
	if directory == "" {
		t.Skip("capture output is opt-in")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct{ language, theme string }{{"en", "light"}, {"ru", "dark"}} {
		t.Run(variant.language+"-"+variant.theme, func(t *testing.T) {
			a := newPreferencesWindow(t, variant.language, variant.theme)
			a.setRunning(true)
			a.setConnectionState("credentials")
			a.progressFrame = 15
			var captureError error
			captured := false
			finished := make(chan struct{})
			defer close(finished)
			deadline := time.Now().Add(3 * time.Second)
			previousProfile := walk.Rectangle{}
			previousProgress := walk.Rectangle{}
			previousPhase := walk.Rectangle{}
			previousStatus := walk.Rectangle{}
			var poll func()
			poll = func() {
				select {
				case <-finished:
					return
				default:
				}
				profileBounds := a.profileChoice.BoundsPixels()
				progressBounds := a.progress.BoundsPixels()
				phaseBounds := a.phaseLabel.BoundsPixels()
				statusBounds := a.status.BoundsPixels()
				ready := profileBounds.Width > 200 && profileBounds.Height > 16 && progressBounds.Width > 500 && progressBounds.Height > 0 && phaseBounds.Width > 0 && phaseBounds.Height > 0 && statusBounds.Width > 0 && statusBounds.Height > 0
				stable := profileBounds == previousProfile && progressBounds == previousProgress && phaseBounds == previousPhase && statusBounds == previousStatus
				previousProfile, previousProgress = profileBounds, progressBounds
				previousPhase, previousStatus = phaseBounds, statusBounds
				if !ready || !stable {
					if time.Now().After(deadline) {
						captureError = fmt.Errorf("window layout did not settle: profile=%v progress=%v phase=%v status=%v", profileBounds, progressBounds, phaseBounds, statusBounds)
						a.window.Close()
						return
					}
					time.AfterFunc(25*time.Millisecond, func() {
						select {
						case <-finished:
							return
						default:
						}
						a.window.Synchronize(poll)
					})
					return
				}
				a.progress.Invalidate()
				win.RedrawWindow(a.window.Handle(), nil, 0, win.RDW_INVALIDATE|win.RDW_ALLCHILDREN|win.RDW_UPDATENOW)
				captureError = saveProgressWindow(a, filepath.Join(directory, variant.language+"-"+variant.theme+"-progress.png"))
				captured = captureError == nil
				a.window.Close()
			}
			time.AfterFunc(25*time.Millisecond, func() { a.window.Synchronize(poll) })
			a.window.Run()
			if captureError != nil {
				t.Fatal(captureError)
			}
			if !captured {
				t.Fatal("window closed before progress capture")
			}

		})
	}
}

func saveProgressWindow(a *application, path string) error {
	return saveCapturedWindow(a.window.Handle(), a.window.SizePixels(), a.window.DPI(), path)
}

func saveCapturedWindow(handle win.HWND, size walk.Size, dpi int, path string) error {
	bitmap, err := walk.NewBitmapForDPI(size, dpi)
	if err != nil {
		return err
	}
	defer bitmap.Dispose()
	canvas, err := walk.NewCanvasFromImage(bitmap)
	if err != nil {
		return err
	}
	result, _, printError := windows.NewLazySystemDLL("user32.dll").NewProc("PrintWindow").Call(uintptr(handle), uintptr(canvas.HDC()), 2)
	canvas.Dispose()
	if result == 0 {
		return fmt.Errorf("capture rendered window: %w", printError)
	}
	image, err := bitmap.ToImage()
	if err != nil {
		return err
	}
	for i := 3; i < len(image.Pix); i += 4 {
		image.Pix[i] = 255
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, image)
}

func TestClosingWindowsDuringRepeatedLayoutChanges(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			a := newPreferencesWindow(t, "en", "light")
			for j := 0; j < 4; j++ {
				a.preferences.Language = "ru"
				a.preferences.Theme = "dark"
				a.applyLanguage()
				a.applyTheme()
				a.window.SetSize(walk.Size{Width: 820 + j, Height: 700 + j})
			}
		})
	}
}
