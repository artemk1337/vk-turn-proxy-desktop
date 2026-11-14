// SPDX-License-Identifier: MIT

package main

import (
	"unsafe"

	"github.com/lxn/walk"
	"golang.org/x/sys/windows"
)

// Dialogs own their brushes so changing the main theme cannot dispose them.
func (a *application) themeDialog(dialog *walk.Dialog) {
	background, _ := walk.NewSolidColorBrush(a.palette.window)
	input, _ := walk.NewSolidColorBrush(a.palette.input)
	dialog.Disposing().Attach(func() { background.Dispose(); input.Dispose() })
	dialog.SetBackground(background)
	nativeTheme := "Explorer"
	dark := int32(0)
	if a.preferences.Theme == "dark" {
		nativeTheme = "DarkMode_Explorer"
		dark = 1
	}
	var apply func(walk.Widget)
	apply = func(widget walk.Widget) {
		brush := background
		switch widget.(type) {
		case *walk.TextEdit, *walk.LineEdit, *walk.ComboBox:
			brush = input
		}
		widget.SetBackground(brush)
		if text, ok := widget.(interface{ SetTextColor(walk.Color) }); ok {
			text.SetTextColor(a.palette.text)
		}
		setWindowTheme.Call(uintptr(widget.Handle()), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(nativeTheme))), 0)
		if container, ok := widget.(walk.Container); ok {
			for i := 0; i < container.Children().Len(); i++ {
				apply(container.Children().At(i))
			}
		}
	}
	for i := 0; i < dialog.Children().Len(); i++ {
		apply(dialog.Children().At(i))
	}
	setDWMAttribute.Call(uintptr(dialog.Handle()), 20, uintptr(unsafe.Pointer(&dark)), unsafe.Sizeof(dark))
}
