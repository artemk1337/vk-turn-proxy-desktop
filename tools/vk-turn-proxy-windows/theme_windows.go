// SPDX-License-Identifier: MIT

package main

import (
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

type themePalette struct{ window, panel, input, text, muted, border, accent, success, danger walk.Color }

func paletteFor(theme string) themePalette {
	if theme == "dark" {
		return themePalette{
			window: walk.RGB(28, 30, 34), panel: walk.RGB(36, 39, 44), input: walk.RGB(44, 48, 54),
			text: walk.RGB(235, 238, 243), muted: walk.RGB(166, 175, 188), border: walk.RGB(65, 72, 82),
			accent: walk.RGB(113, 171, 255), success: walk.RGB(91, 202, 151), danger: walk.RGB(255, 131, 139),
		}
	}
	return themePalette{
		window: walk.RGB(246, 248, 251), panel: walk.RGB(255, 255, 255), input: walk.RGB(255, 255, 255),
		text: walk.RGB(24, 37, 59), muted: walk.RGB(90, 104, 125), border: walk.RGB(217, 223, 232),
		accent: walk.RGB(41, 104, 203), success: walk.RGB(23, 125, 75), danger: walk.RGB(180, 45, 45),
	}
}

var themeDLL = windows.NewLazySystemDLL("uxtheme.dll")
var setWindowTheme = themeDLL.NewProc("SetWindowTheme")

var setDWMAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

func (a *application) applyTheme() {
	a.palette = paletteFor(a.preferences.Theme)
	dark := uintptr(0)
	nativeTheme := "Explorer"
	mode := uintptr(3)
	if a.preferences.Theme == "dark" {
		dark = 1
		mode = 2
		nativeTheme = "DarkMode_Explorer"
	}
	allowDarkProc, _ := windows.GetProcAddressByOrdinal(windows.Handle(themeDLL.Handle()), 133)
	if appMode, err := windows.GetProcAddressByOrdinal(windows.Handle(themeDLL.Handle()), 135); err == nil {
		syscall.SyscallN(appMode, mode)
	}
	old := a.themeBrushes
	oldInputBrush := a.nativeInputBrush
	a.nativeInputBrush = nativeBrush(a.palette.input)
	windowBrush, _ := walk.NewSolidColorBrush(a.palette.window)
	panelBrush, _ := walk.NewSolidColorBrush(a.palette.panel)
	inputBrush, _ := walk.NewSolidColorBrush(a.palette.input)
	a.themeBrushes = []*walk.SolidColorBrush{windowBrush, panelBrush, inputBrush}
	a.window.SetBackground(windowBrush)
	var apply func(walk.Widget, walk.Brush)
	apply = func(widget walk.Widget, baseBrush walk.Brush) {
		brush := baseBrush
		switch widget.(type) {
		case *walk.TextEdit, *walk.LineEdit, *walk.ComboBox:
			brush = inputBrush
		}
		widget.SetBackground(brush)
		switch widget.(type) {
		case *walk.TextEdit, *walk.LineEdit:
			style := win.GetWindowLongPtr(widget.Handle(), win.GWL_EXSTYLE)
			if dark != 0 {
				style &^= win.WS_EX_CLIENTEDGE
			} else {
				style |= win.WS_EX_CLIENTEDGE
			}
			win.SetWindowLongPtr(widget.Handle(), win.GWL_EXSTYLE, style)
			win.SetWindowPos(widget.Handle(), 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)
		}

		if text, ok := widget.(interface{ SetTextColor(walk.Color) }); ok {
			color := a.palette.text
			if label, ok := widget.(*walk.Label); ok && a.mutedLabels[label] {
				color = a.palette.muted
			}
			text.SetTextColor(color)
		}
		if allowDarkProc != 0 {
			syscall.SyscallN(allowDarkProc, uintptr(widget.Handle()), dark)
		}
		setWindowTheme.Call(uintptr(widget.Handle()), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(nativeTheme))), 0)
		if container, ok := widget.(walk.Container); ok {
			for i := 0; i < container.Children().Len(); i++ {
				apply(container.Children().At(i), baseBrush)
			}
		}
		widget.Invalidate()
	}
	for i := 0; i < a.window.Children().Len(); i++ {
		apply(a.window.Children().At(i), windowBrush)
	}
	for i := 0; i < a.tabs.Pages().Len(); i++ {
		apply(a.tabs.Pages().At(i), panelBrush)
	}
	// Root-level composites belong to the window's quieter surface.
	for i := 0; i < a.window.Children().Len(); i++ {
		if composite, ok := a.window.Children().At(i).(*walk.Composite); ok {
			composite.SetBackground(windowBrush)
		}
	}
	if allowDarkProc != 0 {
		syscall.SyscallN(allowDarkProc, uintptr(a.window.Handle()), dark)
	}
	value := int32(dark)
	setDWMAttribute.Call(uintptr(a.window.Handle()), 20, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	a.updateConnectionState()
	a.progress.Invalidate()
	a.tabs.Invalidate()
	win.RedrawWindow(a.window.Handle(), nil, 0, win.RDW_INVALIDATE|win.RDW_ALLCHILDREN)
	if oldInputBrush != 0 {
		win.DeleteObject(win.HGDIOBJ(oldInputBrush))
	}
	for _, brush := range old {
		brush.Dispose()
	}
}

