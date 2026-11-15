// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"image/color"
	"image/png"
	"runtime"
	"testing"
)

func TestTrayIconUsesLogoAndState(t *testing.T) {
	want := color.RGBA{34, 164, 119, 255}
	data := trayIcon(want)
	if runtime.GOOS == "windows" {
		data = data[22:]
	}
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if picture.Bounds().Dx() != 32 || picture.Bounds().Dy() != 32 {
		t.Fatal("tray icon dimensions changed")
	}
	if got := color.RGBAModel.Convert(picture.At(26, 26)).(color.RGBA); got != want {
		t.Fatalf("state badge color is %v, want %v", got, want)
	}
	for y := range 32 {
		for x := range 20 {
			if _, _, _, alpha := picture.At(x, y).RGBA(); alpha != 0 {
				return
			}
		}
	}
	t.Fatal("VT logo is missing outside the status badge")
}

func TestTrayPresentation(t *testing.T) {
	for _, test := range []struct {
		state, language, status string
		connect, disconnect     bool
	}{
		{"disconnected", "en", "Disconnected", true, false},
		{"credentials", "ru", "Подключение…", false, true},
		{"relay", "en", "Connecting…", false, true},
		{"handshake", "en", "Connecting…", false, true},
		{"connected", "ru", "Подключено", false, true},
		{"disconnecting", "en", "Disconnecting…", false, false},
		{"failed", "ru", "Ошибка подключения", true, false},
	} {
		t.Run(test.state+test.language, func(t *testing.T) {
			p := describeTray(test.state, test.language)
			if p.status != test.status || p.canConnect != test.connect || p.canDisconnect != test.disconnect {
				t.Fatalf("unexpected presentation %+v", p)
			}
			if p.show == "" || p.connect == "" || p.disconnect == "" || p.exit == "" {
				t.Fatal("tray labels must remain readable in both languages")
			}
		})
	}
}
