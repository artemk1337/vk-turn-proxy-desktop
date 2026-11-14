// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows"
)

func (a *application) preparePreferences() error {
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return err
	}
	root = filepath.Join(root, "VKTurnProxyWindows")
	if err := secureDirectory(root); err != nil {
		return err
	}
	a.preferencesPath = filepath.Join(root, "preferences.json")
	language := "en"
	if languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME); err == nil && len(languages) > 0 && strings.HasPrefix(strings.ToLower(languages[0]), "ru") {
		language = "ru"
	}
	value, err := loadPreferences(a.preferencesPath, language)
	a.preferences = value
	if err != nil {
		a.preferencesError = err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if enabled, err := autostartEnabled(executable); err == nil {
		a.preferences.AutoStart = enabled
	}
	return nil
}

func (a *application) settingsTab() TabPage {
	return TabPage{
		Title:  "Settings",
		Layout: VBox{Margins: Margins{Left: 14, Top: 16, Right: 14, Bottom: 14}, Alignment: AlignHNearVNear, Spacing: 12},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVNear, Spacing: 20}, Children: []Widget{
				Composite{MinSize: Size{Width: 220}, MaxSize: Size{Width: 240}, Layout: VBox{MarginsZero: true, Alignment: AlignHNearVNear, Spacing: 7}, Children: []Widget{
					Label{Text: "Language", Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, TextColor: headingText},
					ComboBox{AssignTo: &a.languageChoice, Model: []string{"English", "Русский"}, CurrentIndex: 0, MinSize: Size{Width: 220}},
				}},
				Composite{MinSize: Size{Width: 220}, MaxSize: Size{Width: 240}, Layout: VBox{MarginsZero: true, Alignment: AlignHNearVNear, Spacing: 7}, Children: []Widget{
					Label{Text: "Appearance", Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, TextColor: headingText},
					ComboBox{AssignTo: &a.themeChoice, Model: []string{"Light", "Dark gray"}, CurrentIndex: 0, MinSize: Size{Width: 220}},
				}},
				HSpacer{},
			}},
			VSpacer{Size: 8},
			Composite{Layout: VBox{MarginsZero: true, Alignment: AlignHNearVNear, Spacing: 6}, Children: []Widget{
				Composite{Layout: HBox{MarginsZero: true, Alignment: AlignHNearVCenter, SpacingZero: true}, Children: []Widget{
					CheckBox{AssignTo: &a.startupChoice, Alignment: AlignHNearVCenter, Text: "Start with Windows", Checked: a.preferences.AutoStart},
					HSpacer{},
				}},
				Composite{Layout: HBox{Margins: Margins{Left: 22}, Alignment: AlignHNearVCenter, SpacingZero: true}, Children: []Widget{
					Label{Text: "Startup opens the app. Connect when you are ready.", TextColor: mutedText, Font: Font{Family: "Segoe UI", PointSize: 9}},
					HSpacer{},
				}},
			}},
			VSpacer{},
		},
	}
}

func (a *application) initializePreferences() {
	a.mutedLabels = make(map[*walk.Label]bool)
	a.collectText(a.window)
	for i := 0; i < a.tabs.Pages().Len(); i++ {
		a.collectText(a.tabs.Pages().At(i))
	}
	a.installTabPainting()
	a.installNativeControlPainting()
	a.applyLanguage()
	a.applyTheme()
	a.languageChoice.CurrentIndexChanged().Attach(func() {
		if a.updatingPreferences {
			return
		}
		a.preferences.Language = "en"
		if a.languageChoice.CurrentIndex() == 1 {
			a.preferences.Language = "ru"
		}
		a.applyLanguage()
		a.savePreferences()
	})
	a.themeChoice.CurrentIndexChanged().Attach(func() {
		if a.updatingPreferences {
			return
		}
		a.preferences.Theme = "light"
		if a.themeChoice.CurrentIndex() == 1 {
			a.preferences.Theme = "dark"
		}
		a.applyTheme()
		a.savePreferences()
	})
	a.startupChoice.CheckedChanged().Attach(a.changeAutostart)
	if a.preferencesError != nil {
		a.fail(fmt.Errorf("%s: %w", a.tr("Preferences could not be loaded"), a.preferencesError))
	}
}

func (a *application) savePreferences() {
	if err := savePreferences(a.preferencesPath, a.preferences); err != nil {
		a.fail(fmt.Errorf("%s: %w", a.tr("Preferences could not be saved"), err))
	}
}

func (a *application) changeAutostart() {
	if a.updatingPreferences {
		return
	}
	enabled := a.startupChoice.Checked()
	executable, err := os.Executable()
	if err != nil {
		a.fail(err)
		return
	}
	a.startupChoice.SetEnabled(false)
	go func() {
		err := setAutostart(enabled, executable)
		if a.window.IsDisposed() {
			return
		}
		a.window.Synchronize(func() {
			a.startupChoice.SetEnabled(true)
			if err != nil {
				a.updatingPreferences = true
				a.startupChoice.SetChecked(a.preferences.AutoStart)
				a.updatingPreferences = false
				a.fail(fmt.Errorf("%s: %w", a.tr("Windows startup could not be changed"), err))
				return
			}
			a.preferences.AutoStart = enabled
			a.savePreferences()
		})
	}()
}
