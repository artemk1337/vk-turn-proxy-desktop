// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var mutedText = walk.RGB(90, 104, 125)
var headingText = walk.RGB(24, 37, 59)

func (a *application) createWindow() error {
	err := (MainWindow{
		AssignTo: &a.window, Title: "VK TURN Proxy", Font: Font{Family: "Segoe UI", PointSize: 10},
		Background: SolidColorBrush{Color: walk.RGB(246, 248, 251)},
		MinSize:    Size{Width: 660, Height: 480}, Size: Size{Width: 700, Height: 520},
		Layout: VBox{Margins: Margins{Left: 16, Top: 14, Right: 16, Bottom: 12}, Spacing: 12},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVCenter, Spacing: 12}, Children: []Widget{
				Label{Text: "VK TURN Proxy", Font: Font{Family: "Segoe UI", PointSize: 17, Bold: true}, TextColor: headingText}, HSpacer{},
				Label{AssignTo: &a.status, Text: "Disconnected", TextColor: mutedText, Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}},
				PushButton{AssignTo: &a.themeToggle, Text: "☾", MinSize: Size{Width: 34, Height: 30}, MaxSize: Size{Width: 34, Height: 30}, OnClicked: a.toggleTheme},
			}},
			TabWidget{AssignTo: &a.tabs, StretchFactor: 1, Pages: []TabPage{a.connectionTab(), a.logTab(), a.settingsTab()}},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
				Label{AssignTo: &a.modeLabel, Text: "Add a profile", TextColor: mutedText}, HSpacer{},
				PushButton{AssignTo: &a.disconnect, Text: "Disconnect", MinSize: Size{Width: 116, Height: 34}, MaxSize: Size{Width: 116, Height: 34}, Enabled: false, OnClicked: a.stop},
				PushButton{AssignTo: &a.connect, Text: "Connect", MinSize: Size{Width: 128, Height: 34}, MaxSize: Size{Width: 128, Height: 34}, Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, OnClicked: a.start},
			}},
		},
	}).Create()
	if err != nil {
		return err
	}
	a.tabs.CurrentIndexChanged().Attach(a.changeTab)
	a.initializeProfiles()
	a.profileChoice.CurrentIndexChanged().Attach(a.selectProfile)
	a.initializePreferences()
	a.logView.SetLimit(a.log, a.preferences.LogLines)
	a.initializeVKAuth()
	a.window.Disposing().Attach(a.stopVKAuth)
	return nil
}

func (a *application) connectionTab() TabPage {
	return TabPage{Title: "Connection", Layout: VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 14}, Spacing: 12}, Children: []Widget{
		Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVCenter, Spacing: 6}, Children: []Widget{
			Label{Text: "Profile", TextColor: headingText, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}},
			Label{Text: "ⓘ", ToolTipText: "Choose a profile link, or a WireGuard configuration with a VK call link.", TextColor: mutedText}, HSpacer{},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
			ComboBox{AssignTo: &a.profileChoice, StretchFactor: 1},
			PushButton{AssignTo: &a.addProfileButton, Text: "Add", MinSize: Size{Width: 76}, MaxSize: Size{Width: 76}, OnClicked: a.addProfile},
			PushButton{AssignTo: &a.editProfileButton, Text: "Edit", MinSize: Size{Width: 76}, MaxSize: Size{Width: 76}, OnClicked: a.editProfile},
			PushButton{AssignTo: &a.removeProfileButton, Text: "Remove", MinSize: Size{Width: 82}, MaxSize: Size{Width: 82}, OnClicked: a.removeProfile},
		}},
		VSpacer{Size: 4},
		Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVCenter, Spacing: 6}, Children: []Widget{
			Label{Text: "VK account", TextColor: headingText, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}},
			Label{Text: "ⓘ", ToolTipText: "One VK account is used for all profiles. The session is checked automatically.", TextColor: mutedText}, HSpacer{},
		}},
		Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVCenter, Spacing: 8}, Children: []Widget{
			Label{AssignTo: &a.authStatus, Text: "Anonymous access", TextColor: mutedText}, HSpacer{},
			PushButton{AssignTo: &a.authButton, Text: "Sign in to VK", MinSize: Size{Width: 124, Height: 32}, MaxSize: Size{Width: 124, Height: 32}, OnClicked: a.openVKLogin},
			PushButton{AssignTo: &a.authDetailsButton, Text: "Cookies…", MinSize: Size{Width: 90, Height: 32}, MaxSize: Size{Width: 90, Height: 32}, OnClicked: a.manageCookies},
		}},
		VSpacer{},
		Label{AssignTo: &a.phaseLabel, Text: "", TextColor: mutedText, MinSize: Size{Height: 18}, Font: Font{Family: "Segoe UI", PointSize: 9}},
		CustomWidget{AssignTo: &a.progress, MinSize: Size{Height: 58}, MaxSize: Size{Height: 58}, PaintPixels: a.paintProgress},
	}}
}

func (a *application) logTab() TabPage {
	return TabPage{Title: "Log", Layout: VBox{Margins: Margins{Left: 12, Top: 14, Right: 12, Bottom: 12}, Spacing: 8}, Children: []Widget{
		TextEdit{AssignTo: &a.log, ReadOnly: true, VScroll: true, StretchFactor: 1, Font: Font{Family: "Consolas", PointSize: 9}},
	}}
}
func (a *application) changeTab() {
	if a.tabs.CurrentIndex() == 1 {
		a.logView.FollowTail(a.log)
	}
}
func (a *application) connectionProfile() ([]byte, error) {
	p, ok := a.selectedProfile()
	if !ok {
		return nil, errors.New(a.tr("Add a profile first."))
	}
	return p.connectionProfile()
}
