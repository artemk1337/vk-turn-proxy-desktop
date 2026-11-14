// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/walk"
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
	dialog, err := walk.NewDialog(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dialog.Dispose()
	browser, err := walk.NewComposite(dialog)
	if err != nil {
		t.Fatal(err)
	}
	dialog.SetLayout(walk.NewVBoxLayout())
	dialog.SetSize(walk.Size{Width: 600, Height: 600})
	login := &vkLogin{dialog: dialog, browser: browser, url: "about:blank"}
	dialog.Closing().Attach(func(_ *bool, _ walk.CloseReason) { login.closed = true })
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
					if !injected {
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
