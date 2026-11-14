// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func TestTabHoverKeepsOneStablePaint(t *testing.T) {
	for _, variant := range []struct{ language, theme string }{{"en", "light"}, {"ru", "dark"}} {
		t.Run(variant.language+"-"+variant.theme, func(t *testing.T) {
			a := newPreferencesWindow(t, variant.language, variant.theme)
			var result error
			completed := false
			deadline := time.Now().Add(3 * time.Second)
			previous := walk.Rectangle{}
			var poll func()
			poll = func() {
				bounds := a.tabs.BoundsPixels()
				if bounds.Width < 400 || bounds.Height < 100 || bounds != previous {
					previous = bounds
					if time.Now().Before(deadline) {
						time.AfterFunc(25*time.Millisecond, func() { a.window.Synchronize(poll) })
						return
					}
					result = fmt.Errorf("tab layout did not settle: %v", bounds)
				} else {
					result = verifyTabHover(a)
				}
				completed = true
				a.window.Close()
			}
			time.AfterFunc(25*time.Millisecond, func() { a.window.Synchronize(poll) })
			a.window.Run()
			if result != nil {
				t.Fatal(result)
			}
			if !completed {
				t.Fatal("window closed before hover verification")
			}
		})
	}
}

func verifyTabHover(a *application) error {
	// Observe the actual screen paint path. The old Walk procedure re-entered
	// WM_PRINTCLIENT during WM_PAINT, then added its own selected label before
	// our GetDC overlay. Even when the final pixels matched, two different
	// label positions were briefly visible in one frame.
	original := win.GetWindowLongPtr(a.nativeTabs, win.GWLP_WNDPROC)
	painting, paints, nestedPrints := 0, 0, 0
	probe := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_PAINT {
			painting++
			paints++
			defer func() { painting-- }()
		}
		if msg == win.WM_PRINTCLIENT && painting > 0 {
			nestedPrints++
		}
		return win.CallWindowProc(original, hwnd, msg, wParam, lParam)
	})
	win.SetWindowLongPtr(a.nativeTabs, win.GWLP_WNDPROC, probe)
	defer win.SetWindowLongPtr(a.nativeTabs, win.GWLP_WNDPROC, original)
	win.SetFocus(a.profile.Handle())
	redraw := func() {
		win.RedrawWindow(a.nativeTabs, nil, 0, win.RDW_INVALIDATE|win.RDW_UPDATENOW)
	}
	redraw()
	baseline, err := tabScreenPixels(a)
	if err != nil {
		return err
	}
	for cycle := 0; cycle < 3; cycle++ {
		for i := 0; i < a.tabs.Pages().Len(); i++ {
			var rect win.RECT
			win.SendMessage(a.nativeTabs, win.TCM_GETITEMRECT, uintptr(i), uintptr(unsafe.Pointer(&rect)))
			x, y := (rect.Left+rect.Right)/2, (rect.Top+rect.Bottom)/2
			win.SendMessage(a.nativeTabs, win.WM_MOUSEMOVE, 0, uintptr(uint32(y)<<16|uint32(x)&0xffff))
			redraw()
			current, err := tabScreenPixels(a)
			if err != nil {
				return err
			}
			if !bytes.Equal(baseline, current) {
				return fmt.Errorf("tab header pixels moved on hover, tab %d, cycle %d", i, cycle)
			}
			win.SendMessage(a.nativeTabs, win.WM_MOUSELEAVE, 0, 0)
			redraw()
			current, err = tabScreenPixels(a)
			if err != nil {
				return err
			}
			if !bytes.Equal(baseline, current) {
				return fmt.Errorf("tab header pixels moved after mouse leave, tab %d", i)
			}
		}
	}
	if paints == 0 || nestedPrints != 0 {
		return fmt.Errorf("screen paint re-entered a second tab renderer: paints=%d nested prints=%d", paints, nestedPrints)
	}
	win.SetFocus(a.nativeTabs)
	win.SendMessage(a.nativeTabs, win.WM_KEYDOWN, win.VK_RIGHT, 0)
	win.SendMessage(a.nativeTabs, win.WM_KEYUP, win.VK_RIGHT, 0)
	if a.tabs.CurrentIndex() != 1 {
		return fmt.Errorf("native keyboard navigation selected tab %d, want 1", a.tabs.CurrentIndex())
	}
	return nil
}

func tabScreenPixels(a *application) ([]byte, error) {
	var client, item win.RECT
	win.GetClientRect(a.nativeTabs, &client)
	win.SendMessage(a.nativeTabs, win.TCM_GETITEMRECT, 0, uintptr(unsafe.Pointer(&item)))
	height := item.Bottom + int32(a.tabs.IntFrom96DPI(2))
	bitmap, err := walk.NewBitmapForDPI(walk.Size{Width: int(client.Right), Height: int(height)}, a.tabs.DPI())
	if err != nil {
		return nil, err
	}
	defer bitmap.Dispose()
	canvas, err := walk.NewCanvasFromImage(bitmap)
	if err != nil {
		return nil, err
	}
	screen := win.GetDC(a.nativeTabs)
	copied := win.BitBlt(canvas.HDC(), 0, 0, client.Right, height, screen, 0, 0, win.SRCCOPY)
	win.ReleaseDC(a.nativeTabs, screen)
	canvas.Dispose()
	if !copied {
		return nil, fmt.Errorf("copy tab header screen pixels")
	}
	frame, err := bitmap.ToImage()
	if err != nil {
		return nil, err
	}
	for i := 3; i < len(frame.Pix); i += 4 {
		frame.Pix[i] = 255
	}
	return frame.Pix, nil
}
