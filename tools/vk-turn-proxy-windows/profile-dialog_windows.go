// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *application) openProfileEditor(profile savedProfile) (savedProfile, bool, error) {
	var dialog *walk.Dialog
	var accept, cancel *walk.PushButton
	var name, call, endpoint, server *walk.LineEdit
	var mode, transport *walk.ComboBox
	var link, config *walk.TextEdit
	var linkArea, manualArea *walk.Composite
	text := func(en, ru string) string {
		if a.preferences.Language == "ru" {
			return ru
		}
		return en
	}
	openFile := func(target *walk.TextEdit, filter string) {
		picker := walk.FileDialog{Title: text("Open profile", "Открыть профиль"), Filter: filter}
		ok, err := picker.ShowOpen(dialog)
		if err != nil {
			walk.MsgBox(dialog, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
			return
		}
		if !ok {
			return
		}
		file, err := os.Open(picker.FilePath)
		if err != nil {
			walk.MsgBox(dialog, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err == nil && info.Size() > maxProfileSize {
			err = fmt.Errorf("%s", text("File is too large.", "Слишком большой файл."))
		}
		if err != nil {
			walk.MsgBox(dialog, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, maxProfileSize+1))
		if err == nil && len(data) > maxProfileSize {
			err = fmt.Errorf("%s", text("File is too large.", "Слишком большой файл."))
		}
		if err != nil {
			walk.MsgBox(dialog, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
			return
		}
		target.SetText(string(data))
	}
	accepted := false
	err := (Dialog{
		AssignTo: &dialog, Title: text("Connection profile", "Профиль подключения"),
		Font: Font{Family: "Segoe UI", PointSize: 10}, Size: Size{Width: 570, Height: 490}, MinSize: Size{Width: 540, Height: 460},
		Layout: VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 14}, Spacing: 9}, DefaultButton: &accept, CancelButton: &cancel,
		Children: []Widget{
			Label{Text: text("Profile name", "Название профиля")},
			LineEdit{AssignTo: &name, Text: profile.Name},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				ComboBox{AssignTo: &mode, Model: []string{text("Profile link", "Ссылка профиля"), text("WireGuard + VK call", "WireGuard + звонок VK")}, MinSize: Size{Width: 260}, MaxSize: Size{Width: 310}},
				Label{Text: "ⓘ", ToolTipText: text("Choose a profile link or your WireGuard config and VK call link.", "Выберите ссылку профиля или конфигурацию WireGuard со ссылкой на звонок VK.")}, HSpacer{},
			}},
			Composite{AssignTo: &linkArea, StretchFactor: 1, Layout: VBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
					Label{Text: text("Connection link", "Ссылка подключения")}, Label{Text: "ⓘ", ToolTipText: "vkturnproxy://import?data=<base64 JSON>"}, HSpacer{},
					PushButton{Text: text("Open file…", "Открыть файл…"), OnClicked: func() { openFile(link, "Profiles (*.json;*.txt)|*.json;*.txt|All files (*.*)|*.*") }},
				}},
				TextEdit{AssignTo: &link, Text: profile.ProfileText, VScroll: true, MaxLength: maxProfileSize, MinSize: Size{Height: 150}, StretchFactor: 1, ToolTipText: "vkturnproxy://import?data=<base64 JSON>"},
			}},
			Composite{AssignTo: &manualArea, StretchFactor: 1, Layout: VBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{Label{Text: text("WireGuard configuration", "Конфигурация WireGuard")}, HSpacer{}, PushButton{Text: text("Open .conf…", "Открыть .conf…"), OnClicked: func() { openFile(config, "WireGuard (*.conf)|*.conf|All files (*.*)|*.*") }}}},
				TextEdit{AssignTo: &config, Text: profile.WGConfig, VScroll: true, MaxLength: maxProfileSize, MinSize: Size{Height: 95}, StretchFactor: 1},
				LineEdit{AssignTo: &call, Text: profile.VKLink, CueBanner: "https://vk.com/call/join/…"},
				Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
					LineEdit{AssignTo: &endpoint, Text: profile.Endpoint, CueBanner: text("Endpoint from .conf", "Endpoint из .conf"), StretchFactor: 1},
					ComboBox{AssignTo: &transport, Model: []string{"SRTP", "DTLS"}, MinSize: Size{Width: 100}, MaxSize: Size{Width: 120}},
				}},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				Label{Text: text("Server", "Сервер")}, Label{Text: "ⓘ", ToolTipText: text("Optional server name in a multi-server profile.", "Имя сервера в профиле с несколькими серверами. Можно оставить пустым.")},
				LineEdit{AssignTo: &server, Text: profile.ServerName, CueBanner: text("Default", "По умолчанию"), StretchFactor: 1},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				HSpacer{}, PushButton{AssignTo: &cancel, Text: text("Cancel", "Отмена"), OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &accept, Text: text("Save", "Сохранить"), OnClicked: func() {
					candidate := savedProfile{ID: profile.ID, Name: strings.TrimSpace(name.Text()), Mode: "link", ProfileText: link.Text(), WGConfig: config.Text(), VKLink: call.Text(), Endpoint: endpoint.Text(), ServerName: strings.TrimSpace(server.Text()), UseSRTP: transport.CurrentIndex() == 0}
					if mode.CurrentIndex() == 1 {
						candidate.Mode = "wireguard"
					}
					if candidate.Name == "" {
						walk.MsgBox(dialog, "VK TURN Proxy", text("Enter a profile name.", "Введите название профиля."), walk.MsgBoxIconWarning)
						return
					}
					if _, err := candidate.connectionProfile(); err != nil {
						walk.MsgBox(dialog, "VK TURN Proxy", a.tr(err.Error()), walk.MsgBoxIconWarning)
						return
					}
					if candidate.ID == "" {
						var err error
						candidate.ID, err = newProfileID()
						if err != nil {
							walk.MsgBox(dialog, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
							return
						}
					}
					profile = candidate
					accepted = true
					dialog.Accept()
				}},
			}},
		},
	}).Create(a.window)
	if err != nil {
		return profile, false, err
	}
	defer dialog.Dispose()
	updateMode := func() {
		manual := mode.CurrentIndex() == 1
		linkArea.SetVisible(!manual)
		manualArea.SetVisible(manual)
	}
	mode.CurrentIndexChanged().Attach(updateMode)
	index := 0
	if profile.Mode == "wireguard" {
		index = 1
	}
	mode.SetCurrentIndex(index)
	transportIndex := 1
	if profile.UseSRTP || (profile.ID == "" && desktopDefaults.DefaultTransport == "srtp") {
		transportIndex = 0
	}
	transport.SetCurrentIndex(transportIndex)
	updateMode()
	a.themeDialog(dialog)
	installProfileCue(link, a.palette)
	dialog.Run()
	return profile, accepted, nil
}

