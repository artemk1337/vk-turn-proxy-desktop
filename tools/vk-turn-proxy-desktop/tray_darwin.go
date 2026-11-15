// SPDX-License-Identifier: MIT

package main

import "errors"

func startTray(TrayCallbacks) (Tray, error) {
	return nil, errors.New("desktop tray is supported on Windows and Linux")
}
