//go:build windows

// SPDX-License-Identifier: MIT

package main

import (
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func showStartupError(message string) {
	win.MessageBox(0, windows.StringToUTF16Ptr(message), windows.StringToUTF16Ptr("VK TURN Proxy"), win.MB_OK|win.MB_ICONERROR)
}
