// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

// WebView2 COM slots follow the Microsoft WebView2 Win32 interfaces. Keep this
// small adapter independent of generated wrappers with incomplete base vtables.
//
//go:uintptrescapes
func webCall(object uintptr, slot int, args ...uintptr) uintptr {
	table := *(*uintptr)(unsafe.Pointer(object))
	method := *(*uintptr)(unsafe.Pointer(table + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	result, _, _ := syscall.SyscallN(method, append([]uintptr{object}, args...)...)
	return result
}

func webRelease(object uintptr) {
	if object != 0 {
		webCall(object, 2)
	}
}

// Native COM owns references to asynchronous handlers. The registry keeps Go
// objects alive until WebView2 releases its last reference, including on cancel.
var webHandlers = struct {
	sync.Mutex
	live map[*webHandler]bool
}{live: make(map[*webHandler]bool)}

type webHandler struct {
	vtable *[4]uintptr
	refs   atomic.Int32
	iid    windows.GUID
	invoke func(uintptr, uintptr)
}

var webHandlerVTable = [4]uintptr{
	windows.NewCallback(func(this, iid, result uintptr) uintptr {
		handler := (*webHandler)(unsafe.Pointer(this))
		requested := *(*windows.GUID)(unsafe.Pointer(iid))
		unknown := windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
		if requested != unknown && requested != handler.iid {
			*(*uintptr)(unsafe.Pointer(result)) = 0
			return 0x80004002
		}
		*(*uintptr)(unsafe.Pointer(result)) = this
		handler.refs.Add(1)
		return 0
	}),
	windows.NewCallback(func(this uintptr) uintptr {
		return uintptr((*webHandler)(unsafe.Pointer(this)).refs.Add(1))
	}),
	windows.NewCallback(func(this uintptr) uintptr {
		handler := (*webHandler)(unsafe.Pointer(this))
		refs := handler.refs.Add(-1)
		if refs == 0 {
			webHandlers.Lock()
			delete(webHandlers.live, handler)
			webHandlers.Unlock()
		}
		return uintptr(refs)
	}),
	windows.NewCallback(func(this, status, result uintptr) uintptr {
		(*webHandler)(unsafe.Pointer(this)).invoke(status, result)
		return 0
	}),
}

func newWebHandler(iid string, invoke func(uintptr, uintptr)) *webHandler {
	guid, _ := windows.GUIDFromString(iid)
	handler := &webHandler{vtable: &webHandlerVTable, iid: guid, invoke: invoke}
	handler.refs.Store(1)
	webHandlers.Lock()
	webHandlers.live[handler] = true
	webHandlers.Unlock()
	return handler
}

func (h *webHandler) release() { webRelease(uintptr(unsafe.Pointer(h))) }

type vkLogin struct {
	url                       string
	dialog                    *walk.Dialog
	browser                   *walk.Composite
	controller, view, manager uintptr
	closed, pending           bool
	text                      string
	err                       error
}

func (a *application) openVKLogin() {
	if version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil || version == "" {
		if walk.MsgBox(a.window, a.tr("VK login"), a.tr("Install Microsoft Edge WebView2 Runtime to sign in through VK. Open the installer page?"), walk.MsgBoxYesNo|walk.MsgBoxIconInformation) == walk.DlgCmdYes {
			err := windows.ShellExecute(windows.Handle(a.window.Handle()), windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr("https://go.microsoft.com/fwlink/p/?LinkId=2124703"), nil, nil, windows.SW_SHOWNORMAL)
			if err != nil {
				a.fail(err)
			}
		}
		return
	}
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		a.fail(err)
		return
	}
	root = filepath.Join(root, "VKTurnProxyWindows")
	if err = secureDirectory(root); err != nil {
		a.fail(err)
		return
	}
	profile, err := os.MkdirTemp(root, "vk-login-")
	if err != nil {
		a.fail(err)
		return
	}
	login, err := newVKLoginDialog(a.window, a.tr("Sign in to VK"), a.tr("Complete VK login. This window closes when the required cookies are ready."), "https://vk.ru/")
	if err != nil {
		os.RemoveAll(profile)
		a.fail(err)
		return
	}
	defer login.dialog.Dispose()
	stop := make(chan struct{})
	defer close(stop)
	defer func() {
		login.closed = true
		if login.manager != 0 {
			webCall(login.manager, 10)
		} // DeleteAllCookies
		if login.controller != 0 {
			webCall(login.controller, 24)
		} // Close
		webRelease(login.manager)
		webRelease(login.view)
		webRelease(login.controller)
		// Browser subprocesses may briefly retain files after controller.Close.
		go func() {
			for range 30 {
				if os.RemoveAll(profile) == nil {
					return
				}
				time.Sleep(time.Second)
			}
		}()
	}()
	login.dialog.Starting().Attach(func() {
		if err := webviewloader.CreateCoreWebView2EnvironmentWithOptions(login, webviewloader.WithUserDataFolder(profile)); err != nil {
			login.finishError(err)
		}
	})
	go func() {
		startup := time.NewTimer(30 * time.Second)
		defer startup.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-startup.C:
				login.dialog.Synchronize(func() {
					if !login.closed && login.view == 0 {
						login.finishError(fmt.Errorf("WebView2 startup timed out"))
					}
				})
			case <-ticker.C:
				login.dialog.Synchronize(func() {
					if !login.closed {
						login.poll()
					}
				})
			}
		}
	}()
	login.dialog.Run()
	login.closed = true
	if login.err != nil {
		walk.MsgBox(a.window, a.tr("VK login"), a.tr("Could not complete VK website login. Try again or import cookies.")+"\n"+login.err.Error(), walk.MsgBoxIconError)
	} else if login.text != "" {
		a.setCookieText(login.text, "VK website login")
	}
}