// One native callback serves every editor instance without leaking callback slots.
var profileCues = map[win.HWND]profileCue{}

type profileCue struct {
	original uintptr
	brush    win.HBRUSH
	palette  themePalette
	font     win.HFONT
}

var profileCueProc = syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	cue := profileCues[hwnd]
	switch msg {
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		var ps win.PAINTSTRUCT
		dc := win.BeginPaint(hwnd, &ps)
		paintProfileCue(hwnd, dc, cue)
		win.EndPaint(hwnd, &ps)
		return 0
	case win.WM_PRINTCLIENT:
		paintProfileCue(hwnd, win.HDC(wParam), cue)
		return 0
	}
	return win.CallWindowProc(cue.original, hwnd, msg, wParam, lParam)
})

func paintProfileCue(hwnd win.HWND, dc win.HDC, cue profileCue) {
	var bounds win.RECT
	win.GetClientRect(hwnd, &bounds)
	fillNativeRect(dc, &bounds, cue.brush)
	previous := win.SelectObject(dc, win.HGDIOBJ(cue.font))
	defer win.SelectObject(dc, previous)
	win.SetBkMode(dc, win.TRANSPARENT)
	win.SetTextColor(dc, win.COLORREF(cue.palette.muted))
	win.DrawTextEx(dc, windows.StringToUTF16Ptr("vkturnproxy://import?data=<base64 JSON>"), -1, &bounds, win.DT_LEFT|win.DT_TOP|win.DT_WORDBREAK|win.DT_NOPREFIX, nil)
}

func installProfileCue(edit *walk.TextEdit, palette themePalette) {
	hwnd := edit.Handle()
	child := win.CreateWindowEx(0, windows.StringToUTF16Ptr("STATIC"), nil, win.WS_CHILD|win.WS_DISABLED|win.SS_LEFT|win.SS_NOPREFIX, 0, 0, 0, 0, hwnd, 0, 0, nil)
	if child == 0 {
		return
	}
	win.SendMessage(child, win.WM_SETFONT, win.SendMessage(hwnd, win.WM_GETFONT, 0, 0), 1)
	cue := profileCue{original: win.GetWindowLongPtr(child, win.GWLP_WNDPROC), brush: nativeBrush(palette.input), palette: palette, font: win.HFONT(win.SendMessage(hwnd, win.WM_GETFONT, 0, 0))}
	profileCues[child] = cue
	win.SetWindowLongPtr(child, win.GWLP_WNDPROC, profileCueProc)
	update := func() {
		bounds := edit.ClientBoundsPixels()
		inset := edit.IntFrom96DPI(8)
		win.SetWindowPos(child, 0, int32(inset), int32(inset), int32(max(0, bounds.Width-3*inset)), int32(edit.IntFrom96DPI(42)), win.SWP_NOACTIVATE|win.SWP_NOZORDER)
		show := int32(win.SW_HIDE)
		if edit.Text() == "" {
			show = win.SW_SHOWNA
		}
		win.ShowWindow(child, show)
	}
	edit.TextChanged().Attach(update)
	edit.SizeChanged().Attach(update)
	edit.Disposing().Attach(func() {
		win.SetWindowLongPtr(child, win.GWLP_WNDPROC, cue.original)
		delete(profileCues, child)
		win.DeleteObject(win.HGDIOBJ(cue.brush))
	})
	update()
}
