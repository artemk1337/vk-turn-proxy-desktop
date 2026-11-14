// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"strings"
	"syscall"

	wgprofile "github.com/artemk1337/vk-turn-proxy-desktop/pkg/wireguard-profile"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var mutedText = walk.RGB(90, 104, 125)
var headingText = walk.RGB(24, 37, 59)

func (a *application) createWindow() error {
	err := (MainWindow{
		AssignTo:   &a.window,
		Title:      "VK TURN Proxy",
		Font:       Font{Family: "Segoe UI", PointSize: 10},
		Background: SolidColorBrush{Color: walk.RGB(246, 248, 251)},
		MinSize:    Size{Width: 740, Height: 640},
		Size:       Size{Width: 820, Height: 700},
		Layout:     VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 14},
		Children: []Widget{
			Composite{Layout: HBox{Margins: Margins{Left: 18, Right: 18}, Alignment: AlignHNearVCenter, Spacing: 20}, Children: []Widget{
				Composite{Layout: VBox{MarginsZero: true, Alignment: AlignHNearVNear, Spacing: 6}, Children: []Widget{
					Label{Text: "VK TURN Proxy", Font: Font{Family: "Segoe UI", PointSize: 20, Bold: true}, TextColor: headingText},
					Label{Text: "WireGuard through VK calls", TextColor: mutedText},
				}},
				HSpacer{},
				Label{AssignTo: &a.status, Text: "Disconnected", TextColor: mutedText, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}},
			}},
			TabWidget{
				AssignTo:      &a.tabs,
				StretchFactor: 1,
				Pages:         []TabPage{a.profileTab(), a.manualTab(), a.advancedTab(), a.logTab(), a.settingsTab()},
			},
			Composite{Layout: VBox{MarginsZero: true, Spacing: 5}, Children: []Widget{
				Label{AssignTo: &a.phaseLabel, Text: "", TextColor: mutedText, MinSize: Size{Height: 16}, Font: Font{Family: "Segoe UI", PointSize: 9}},
				CustomWidget{AssignTo: &a.progress, MinSize: Size{Height: 6}, MaxSize: Size{Height: 6}, PaintPixels: a.paintProgress},
			}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
				Label{AssignTo: &a.modeLabel, Text: "Using profile link", TextColor: mutedText},
				HSpacer{},
				PushButton{AssignTo: &a.disconnect, Text: "Disconnect", MinSize: Size{Width: 120, Height: 40}, MaxSize: Size{Width: 120, Height: 40}, Enabled: false, OnClicked: a.stop},
				PushButton{AssignTo: &a.connect, Text: "Connect", MinSize: Size{Width: 150, Height: 40}, MaxSize: Size{Width: 150, Height: 40}, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}, OnClicked: a.start},
			}},
		},
	}).Create()
	if err != nil {
		return err
	}
	a.tabs.CurrentIndexChanged().Attach(a.changeTab)
	a.profile.SizeChanged().Attach(a.layoutProfileInput)
	a.profile.TextChanged().Attach(a.updateProfilePlaceholder)
	a.profilePlaceholder = win.CreateWindowEx(win.WS_EX_TRANSPARENT, windows.StringToUTF16Ptr("STATIC"), windows.StringToUTF16Ptr("vkturnproxy://import?data=<base64 JSON>"), win.WS_CHILD|win.WS_VISIBLE|win.WS_DISABLED|win.SS_LEFT|win.SS_NOPREFIX, 0, 0, 0, 0, a.profile.Handle(), 0, 0, nil)
	if a.profilePlaceholder == 0 {
		return errors.New("create profile placeholder")
	}
	win.SendMessage(a.profilePlaceholder, win.WM_SETFONT, win.SendMessage(a.profile.Handle(), win.WM_GETFONT, 0, 0), 1)
	// A STATIC child asks its edit parent for the background brush. Keep the
	// disabled gray text while matching the editor's white background.
	originalProc := win.GetWindowLongPtr(a.profile.Handle(), win.GWLP_WNDPROC)
	placeholderProc := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_CTLCOLORSTATIC && win.HWND(lParam) == a.profilePlaceholder {
			win.SetBkColor(win.HDC(wParam), win.COLORREF(a.palette.input))
			win.SetTextColor(win.HDC(wParam), win.COLORREF(a.palette.muted))
			return uintptr(a.nativeInputBrush)
		}
		return win.CallWindowProc(originalProc, hwnd, msg, wParam, lParam)
	})
	win.SetWindowLongPtr(a.profile.Handle(), win.GWLP_WNDPROC, placeholderProc)
	a.layoutProfileInput()
	a.initializePreferences()
	return nil
}

func (a *application) profileTab() TabPage {
	return TabPage{
		AssignTo: &a.linkPage, Title: "Profile link",
		Layout: VBox{Margins: Margins{Left: 18, Top: 20, Right: 18, Bottom: 18}, Spacing: 12},
		Children: []Widget{
			Label{Text: "Paste your connection link", Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true}, TextColor: headingText},
			Label{Text: "The link contains a Base64-encoded JSON profile. You can also open a JSON profile.", TextColor: mutedText},
			TextEdit{AssignTo: &a.profile, MinSize: Size{Height: 140}, MaxSize: Size{Height: 160}, MaxLength: maxProfileSize, VScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: "Open profile...", OnClicked: a.openProfile},
				HSpacer{},
			}},
			VSpacer{},
		},
	}
}