// The production action and native tests share the complete browser layout.
func newVKLoginDialog(owner walk.Form, title, hintText, url string) (*vkLogin, error) {
	login := &vkLogin{url: url}
	var err error
	login.dialog, err = walk.NewDialog(owner)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*vkLogin, error) { login.dialog.Dispose(); return nil, err }
	login.dialog.SetTitle(title)
	login.dialog.Closing().Attach(func(_ *bool, _ walk.CloseReason) { login.closed = true })
	login.dialog.SetMinMaxSize(walk.Size{Width: 580, Height: 660}, walk.Size{})
	layout := walk.NewVBoxLayout()
	if err = login.dialog.SetLayout(layout); err != nil {
		return fail(err)
	}
	hint, err := walk.NewLabel(login.dialog)
	if err != nil {
		return fail(err)
	}
	hint.SetText(hintText)
	login.browser, err = walk.NewComposite(login.dialog)
	if err != nil {
		return fail(err)
	}
	// Walk excludes containers without a layout from their parent's layout.
	// The WebView is a native child, but its host must still participate.
	if err = login.browser.SetLayout(walk.NewVBoxLayout()); err != nil {
		return fail(err)
	}
	login.browser.SetMinMaxSize(walk.Size{Width: 540, Height: 580}, walk.Size{})
	layout.SetStretchFactor(login.browser, 1)
	login.browser.SizeChanged().Attach(login.resize)
	return login, nil
}

func (l *vkLogin) EnvironmentCompleted(status webviewloader.HRESULT, environment *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
	if l.closed {
		return 0
	}
	if status < 0 || environment == nil {
		l.finishError(fmt.Errorf("WebView2 environment: 0x%08x", uint32(status)))
		return 0
	}
	handler := newWebHandler("{6C4819F3-C9B7-4260-8127-C9F5BDE7F68C}", l.controllerReady)
	result := webCall(uintptr(unsafe.Pointer(environment)), 3, uintptr(l.browser.Handle()), uintptr(unsafe.Pointer(handler)))
	handler.release()
	if int32(result) < 0 {
		l.finishError(fmt.Errorf("WebView2 controller: 0x%08x", uint32(result)))
	}
	return 0
}

func (l *vkLogin) controllerReady(status, controller uintptr) {
	if int32(status) < 0 || controller == 0 {
		if !l.closed {
			l.finishError(fmt.Errorf("WebView2 controller: 0x%08x", uint32(status)))
		}
		return
	}
	if l.closed {
		webCall(controller, 24)
		return
	}
	webCall(controller, 1)
	l.controller = controller
	result := webCall(controller, 25, uintptr(unsafe.Pointer(&l.view))) // get_CoreWebView2
	if int32(result) < 0 {
		l.finishError(fmt.Errorf("WebView2 view: 0x%08x", uint32(result)))
		return
	}
	iid, _ := windows.GUIDFromString("{9E8F0CF8-E670-4B5E-B2BC-73E061E3184C}")
	var view2 uintptr
	result = webCall(l.view, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&view2)))
	if int32(result) < 0 {
		l.finishError(fmt.Errorf("WebView2 requires an updated Runtime"))
		return
	}
	result = webCall(view2, 66, uintptr(unsafe.Pointer(&l.manager))) // ICoreWebView2_2.get_CookieManager
	webRelease(view2)
	if int32(result) < 0 {
		l.finishError(fmt.Errorf("WebView2 cookie manager: 0x%08x", uint32(result)))
		return
	}
	l.resize()
	webCall(controller, 4, 1) // put_IsVisible
	url, _ := windows.UTF16PtrFromString(l.url)
	result = webCall(l.view, 5, uintptr(unsafe.Pointer(url))) // Navigate
	if int32(result) < 0 {
		l.finishError(fmt.Errorf("WebView2 navigation: 0x%08x", uint32(result)))
	}
}

