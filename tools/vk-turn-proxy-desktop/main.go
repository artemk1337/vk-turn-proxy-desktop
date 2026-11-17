//go:build desktop

// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed frontend
var frontend embed.FS

func main() {
	smoke := slices.Contains(os.Args[1:], "--smoke-test")
	root, err := defaultDataDir()
	if err != nil {
		failStartup(err)
	}
	if smoke {
		root, err = os.MkdirTemp("", "vk-turn-desktop-smoke-")
		if err != nil {
			failStartup(err)
		}
		defer os.RemoveAll(root)
	}
	executable, err := os.Executable()
	if err != nil {
		failStartup(err)
	}
	backendName := "vk-turn-proxy-console"
	if runtime.GOOS == "windows" {
		backendName += ".exe"
	}
	var ctx context.Context
	app, err := newApp(root, filepath.Join(filepath.Dir(executable), backendName), func() { wailsruntime.Quit(ctx) })
	if err != nil {
		failStartup(err)
	}
	app.updateSave = func(name string) (string, error) {
		return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{DefaultFilename: name})
	}
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		failStartup(err)
	}
	var tray Tray
	var trayMu sync.Mutex
	closeTray := func() {
		trayMu.Lock()
		defer trayMu.Unlock()
		if tray != nil {
			tray.Close()
		}
	}
	var smokeFailed atomic.Bool
	background := options.NewRGB(252, 253, 255)
	windowTheme := windows.Light
	if app.preferences.Theme == "dark" {
		background = options.NewRGB(27, 32, 40)
		windowTheme = windows.Dark
	}
	err = wails.Run(&options.App{
		Title: "VK TURN Proxy", Width: 1000, Height: 740, MinWidth: 760, MinHeight: 560,
		BackgroundColour: background,
		Windows:          &windows.Options{Theme: windowTheme},
		AssetServer:      &assetserver.Options{Assets: assets},
		Bind:             []interface{}{app},
		OnStartup: func(startup context.Context) {
			ctx = startup
			if !smoke {
				app.startChecks()
				created, err := startTray(TrayCallbacks{
					Show: func() { wailsruntime.WindowShow(ctx); wailsruntime.WindowUnminimise(ctx) },
					Connect: func() {
						if _, err := app.Connect(); err != nil {
							app.recordError(err)
						}
					},
					Disconnect: func() {
						if _, err := app.Disconnect(); err != nil {
							app.recordError(err)
						}
					},
					Exit: func() { go app.Exit() },
				})
				if err == nil {
					trayMu.Lock()
					tray = created
					trayMu.Unlock()
				}
			}
		},
		OnDomReady: func(context.Context) {
			go func() {
				ticker := time.NewTicker(200 * time.Millisecond)
				defer ticker.Stop()
				previousTheme := ""
				for {
					state := app.GetState()
					if state.Preferences.Theme != previousTheme {
						previousTheme = state.Preferences.Theme
						if previousTheme == "dark" {
							wailsruntime.WindowSetDarkTheme(ctx)
							wailsruntime.WindowSetBackgroundColour(ctx, 27, 32, 40, 255)
						} else {
							wailsruntime.WindowSetLightTheme(ctx)
							wailsruntime.WindowSetBackgroundColour(ctx, 252, 253, 255, 255)
						}
					}
					trayMu.Lock()
					if tray != nil {
						tray.Update(state.Connection.State, state.Preferences.Language)
					}
					trayMu.Unlock()
					select {
					case <-app.ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			if smoke {
				go func() {
					select {
					case <-app.frontendReady:
						time.Sleep(time.Second)
						fmt.Println("Wails smoke passed: native window, frontend bridge and private storage")
					case <-time.After(15 * time.Second):
						smokeFailed.Store(true)
						fmt.Fprintln(os.Stderr, "Wails smoke failed: frontend did not initialize")
					}
					app.Exit()
				}()
			}
		},
		OnBeforeClose: func(context.Context) bool {
			app.mu.Lock()
			closing := app.closing
			app.mu.Unlock()
			if closing {
				return false
			}
			trayMu.Lock()
			hasTray := tray != nil && tray.Available()
			trayMu.Unlock()
			if hasTray {
				wailsruntime.WindowHide(ctx)
			} else {
				go app.Exit()
			}
			return true
		},
		OnShutdown: func(context.Context) { app.shutdown(); closeTray() },
	})
	app.shutdown()
	closeTray()
	if err != nil {
		failStartup(err)
	}
	if smokeFailed.Load() {
		os.Exit(1)
	}
}

func failStartup(err error) {
	fmt.Fprintln(os.Stderr, "VK TURN Proxy:", err)
	showStartupError(err.Error())
	os.Exit(1)
}
