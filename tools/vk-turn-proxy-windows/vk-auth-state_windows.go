// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	vkauth "github.com/artemk1337/vk-turn-proxy-desktop/pkg/vk-auth"
	"golang.org/x/sys/windows"
)

type vkAuthState struct {
	storedCookies string
	status        vkauth.Status
	checking      bool
	cancel        context.CancelFunc
	requestCancel context.CancelFunc
	done          <-chan struct{}
}

func (a *application) currentVKCookies() string {
	text, _ := a.readVKCookies()
	return text
}

func (a *application) readVKCookies() (string, error) {
	if a.vkCookieText != "" {
		return a.vkCookieText, nil
	}
	if a.vkCookieFile == "" {
		return "", nil
	}
	file, err := os.Open(a.vkCookieFile)
	if err != nil {
		return "", errors.New("VK cookie file could not be opened.")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return "", errors.New("VK cookie file could not be read or exceeds 4 MiB.")
	}
	return string(data), nil
}

func (a *application) initializeVKAuth() {
	ctx, cancel := context.WithCancel(context.Background())
	a.vkAuth.cancel = cancel
	a.vkAuth.done = ctx.Done()
	text, err := loadVKAuth(filepath.Join(filepath.Dir(a.preferencesPath), "vk-auth.bin"))
	if err != nil {
		a.fail(errors.New("VK session could not be loaded. Sign in again."))
	} else if text != "" {
		a.vkCookieText = text
		a.vkAuth.storedCookies = text
		a.vkCookieFile = ""
	}
	a.updateVKStatus()
	a.checkVKAuth()
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.window.IsDisposed() {
					return
				}
				a.window.Synchronize(a.checkVKAuth)
			}
		}
	}()
}

func (a *application) stopVKAuth() {
	if a.vkAuth.cancel != nil {
		a.vkAuth.cancel()
	}
	if a.vkAuth.requestCancel != nil {
		a.vkAuth.requestCancel()
	}
}

func (a *application) saveVKAuth() {
	text, err := a.readVKCookies()
	if err == nil && text != "" {
		var cookies []vkauth.Cookie
		cookies, err = vkauth.ParseCookieInput(strings.NewReader(text))
		if err == nil {
			_, _, err = vkauth.CookieHeader(cookies, time.Now())
		}
	}
	if err == nil {
		err = storeVKAuth(filepath.Join(filepath.Dir(a.preferencesPath), "vk-auth.bin"), text)
	}
	if err != nil {
		a.vkCookieText = a.vkAuth.storedCookies
		a.vkCookieFile = ""
		a.fail(err)
		return
	}
	a.vkAuth.storedCookies = text
	a.vkCookieText = text
	a.vkCookieFile = ""
	a.vkAuth.status = vkauth.Unknown
	if a.vkAuth.requestCancel != nil {
		a.vkAuth.requestCancel()
	}
	a.updateVKStatus()
}

func (a *application) clearVKAuth() {
	a.vkCookieText = ""
	a.vkCookieFile = ""
	a.saveVKAuth()
	a.updateVKStatus()
}

func (a *application) checkVKAuth() {
	if a.vkAuth.done != nil {
		select {
		case <-a.vkAuth.done:
			return
		default:
		}
	}
	if a.vkAuth.checking {
		return
	}
	text := a.currentVKCookies()
	if text == "" {
		a.vkAuth.status = vkauth.Unknown
		a.updateVKStatus()
		return
	}
	a.vkAuth.checking = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	a.vkAuth.requestCancel = cancel
	a.updateVKStatus()
	go func() {
		status := vkauth.Check(ctx, text)
		cancel()
		if a.window.IsDisposed() {
			return
		}
		a.window.Synchronize(func() {
			a.vkAuth.checking = false
			a.vkAuth.requestCancel = nil
			// A previous account's pending response must never update a new account.
			if a.currentVKCookies() != text {
				a.checkVKAuth()
				return
			}
			a.vkAuth.status = status
			a.updateVKStatus()
		})
	}()
}

func (a *application) updateVKStatus() {
	text := "VK signed out"
	color := a.palette.muted
	if a.currentVKCookies() != "" {
		text = "VK check unavailable"
		switch a.vkAuth.status {
		case vkauth.Valid:
			text = "VK connected"
			color = a.palette.success
		case vkauth.Rejected:
			text = "VK session expired"
			color = a.palette.danger
		}
	}
	if a.vkAuth.checking && a.currentVKCookies() != "" {
		text = "Checking VK…"
	}
	a.authStatus.SetText(a.tr(text))
	a.authStatus.SetTextColor(color)
	button := "Sign in to VK"
	if a.vkAuth.status == vkauth.Rejected {
		button = "Sign in again"
	}
	a.authButton.SetText(a.tr(button))
}

func loadVKAuth(path string) (string, error) {
	encrypted, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(encrypted) == 0 || len(encrypted) > 5<<20 {
		return "", errors.New("invalid VK session size")
	}
	input := windows.DataBlob{Size: uint32(len(encrypted)), Data: &encrypted[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	plain := unsafe.Slice(output.Data, output.Size)
	defer clear(plain)
	return string(plain), nil
}

func storeVKAuth(path, text string) error {
	if text == "" {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := secureDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	plain := []byte(text)
	defer clear(plain)
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	file, err := os.CreateTemp(filepath.Dir(path), ".vk-auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(unsafe.Slice(output.Data, output.Size)); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