func (l *vkLogin) resize() {
	if l.controller == 0 || l.closed {
		return
	}
	bounds := l.browser.ClientBoundsPixels()
	rect := [4]int32{0, 0, int32(bounds.Width), int32(bounds.Height)}
	// Windows ARM64 passes a 16-byte RECT in two registers. Windows x64 passes its address.
	if runtime.GOARCH == "arm64" {
		words := (*[2]uintptr)(unsafe.Pointer(&rect))
		webCall(l.controller, 6, words[0], words[1])
	} else {
		webCall(l.controller, 6, uintptr(unsafe.Pointer(&rect)))
	}
}

func (l *vkLogin) finishError(err error) {
	if l.closed {
		return
	}
	l.err = err
	l.dialog.Synchronize(func() {
		if !l.closed {
			l.dialog.Cancel()
		}
	})
}

func (l *vkLogin) poll() {
	if l.manager == 0 || l.pending {
		return
	}
	l.pending = true
	handler := newWebHandler("{5A4F5069-5C15-47C3-8646-F4DE1C116670}", l.cookiesReady)
	uri, _ := windows.UTF16PtrFromString("") // Empty URI returns all profile cookies, including HttpOnly.
	result := webCall(l.manager, 5, uintptr(unsafe.Pointer(uri)), uintptr(unsafe.Pointer(handler)))
	handler.release()
	if int32(result) < 0 {
		l.pending = false
		l.finishError(fmt.Errorf("WebView2 cookies: 0x%08x", uint32(result)))
	}
}

func webString(object uintptr, slot int) (string, bool) {
	var value *uint16
	result := webCall(object, slot, uintptr(unsafe.Pointer(&value)))
	if int32(result) < 0 {
		return "", false
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(value))
	return windows.UTF16PtrToString(value), true
}

func (l *vkLogin) cookiesReady(status, list uintptr) {
	l.pending = false
	if l.closed {
		return
	}
	if int32(status) < 0 || list == 0 {
		l.finishError(fmt.Errorf("WebView2 cookies: 0x%08x", uint32(status)))
		return
	}
	var count uint32
	if int32(webCall(list, 3, uintptr(unsafe.Pointer(&count)))) < 0 {
		return
	}
	var sid, token string
	for i := uint32(0); i < count; i++ {
		var cookie uintptr
		if int32(webCall(list, 4, uintptr(i), uintptr(unsafe.Pointer(&cookie)))) < 0 {
			continue
		}
		name, ok := webString(cookie, 3)
		domain, domainOK := webString(cookie, 6)
		domain = strings.TrimPrefix(strings.ToLower(domain), ".")
		wanted := (name == "remixsid" && (domain == "vk.com" || domain == "vk.ru")) ||
			(name == "p" && (domain == "login.vk.com" || domain == "login.vk.ru"))
		if ok && domainOK && wanted {
			value, valueOK := webString(cookie, 4)
			var expires float64
			var session int32
			expiryOK := int32(webCall(cookie, 8, uintptr(unsafe.Pointer(&expires)))) >= 0
			sessionOK := int32(webCall(cookie, 16, uintptr(unsafe.Pointer(&session)))) >= 0
			if valueOK && expiryOK && sessionOK && value != "" && !strings.ContainsAny(value, ";\r\n\t") && (session != 0 || expires > float64(time.Now().Unix())) {
				expiry := int64(expires)
				if session != 0 {
					expiry = 0
				}
				line := fmt.Sprintf("#HttpOnly_.%s\tTRUE\t/\tTRUE\t%d\t%s\t%s\n", domain, expiry, name, value)
				if name == "remixsid" {
					sid = line
				} else {
					token = line
				}
			}
		}
		webRelease(cookie)
	}
	if sid != "" && token != "" {
		l.text = "# Netscape HTTP Cookie File\n" + sid + token
		l.dialog.Accept()
	}
}
