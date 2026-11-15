//go:build darwin

// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"errors"
)

func loginVK(ctx context.Context, language string) (string, error) {
	return "", errors.New(loginText(language, "website login is supported on Windows and Linux; import VK cookies"))
}
