//go:build darwin

// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMacLoginCookiesReady(t *testing.T) {
	now := time.Unix(2000, 0)
	sid := "#HttpOnly_vk.ru\tTRUE\t/\tTRUE\t3000\tremixsid\tsession\n"
	token := "#HttpOnly_login.vk.ru\tTRUE\t/\tTRUE\t0\tp\ttoken\n"
	for _, test := range []struct {
		name, text string
		ready      bool
	}{
		{"empty", "", false},
		{"missing token", sid, false},
		{"missing session", token, false},
		{"valid pair", sid + token, true},
		{"expired session", "vk.ru\tTRUE\t/\tTRUE\t1000\tremixsid\tsession\n" + token, false},
		{"unrelated domain", "example.com\tTRUE\t/\tTRUE\t0\tremixsid\tsession\n" + token, false},
		{"invalid value", "vk.ru\tTRUE\t/\tTRUE\t0\tremixsid\tbad;value\n" + token, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := macLoginCookiesReady(test.text, now); got != test.ready {
				t.Fatalf("ready = %v, want %v", got, test.ready)
			}
		})
	}
}

func TestMacLoginCancelledBeforeOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	text, err := loginVK(ctx, "ru")
	if text != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled login returned text length %d, error %v", len(text), err)
	}
}
