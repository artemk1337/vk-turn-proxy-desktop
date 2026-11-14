// SPDX-License-Identifier: MIT

package main

import (
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"path/filepath"
	"slices"
)

func (a *application) initializeProfiles() {
	a.profilesPath = filepath.Join(filepath.Dir(a.preferencesPath), "profiles.dat")
	value, err := loadProfileStore(a.profilesPath)
	a.profiles = value
	if err != nil {
		a.fail(err)
	}
	a.refreshProfiles()
}
func (a *application) selectedProfile() (savedProfile, bool) {
	for _, p := range a.profiles.Profiles {
		if p.ID == a.profiles.ActiveID {
			return p, true
		}
	}
	return savedProfile{}, false
}
func (a *application) refreshProfiles() {
	a.updatingProfiles = true
	names := make([]string, len(a.profiles.Profiles))
	index := -1
	for i, p := range a.profiles.Profiles {
		names[i] = p.Name
		if p.ID == a.profiles.ActiveID {
			index = i
		}
	}
	a.profileChoice.SetModel(names)
	a.profileChoice.SetCurrentIndex(index)
	a.updatingProfiles = false
	a.setRunning(a.command != nil)
	a.updateModeLabel()
}
func (a *application) selectProfile() {
	if a.updatingProfiles {
		return
	}
	index := a.profileChoice.CurrentIndex()
	if index < 0 || index >= len(a.profiles.Profiles) {
		return
	}
	value := a.profiles
	value.ActiveID = value.Profiles[index].ID
	a.persistProfiles(value)
}
func (a *application) persistProfiles(value profileStore) {
	if err := saveProfileStore(a.profilesPath, value); err != nil {
		a.fail(err)
		a.refreshProfiles()
		return
	}
	a.profiles = value
	a.refreshProfiles()
}
func (a *application) addProfile() { a.editSavedProfile(savedProfile{}) }
func (a *application) editProfile() {
	if p, ok := a.selectedProfile(); ok {
		a.editSavedProfile(p)
	}
}
func (a *application) editSavedProfile(profile savedProfile) {
	value, ok, err := a.openProfileEditor(profile)
	if err != nil {
		a.fail(err)
		return
	}
	if !ok {
		return
	}
	store := profileStore{Profiles: slices.Clone(a.profiles.Profiles), ActiveID: value.ID}
	found := false
	for i, p := range store.Profiles {
		if p.ID == value.ID {
			store.Profiles[i] = value
			found = true
			break
		}
	}
	if !found {
		store.Profiles = append(store.Profiles, value)
	}
	a.persistProfiles(store)
}
func (a *application) removeProfile() {
	index := a.profileChoice.CurrentIndex()
	if index < 0 {
		return
	}
	store := profileStore{Profiles: slices.Delete(slices.Clone(a.profiles.Profiles), index, index+1)}
	if len(store.Profiles) > 0 {
		store.ActiveID = store.Profiles[0].ID
	}
	a.persistProfiles(store)
}
func (a *application) manageCookies() {
	var dialog *walk.Dialog
	var text *walk.TextEdit
	var save *walk.PushButton
	err := (Dialog{AssignTo: &dialog, Title: a.tr("Cookies"), MinSize: Size{Width: 480, Height: 280}, Size: Size{Width: 520, Height: 320}, Layout: VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}, Spacing: 10}, DefaultButton: &save, Children: []Widget{
		TextEdit{AssignTo: &text, Text: a.currentVKCookies(), MaxLength: maxProfileSize, VScroll: true, StretchFactor: 1, Font: Font{Family: "Consolas", PointSize: 9}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			PushButton{Text: a.tr("Import file…"), OnClicked: func() { dialog.Cancel(); a.openCookies() }},
			PushButton{Text: a.tr("Clear session"), OnClicked: func() { a.setCookieText("", ""); dialog.Accept() }}, HSpacer{},
			PushButton{Text: a.tr("Cancel"), OnClicked: func() { dialog.Cancel() }},
			PushButton{AssignTo: &save, Text: a.tr("Save"), OnClicked: func() { a.setCookieText(text.Text(), "text"); dialog.Accept() }},
		}},
	}}).Create(a.window)
	if err != nil {
		a.fail(err)
		return
	}
	defer dialog.Dispose()
	a.themeDialog(dialog)
	dialog.Run()
}
