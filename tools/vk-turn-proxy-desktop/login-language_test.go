// SPDX-License-Identifier: MIT
package main

import "testing"

func TestLoginLanguage(t *testing.T) {
	for _, item := range []struct{ language, text, want string }{
		{"ru", "Sign in to VK", "Вход в VK"},
		{"en", "Sign in to VK", "Sign in to VK"},
		{"ru", "WebView2 cookies: 0x80004005", "Cookies WebView2: 0x80004005"},
		{"ru", "native error", "native error"},
	} {
		if got := loginText(item.language, item.text); got != item.want {
			t.Errorf("loginText(%q,%q) = %q", item.language, item.text, got)
		}
	}
}
