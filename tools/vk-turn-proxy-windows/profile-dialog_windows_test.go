// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func TestProfileEditorLayoutAndCapture(t *testing.T) {
	directory := os.Getenv("VKTURN_UI_CAPTURE_DIR")
	if directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, language := range []string{"en", "ru"} {
		for _, theme := range []string{"light", "dark"} {
			t.Run(language+"-"+theme, func(t *testing.T) {
				a := newPreferencesWindow(t, language, theme)
				var testError error
				modeIndex := 0
				completed := false
				done := make(chan struct{})
				defer close(done)
				deadline := time.Now().Add(5 * time.Second)
				var poll func()
				schedule := func() {
					time.AfterFunc(100*time.Millisecond, func() {
						select {
						case <-done:
							return
						default:
						}
						a.window.Synchronize(poll)
					})
				}
				poll = func() {
					dialog := win.GetWindow(a.window.Handle(), win.GW_ENABLEDPOPUP)
					if dialog == 0 || dialog == a.window.Handle() || !win.IsWindowVisible(dialog) {
						if time.Now().After(deadline) {
							testError = fmt.Errorf("profile editor did not appear")
							a.window.Close()
							return
						}
						schedule()
						return
					}
					var bounds win.RECT
					win.GetWindowRect(dialog, &bounds)
					var combo win.HWND
					visibleEdits := 0
					var visit func(win.HWND)
					visit = func(parent win.HWND) {
						for child := win.GetWindow(parent, win.GW_CHILD); child != 0; child = win.GetWindow(child, win.GW_HWNDNEXT) {
							if !win.IsWindowVisible(child) {
								continue
							}
							var name [128]uint16
							win.GetClassName(child, &name[0], len(name))
							class := windows.UTF16ToString(name[:])
							if strings.EqualFold(class, "ComboBox") && combo == 0 {
								combo = child
							}
							var rect win.RECT
							win.GetWindowRect(child, &rect)
							if rect.Right <= rect.Left || rect.Bottom <= rect.Top {
								testError = fmt.Errorf("empty %s control bounds", class)
							}
							if rect.Left < bounds.Left || rect.Top < bounds.Top || rect.Right > bounds.Right || rect.Bottom > bounds.Bottom {
								testError = fmt.Errorf("%s outside dialog: %v within %v", class, rect, bounds)
							}
							if strings.EqualFold(class, "Edit") {
								visibleEdits++
							}
							visit(child)
						}
					}
					visit(dialog)
					if combo == 0 {
						testError = fmt.Errorf("profile method selector missing")
					}
					if modeIndex == 0 && visibleEdits < 3 {
						testError = fmt.Errorf("link editor fields missing: %d", visibleEdits)
					}
					if modeIndex == 1 && visibleEdits < 5 {
						testError = fmt.Errorf("WireGuard editor fields missing: %d", visibleEdits)
					}
					if testError == nil && directory != "" {
						method := []string{"link", "wireguard"}[modeIndex]
						win.RedrawWindow(dialog, nil, 0, win.RDW_INVALIDATE|win.RDW_ALLCHILDREN|win.RDW_UPDATENOW)
						testError = saveCapturedWindow(dialog, walk.Size{Width: int(bounds.Right - bounds.Left), Height: int(bounds.Bottom - bounds.Top)}, a.window.DPI(), filepath.Join(directory, language+"-"+theme+"-profile-"+method+".png"))
					}
					if testError != nil || modeIndex == 1 {
						completed = testError == nil
						win.SendMessage(dialog, win.WM_CLOSE, 0, 0)
						return
					}
					win.SendMessage(combo, win.CB_SETCURSEL, 1, 0)
					id := win.GetWindowLongPtr(combo, win.GWLP_ID)
					win.SendMessage(win.GetParent(combo), win.WM_COMMAND, id|(win.CBN_SELCHANGE<<16), uintptr(combo))
					modeIndex = 1
					schedule()
				}
				schedule()
				_, accepted, err := a.openProfileEditor(savedProfile{Mode: "link", UseSRTP: true})
				if err != nil {
					t.Fatal(err)
				}
				if testError != nil {
					t.Fatal(testError)
				}
				if accepted || !completed {
					t.Fatalf("editor accepted=%v, completed=%v", accepted, completed)
				}
				if len(profileCues) != 0 {
					t.Fatal("profile cue retained after closing editor")
				}
			})
		}
	}
}
