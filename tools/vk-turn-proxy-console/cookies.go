// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"time"

	vkauth "github.com/artemk1337/vk-turn-proxy-desktop/pkg/vk-auth"
)

type fileCookie = vkauth.Cookie

func parseCookieInput(r io.Reader) ([]fileCookie, error)     { return vkauth.ParseCookieInput(r) }
func parseNetscapeCookies(r io.Reader) ([]fileCookie, error) { return vkauth.ParseNetscapeCookies(r) }
func vkCookieHeader(cookies []fileCookie, now time.Time) (string, time.Time, error) {
	return vkauth.CookieHeader(cookies, now)
}
