// SPDX-License-Identifier: MIT

package main

import (
	"syscall"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Keep native keyboard, focus, and dropdown behavior while painting the dark
// control face. The old Walk controls otherwise use fixed system colors.
func (a *application) installNativeControlPainting() {
	var visit func(walk.Container)
	visit = func(container walk.Container) {
		for i := 0; i < container.Children().Len(); i++ {
			widget := container.Children().At(i)
			switch control := widget.(type) {
			case *walk.ComboBox:
				a.installDarkFace(control, true)
			case *walk.PushButton:
				a.installDarkFace(control, false)
			}
			if child, ok := widget.(walk.Container); ok {
				visit(child)
			}
		}
	}
	visit(a.window)
	for i := 0; i < a.tabs.Pages().Len(); i++ {
		visit(a.tabs.Pages().At(i))
	}
}

func (a *application) installDarkFace(widget walk.Widget, combo bool) {
	original := win.GetWindowLongPtr(widget.Handle(), win.GWLP_WNDPROC)
	callback := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if a.preferences.Theme == "dark" && (msg == win.WM_CTLCOLORLISTBOX || msg == win.WM_CTLCOLORSTATIC || msg == win.WM_CTLCOLOREDIT) {
			win.SetTextColor(win.HDC(wParam), win.COLORREF(a.palette.text))
			win.SetBkColor(win.HDC(wParam), win.COLORREF(a.palette.input))
			return uintptr(a.nativeInputBrush)
		}
		result := win.CallWindowProc(original, hwnd, msg, wParam, lParam)
		if a.preferences.Theme == "dark" {
			switch msg {
			case win.WM_PAINT:
				a.paintDarkFace(widget, combo, 0)
			case win.WM_PRINTCLIENT, win.WM_PRINT:
				a.paintDarkFace(widget, combo, win.HDC(wParam))
			}
		}
		return result
	})
	win.SetWindowLongPtr(widget.Handle(), win.GWLP_WNDPROC, callback)
}

func (a *application) paintDarkFace(widget walk.Widget, combo bool, dc win.HDC) {
	if dc == 0 {
		dc = win.GetDC(widget.Handle())
		defer win.ReleaseDC(widget.Handle(), dc)
	}
	var rect win.RECT
	win.GetClientRect(widget.Handle(), &rect)
	fill := a.palette.input
	textColor := a.palette.text
	border := a.palette.border
	if !widget.Enabled() {
		textColor = a.palette.muted
	}
	if win.GetFocus() == widget.Handle() {
		border = a.palette.accent
	}
	if widget == a.connect && widget.Enabled() {
		fill = walk.RGB(48, 100, 177)
		textColor = walk.RGB(255, 255, 255)
		border = fill
	}
	body := nativeBrush(fill)
	defer win.DeleteObject(win.HGDIOBJ(body))
	pen := win.ExtCreatePen(win.PS_GEOMETRIC|win.PS_SOLID, 1, &win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(border)}, 0, nil)
	defer win.DeleteObject(win.HGDIOBJ(pen))
	oldPen := win.SelectObject(dc, win.HGDIOBJ(pen))
	defer win.SelectObject(dc, oldPen)
	oldBrush := win.SelectObject(dc, win.HGDIOBJ(body))
	defer win.SelectObject(dc, oldBrush)
	win.Rectangle_(dc, rect.Left, rect.Top, rect.Right, rect.Bottom)
	win.SetBkMode(dc, win.TRANSPARENT)
	win.SetTextColor(dc, win.COLORREF(textColor))
	oldFont := win.SelectObject(dc, win.HGDIOBJ(win.SendMessage(widget.Handle(), win.WM_GETFONT, 0, 0)))
	defer win.SelectObject(dc, oldFont)
	text := widget.(interface{ Text() string }).Text()
	format := uint32(win.DT_CENTER | win.DT_VCENTER | win.DT_SINGLELINE | win.DT_END_ELLIPSIS)
	if combo {
		label := rect
		label.Left += 8
		label.Right -= 28
		win.DrawTextEx(dc, windows.StringToUTF16Ptr(text), -1, &label, win.DT_LEFT|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_END_ELLIPSIS, nil)
		rect.Left = rect.Right - 26
		text = "⌄"
	}
	win.DrawTextEx(dc, windows.StringToUTF16Ptr(text), -1, &rect, format, nil)
}

func (a *application) paintTabBorder(hwnd win.HWND, dc win.HDC, top int32) {
	var rect win.RECT
	win.GetClientRect(hwnd, &rect)
	pen := win.ExtCreatePen(win.PS_GEOMETRIC|win.PS_SOLID, 2, &win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(a.palette.border)}, 0, nil)
	oldPen := win.SelectObject(dc, win.HGDIOBJ(pen))
	oldBrush := win.SelectObject(dc, win.GetStockObject(win.HOLLOW_BRUSH))
	win.Rectangle_(dc, 0, top, rect.Right, rect.Bottom)
	win.SelectObject(dc, oldBrush)
	win.SelectObject(dc, oldPen)
	win.DeleteObject(win.HGDIOBJ(pen))
}
