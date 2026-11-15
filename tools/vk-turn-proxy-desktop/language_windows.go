//go:build windows

// SPDX-License-Identifier: MIT

package main

import (
	"golang.org/x/sys/windows"
	"strings"
)

func nativeLanguage() string {
	languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(languages) == 0 {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(languages[0]), "ru") {
		return "ru"
	}
	return defaults.DefaultLanguage
}
