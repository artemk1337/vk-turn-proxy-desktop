// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func TestCompactLayoutPreservesSettingsAlignmentAndFormBounds(t *testing.T) {
	for _, variant := range []struct{ language, theme string }{{"en", "light"}, {"ru", "dark"}} {
		t.Run(variant.language+"-"+variant.theme, func(t *testing.T) {
			a := newPreferencesWindow(t, variant.language, variant.theme)
			type layoutCase struct {
				size walk.Size
				page int
			}
			var cases []layoutCase
			for _, size := range []walk.Size{{Width: 760, Height: 610}, {Width: 960, Height: 760}, {Width: 720, Height: 580}} {
				for _, page := range []int{4, 1, 2} {
					cases = append(cases, layoutCase{size, page})
				}
			}
			var failure error
			index, stable := 0, 0
			previous := [4]win.RECT{}
			deadline := time.Now().Add(3 * time.Second)
			setCase := func() {
				a.tabs.SetCurrentIndex(cases[index].page)
				a.window.SetSize(cases[index].size)
				previous = [4]win.RECT{}
				stable = 0
				deadline = time.Now().Add(3 * time.Second)
			}
			setCase()
			finished := make(chan struct{})
			defer close(finished)
			var poll func()
			poll = func() {
				select {
				case <-finished:
					return
				default:
				}
				current := cases[index]
				controls := compactPageControls(a, current.page)
				var snapshot [4]win.RECT
				ready := true
				for i, control := range controls {
					win.GetWindowRect(control.Handle(), &snapshot[i])
					ready = ready && snapshot[i].Right > snapshot[i].Left && snapshot[i].Bottom > snapshot[i].Top
				}
				if ready && snapshot == previous {
					stable++
				} else {
					stable = 0
				}
				previous = snapshot
				if stable >= 3 {
					failure = checkCompactPage(a, current.page, current.size)
					if directory := os.Getenv("VKTURN_UI_CAPTURE_DIR"); directory != "" {
						if err := os.MkdirAll(directory, 0700); err != nil && failure == nil {
							failure = err
						}
						name := fmt.Sprintf("%s-%s-compact-%d-page-%d.png", variant.language, variant.theme, current.size.Width, current.page)
						if err := saveProgressWindow(a, filepath.Join(directory, name)); failure == nil {
							failure = err
						}
					}
					index++
					if failure != nil || index == len(cases) {
						a.window.Close()
						return
					}
					setCase()
				} else if time.Now().After(deadline) {
					failure = fmt.Errorf("compact page %d did not settle: %v", current.page, snapshot)
					a.window.Close()
					return
				}
				time.AfterFunc(25*time.Millisecond, func() {
					select {
					case <-finished:
						return
					default:
						a.window.Synchronize(poll)
					}
				})
			}
			time.AfterFunc(25*time.Millisecond, func() { a.window.Synchronize(poll) })
			a.window.Run()
			if failure != nil {
				t.Fatal(failure)
			}
		})
	}
}

func compactPageControls(a *application, page int) []walk.Widget {
	switch page {
	case 1:
		return []walk.Widget{a.wgConfig, a.vkLink, a.endpoint, a.transport}
	case 2:
		return []walk.Widget{a.server, a.cookies, a.cookieText}
	default:
		return []walk.Widget{a.languageChoice, a.themeChoice, a.startupChoice}
	}
}

func checkCompactPage(a *application, page int, requested walk.Size) error {
	if size := a.window.Size(); size.Width > requested.Width+2 || size.Height > requested.Height+2 {
		return fmt.Errorf("page %d expanded compact window from %v to %v", page, requested, size)
	}
	var pageBounds win.RECT
	win.GetWindowRect(a.tabs.Pages().At(page).Handle(), &pageBounds)
	for _, control := range compactPageControls(a, page) {
		var bounds win.RECT
		win.GetWindowRect(control.Handle(), &bounds)
		if bounds.Left < pageBounds.Left || bounds.Top < pageBounds.Top || bounds.Right > pageBounds.Right || bounds.Bottom > pageBounds.Bottom {
			return fmt.Errorf("page %d clips %T: control=%v page=%v", page, control, bounds, pageBounds)
		}
	}
	if page != 4 {
		return nil
	}
	var language, theme, startup win.RECT
	win.GetWindowRect(a.languageChoice.Handle(), &language)
	win.GetWindowRect(a.themeChoice.Handle(), &theme)
	win.GetWindowRect(a.startupChoice.Handle(), &startup)
	for _, choice := range []*walk.ComboBox{a.languageChoice, a.themeChoice} {
		if width := choice.Size().Width; width < 210 || width > 244 {
			return fmt.Errorf("preference selector is not compact: width %d", width)
		}
	}
	delta := int(startup.Left - language.Left)
	tolerance := a.window.IntFrom96DPI(2)
	if delta < -tolerance || delta > tolerance {
		return fmt.Errorf("startup checkbox moved away from left field edge: %v vs %v", startup, language)
	}
	if theme.Left < language.Right+int32(a.window.IntFrom96DPI(12)) || theme.Top != language.Top {
		return fmt.Errorf("preference fields are not aligned columns: %v and %v", language, theme)
	}
	if startup.Top <= language.Bottom || startup.Right-startup.Left > int32(a.window.IntFrom96DPI(360)) {
		return fmt.Errorf("startup checkbox is stretched or overlaps the selectors: %v", startup)
	}
	return nil
}
