// SPDX-License-Identifier: MIT

package vkauth

// Cookie imports accept Netscape files and Cookie headers. Authentication uses
// remixsid from vk.com or vk.ru and p from login.vk.com or login.vk.ru.
// Only the freshest valid pair is sent. Never include cookie values in errors.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ParseCookieInput accepts an exported cookie jar or a pasted Cookie header.
func ParseCookieInput(r io.Reader) ([]Cookie, error) {
	data, err := io.ReadAll(io.LimitReader(r, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, errors.New("cookie input exceeds 4 MiB")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if strings.Contains(text, "\t") || strings.HasPrefix(text, "#") {
		return ParseNetscapeCookies(strings.NewReader(text))
	}
	if len(text) >= len("Cookie:") && strings.EqualFold(text[:len("Cookie:")], "Cookie:") {
		text = strings.TrimSpace(text[len("Cookie:"):])
	}
	if strings.ContainsAny(text, "\r\n") {
		return nil, errors.New("a Cookie header must occupy one line")
	}
	var out []Cookie
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, errors.New("expected Netscape cookies or remixsid=...; p=...")
		}
		domain := ".vk.com"
		switch name {
		case "remixsid":
		case "p":
			domain = ".login.vk.com"
		default:
			continue
		}
		out = append(out, Cookie{domain: domain, name: name, value: value})
	}
	return out, nil
}

type Cookie struct {
	domain, name, value string
	expires             time.Time // zero: a session cookie
}

// ParseNetscapeCookies reads the seven tab-separated fields of each line:
// domain, subdomains flag, path, secure flag, expiry (unix seconds, 0 = session),
// name, value. "#HttpOnly_" before a domain is curl's marker for an HttpOnly
// cookie, not a comment — VK's auth cookies are HttpOnly.
func ParseNetscapeCookies(r io.Reader) ([]Cookie, error) {
	var out []Cookie
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("line %d: %d tab-separated fields, a Netscape cookie line has 7", n, len(f))
		}
		c := Cookie{domain: strings.ToLower(f[0]), name: f[5], value: f[6]}
		exp, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: the expiry field is not a number", n)
		}
		if exp > 0 {
			c.expires = time.Unix(exp, 0)
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// domainUnder: the cookie's domain is suffix itself or one of its subdomains.
func domainUnder(domain, suffix string) bool {
	d := strings.TrimPrefix(domain, ".")
	return d == suffix || strings.HasSuffix(d, "."+suffix)
}

// CookieHeader picks remixsid (.vk.com / .vk.ru) and p (.login.vk.com /
// .login.vk.ru), unexpired, the one that lives longest of each, and returns
// the app's header with the pair's earlier expiry (zero if both are session
// cookies).
func CookieHeader(cookies []Cookie, now time.Time) (string, time.Time, error) {
	pick := func(name string, suffixes ...string) (*Cookie, error) {
		var best *Cookie
		expired := false
		for i := range cookies {
			c := &cookies[i]
			if c.name != name || c.value == "" {
				continue
			}
			under := false
			for _, s := range suffixes {
				if domainUnder(c.domain, s) {
					under = true
				}
			}
			if !under {
				continue
			}
			if !c.expires.IsZero() && !c.expires.After(now) {
				expired = true
				continue
			}
			if best == nil || (!best.expires.IsZero() && (c.expires.IsZero() || c.expires.After(best.expires))) {
				best = c
			}
		}
		if best == nil {
			if expired {
				return nil, fmt.Errorf("the %s cookie in the file has expired — log in to VK again and export the cookies anew", name)
			}
			return nil, fmt.Errorf("no %s cookie of %s in the file", name, strings.Join(suffixes, " / "))
		}
		return best, nil
	}
	sid, err := pick("remixsid", "vk.com", "vk.ru")
	if err != nil {
		return "", time.Time{}, err
	}
	p, err := pick("p", "login.vk.com", "login.vk.ru")
	if err != nil {
		return "", time.Time{}, err
	}
	if strings.ContainsFunc(sid.value+p.value, func(r rune) bool { return r <= 32 || r == 127 || r == ';' }) {
		return "", time.Time{}, errors.New("a cookie value holds a separator — the file is damaged")
	}
	exp := sid.expires
	if exp.IsZero() || (!p.expires.IsZero() && p.expires.Before(exp)) {
		exp = p.expires
	}
	return "remixsid=" + sid.value + "; p=" + p.value, exp, nil
}