func (a *application) installTabPainting() {
	handle, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW").Call(uintptr(a.tabs.Handle()), 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("SysTabControl32"))), 0)
	a.nativeTabs = win.HWND(handle)
	original := win.GetWindowLongPtr(a.nativeTabs, win.GWLP_WNDPROC)
	callback := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		result := win.CallWindowProc(original, hwnd, msg, wParam, lParam)
		switch msg {
		case win.WM_PAINT:
			a.paintTabHeader(hwnd, 0)
		case win.WM_PRINTCLIENT, win.WM_PRINT:
			a.paintTabHeader(hwnd, win.HDC(wParam))
		}
		return result
	})
	win.SetWindowLongPtr(a.nativeTabs, win.GWLP_WNDPROC, callback)
}

func (a *application) paintTabHeader(hwnd win.HWND, dc win.HDC) {
	if dc == 0 {
		dc = win.GetDC(hwnd)
		defer win.ReleaseDC(hwnd, dc)
	}
	var rect win.RECT
	win.SendMessage(hwnd, 0x130a, 0, uintptr(unsafe.Pointer(&rect)))
	bounds := a.tabs.ClientBoundsPixels()
	track := win.RECT{Right: int32(bounds.Width), Bottom: rect.Bottom + 2}
	brush := nativeBrush(a.palette.window)
	fillNativeRect(dc, &track, brush)
	a.paintTabBorder(hwnd, dc, rect.Bottom)
	win.DeleteObject(win.HGDIOBJ(brush))
	win.SetBkMode(dc, win.TRANSPARENT)
	font := win.SendMessage(hwnd, win.WM_GETFONT, 0, 0)
	oldFont := win.SelectObject(dc, win.HGDIOBJ(font))
	defer win.SelectObject(dc, oldFont)
	for i := 0; i < a.tabs.Pages().Len(); i++ {
		win.SendMessage(hwnd, 0x130a, uintptr(i), uintptr(unsafe.Pointer(&rect)))
		color := a.palette.muted
		if i == a.tabs.CurrentIndex() {
			active := nativeBrush(a.palette.panel)
			fillNativeRect(dc, &rect, active)
			win.DeleteObject(win.HGDIOBJ(active))
			accent := nativeBrush(a.palette.accent)
			line := rect
			line.Top = line.Bottom - 2
			fillNativeRect(dc, &line, accent)
			win.DeleteObject(win.HGDIOBJ(accent))
			color = a.palette.text
		}
		win.SetTextColor(dc, win.COLORREF(color))
		text := windows.StringToUTF16Ptr(a.tabs.Pages().At(i).Title())
		win.DrawTextEx(dc, text, -1, &rect, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE, nil)
	}
}

func nativeBrush(color walk.Color) win.HBRUSH {
	return win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(color)})
}

var fillRectProc = windows.NewLazySystemDLL("user32.dll").NewProc("FillRect")

func fillNativeRect(dc win.HDC, rect *win.RECT, brush win.HBRUSH) {
	fillRectProc.Call(uintptr(dc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}
