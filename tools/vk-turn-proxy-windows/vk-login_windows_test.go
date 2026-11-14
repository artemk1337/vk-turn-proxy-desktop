// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"github.com/lxn/walk"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

func TestWebHandlerRetainsNativeReferences(t *testing.T) {
	called := false
	handler := newWebHandler("{5A4F5069-5C15-47C3-8646-F4DE1C116670}", func(status, result uintptr) { called = status == 7 && result == 9 })
	object := uintptr(unsafe.Pointer(handler))
	webCall(object, 1)
	handler.release()
	runtime.GC()
	webCall(object, 3, 7, 9)
	if !called {
		t.Fatal("native callback lost its Go handler")
	}
	webRelease(object)
	webHandlers.Lock()
	retained := webHandlers.live[handler]
	webHandlers.Unlock()
	if retained {
		t.Fatal("released callback retained in registry")
	}
}

func TestWebView2LoginCapturesHttpOnlyCookies(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil || version == "" {
		if os.Getenv("VKTURN_REQUIRE_WEBVIEW2_TEST") == "1" {
			t.Fatal("WebView2 Runtime is required for this test run")
		}
		t.Skip("WebView2 Runtime is not installed")
	}
	login, err := newVKLoginDialog(nil, "Sign in to VK", "Complete VK login.", "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	dialog := login.dialog
	defer dialog.Dispose()
	defer func() {
		login.closed = true
		if login.controller != 0 {
			webCall(login.controller, 24)
		}
		webRelease(login.manager)
		webRelease(login.view)
		webRelease(login.controller)
	}()
	profile, err := os.MkdirTemp("", "vk-webview-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for range 30 {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(time.Second)
		}
		t.Error("WebView2 retained its temporary profile after shutdown")
	})
	dialog.Starting().Attach(func() {
		if err := webviewloader.CreateCoreWebView2EnvironmentWithOptions(login, webviewloader.WithUserDataFolder(profile)); err != nil {
			login.finishError(err)
		}
	})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(30 * time.Second)
		injected := false
		for {
			select {
			case <-stop:
				return
			case <-timeout:
				dialog.Synchronize(func() {
					if !login.closed {
						login.finishError(windows.ERROR_TIMEOUT)
					}
				})
				return
			case <-ticker.C:
				dialog.Synchronize(func() {
					if login.closed || login.manager == 0 {
						return
					}
					bounds := login.browser.ClientBoundsPixels()
					if bounds.Width < 400 || bounds.Height < 400 {
						if directory := os.Getenv("VKTURN_UI_CAPTURE_DIR"); directory != "" {
							_ = captureLoginWindow(dialog, filepath.Join(directory, "vk-login-layout-failure.png"))
						}
						login.finishError(fmt.Errorf("VK browser layout too small: browser=%+v dialog=%+v", bounds, dialog.ClientBoundsPixels()))
						return
					}
					if !injected {
						if directory := os.Getenv("VKTURN_UI_CAPTURE_DIR"); directory != "" {
							_ = captureLoginWindow(dialog, filepath.Join(directory, "vk-login-browser.png"))
						}
						injected = true
						for _, item := range [][3]string{{"remixsid", "fixture-session", ".vk.ru"}, {"p", "fixture-token", ".login.vk.ru"}} {
							name, _ := windows.UTF16PtrFromString(item[0])
							value, _ := windows.UTF16PtrFromString(item[1])
							domain, _ := windows.UTF16PtrFromString(item[2])
							path, _ := windows.UTF16PtrFromString("/")
							var cookie uintptr
							hr := webCall(login.manager, 3, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(value)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&cookie)))
							if int32(hr) < 0 {
								login.finishError(windows.Errno(hr))
								return
							}
							webCall(cookie, 11, 1) // HttpOnly cookies cannot be read with document.cookie.
							hr = webCall(login.manager, 6, cookie)
							webRelease(cookie)
							if int32(hr) < 0 {
								login.finishError(windows.Errno(hr))
								return
							}
						}
					}
					login.poll()
				})
			}
		}
	}()
	dialog.Run()
	if login.err != nil {
		t.Fatal(login.err)
	}
	if !strings.Contains(login.text, "remixsid\tfixture-session") || !strings.Contains(login.text, "p\tfixture-token") {
		t.Fatal("required HttpOnly cookies were not captured")
	}
}

// Exercise the actual dashboard button and its registered production callback.
func TestDashboardVKButtonOpensLoginWindow(t *testing.T) {
	if version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil || version == "" {
		if os.Getenv("VKTURN_REQUIRE_WEBVIEW2_TEST") == "1" {
			t.Fatal("WebView2 Runtime is required for this test run")
		}
		t.Skip("WebView2 Runtime is not installed")
	}
	a := newPreferencesWindow(t, "en", "light")
	opened := false
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.NewTimer(10 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case <-finished:
				return
			case <-timeout.C:
				a.window.Synchronize(func() {
					popup := win.GetWindow(a.window.Handle(), win.GW_ENABLEDPOPUP)
					if popup != 0 && popup != a.window.Handle() {
						win.PostMessage(popup, win.WM_CLOSE, 0, 0)
					}
					a.window.Close()
				})
				return
			case <-ticker.C:
				a.window.Synchronize(func() {
					popup := win.GetWindow(a.window.Handle(), win.GW_ENABLEDPOPUP)
					if popup == 0 || popup == a.window.Handle() || !win.IsWindowVisible(popup) {
						return
					}
					var bounds win.RECT
					if win.GetWindowRect(popup, &bounds) && bounds.Right-bounds.Left >= 540 && bounds.Bottom-bounds.Top >= 580 {
						opened = true
						win.PostMessage(popup, win.WM_CLOSE, 0, 0)
					}
				})
			}
		}
	}()
	time.AfterFunc(50*time.Millisecond, func() {
		a.window.Synchronize(func() {
			win.SendMessage(a.authButton.Handle(), win.BM_CLICK, 0, 0)
			a.window.Close()
		})
	})
	a.window.Run()
	if !opened {
		t.Fatal("dashboard Sign in to VK button did not open the browser login window")
	}
}

func captureLoginWindow(dialog *walk.Dialog, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	bitmap, err := walk.NewBitmapForDPI(dialog.SizePixels(), dialog.DPI())
	if err != nil {
		return err
	}
	defer bitmap.Dispose()
	canvas, err := walk.NewCanvasFromImage(bitmap)
	if err != nil {
		return err
	}
	result, _, printError := windows.NewLazySystemDLL("user32.dll").NewProc("PrintWindow").Call(uintptr(dialog.Handle()), uintptr(canvas.HDC()), 2)
	canvas.Dispose()
	if result == 0 {
		return fmt.Errorf("capture VK login: %w", printError)
	}
	image, err := bitmap.ToImage()
	if err != nil {
		return err
	}
	for i := 3; i < len(image.Pix); i += 4 {
		image.Pix[i] = 255
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, image)
}
