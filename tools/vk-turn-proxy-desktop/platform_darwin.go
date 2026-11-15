//go:build darwin

// SPDX-License-Identifier: MIT
package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
void *vkLoginOpen(const char *title, const char *hint);
char *vkLoginRead(void *handle, int *closed);
void vkLoginClose(void *handle);
*/
import "C"

import (
	"context"
	"strings"
	"time"
	"unsafe"

	vkauth "github.com/artemk1337/vk-turn-proxy-desktop/pkg/vk-auth"
)

func loginVK(ctx context.Context, language string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title := C.CString(loginText(language, "Sign in to VK"))
	hint := C.CString(loginText(language, "Complete VK login. This window closes when the required cookies are ready."))
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(hint))
	handle := C.vkLoginOpen(title, hint)
	defer C.vkLoginClose(handle)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			var closed C.int
			result := C.vkLoginRead(handle, &closed)
			text := C.GoString(result)
			C.free(unsafe.Pointer(result))
			if closed != 0 {
				return "", nil
			}
			if macLoginCookiesReady(text, time.Now()) {
				return text, nil
			}
		}
	}
}

func macLoginCookiesReady(text string, now time.Time) bool {
	cookies, err := vkauth.ParseCookieInput(strings.NewReader(text))
	if err != nil {
		return false
	}
	_, _, err = vkauth.CookieHeader(cookies, now)
	return err == nil
}