func (a *application) manualTab() TabPage {
	return TabPage{
		AssignTo: &a.manualPage, Title: "WireGuard config",
		Layout: VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 14}, Spacing: 8},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{Text: "WireGuard configuration", Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true}, TextColor: headingText},
				HSpacer{},
				PushButton{Text: "Open .conf...", MinSize: Size{Width: 120}, MaxSize: Size{Width: 120}, OnClicked: a.openWireGuard},
			}},
			Label{Text: "Full-tunnel .conf with one peer.", TextColor: mutedText},
			TextEdit{AssignTo: &a.wgConfig, MinSize: Size{Height: 120}, StretchFactor: 1, MaxLength: maxProfileSize, VScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
			Label{Text: "VK call link", TextColor: headingText},
			LineEdit{AssignTo: &a.vkLink, CueBanner: "https://vk.com/call/join/..."},
			Composite{Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 10}, Children: []Widget{
				Label{Text: "VK TURN server (optional)", TextColor: headingText, Row: 0, Column: 0},
				Label{Text: "Server transport", TextColor: headingText, Row: 0, Column: 1},
				LineEdit{AssignTo: &a.endpoint, MinSize: Size{Width: 330}, CueBanner: "Use Endpoint from WireGuard config", ToolTipText: "VK TURN listener, usually port 56000. Leave empty to use Endpoint from the config.", Row: 1, Column: 0},
				ComboBox{AssignTo: &a.transport, Model: []string{"Native SRTP", "Legacy DTLS"}, CurrentIndex: 0, MinSize: Size{Width: 180}, Row: 1, Column: 1},
			}},
			Label{Text: "The server address must point to the VK TURN listener, usually port 56000.", TextColor: mutedText},
		},
	}
}

func (a *application) advancedTab() TabPage {
	return TabPage{
		AssignTo: &a.advancedPage, Title: "Advanced",
		Layout: VBox{Margins: Margins{Left: 18, Top: 20, Right: 18, Bottom: 18}, Spacing: 12},
		Children: []Widget{
			Label{Text: "Server name", Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true}, TextColor: headingText},
			Label{Text: "Choose a server by name from a multi-server JSON profile.", TextColor: mutedText},
			LineEdit{AssignTo: &a.server, CueBanner: "Use the active server"},
			VSpacer{Size: 16},
			Label{Text: "VK login", Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true}, TextColor: headingText},
			Label{Text: "Open a cookies.txt file or paste Netscape cookies or remixsid=...; p=... below.", TextColor: mutedText},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
				LineEdit{AssignTo: &a.cookies, ReadOnly: true, StretchFactor: 1, CueBanner: "Anonymous access"},
				PushButton{Text: "Choose...", OnClicked: a.openCookies},
				PushButton{Text: "Sign in to VK", OnClicked: a.openVKLogin},
				PushButton{Text: "Clear", OnClicked: func() { a.cookies.SetText(""); a.cookieText.SetText("") }},
			}},
			TextEdit{AssignTo: &a.cookieText, MaxLength: maxProfileSize, MinSize: Size{Height: 90}, MaxSize: Size{Height: 110}, VScroll: true, Font: Font{Family: "Consolas", PointSize: 9}},
			Label{Text: "Pasted cookies take priority over the selected file.", TextColor: mutedText},
			VSpacer{},
		},
	}
}

func (a *application) logTab() TabPage {
	return TabPage{
		Title:  "Log",
		Layout: VBox{Margins: Margins{Left: 18, Top: 20, Right: 18, Bottom: 18}, Spacing: 12},
		Children: []Widget{
			Label{Text: "Connection log", Font: Font{Family: "Segoe UI", PointSize: 12, Bold: true}, TextColor: headingText},
			TextEdit{AssignTo: &a.log, ReadOnly: true, VScroll: true, StretchFactor: 1, Font: Font{Family: "Consolas", PointSize: 9}},
		},
	}
}

func (a *application) changeTab() {
	index := a.tabs.CurrentIndex()
	if index == 3 {
		a.logView.FollowTail(a.log)
	}
	if index == 0 || index == 1 {
		a.inputMode = index
		a.server.SetEnabled(index == 0)
		a.updateModeLabel()
	}
}

func (a *application) connectionProfile() ([]byte, error) {
	if a.inputMode == 1 {
		return wgprofile.Build(wgprofile.Options{
			Config:      a.wgConfig.Text(),
			VKLink:      a.vkLink.Text(),
			PeerAddress: a.endpoint.Text(),
			UseSrtp:     a.transport.CurrentIndex() == 0,
		})
	}
	profile := strings.TrimSpace(a.profile.Text())
	if profile == "" {
		return nil, errors.New(a.tr("Paste a connection link or open a profile first."))
	}
	return []byte(profile), nil
}

// Keep the placeholder outside the editor's value so it can never be imported.
func (a *application) layoutProfileInput() {
	bounds := a.profile.ClientBoundsPixels()
	inset := a.profile.IntFrom96DPI(8)
	height := a.profile.IntFrom96DPI(24)
	win.SetWindowPos(a.profilePlaceholder, 0, int32(inset), int32(inset), int32(max(0, bounds.Width-3*inset)), int32(height), win.SWP_NOACTIVATE|win.SWP_NOZORDER)
	a.updateProfilePlaceholder()
}

func (a *application) updateProfilePlaceholder() {
	show := int32(win.SW_HIDE)
	if a.profile.Text() == "" {
		show = win.SW_SHOWNA
	}
	win.ShowWindow(a.profilePlaceholder, show)
}
