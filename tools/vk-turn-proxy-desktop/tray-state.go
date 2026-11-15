// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"runtime"
)

//go:embed tray-logo.png
var trayLogoPNG []byte

type TrayCallbacks struct {
	Show, Connect, Disconnect, Exit func()
}

type Tray interface {
	Update(state, language string)
	Available() bool
	Close()
}

type trayPresentation struct {
	status, show, connect, disconnect, exit string
	canConnect, canDisconnect               bool
	color                                   color.RGBA
}

func describeTray(state, language string) trayPresentation {
	p := trayPresentation{
		status: "Disconnected", show: "Open window", connect: "Connect",
		disconnect: "Disconnect", exit: "Exit", color: color.RGBA{114, 126, 145, 255},
		canConnect:    state == "disconnected" || state == "failed",
		canDisconnect: state != "disconnected" && state != "failed" && state != "disconnecting",
	}
	switch state {
	case "credentials", "relay", "handshake":
		p.status, p.color = "Connecting…", color.RGBA{67, 117, 242, 255}
	case "connected":
		p.status, p.color = "Connected", color.RGBA{34, 164, 119, 255}
	case "disconnecting":
		p.status = "Disconnecting…"
	case "failed":
		p.status, p.color = "Connection failed", color.RGBA{210, 70, 82, 255}
	}
	if language == "ru" {
		p.show, p.connect, p.disconnect, p.exit = "Открыть окно", "Подключить", "Отключить", "Выход"
		p.status = map[string]string{
			"Disconnected": "Отключено", "Connecting…": "Подключение…", "Connected": "Подключено",
			"Disconnecting…": "Отключение…", "Connection failed": "Ошибка подключения",
		}[p.status]
	}
	return p
}

func trayIcon(value color.RGBA) []byte {
	picture := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	logo, err := png.Decode(bytes.NewReader(trayLogoPNG))
	if err != nil {
		panic(err)
	}
	draw.Draw(picture, picture.Bounds(), logo, image.Point{}, draw.Src)
	for _, badge := range []struct {
		radius float64
		color  color.RGBA
	}{{6, color.RGBA{255, 255, 255, 255}}, {4.5, value}} {
		mask := image.NewAlpha(picture.Bounds())
		for y := range 32 {
			for x := range 32 {
				distance := math.Hypot(float64(x)-25.5, float64(y)-25.5)
				alpha := uint8(math.Round(255 * min(1.0, max(0.0, badge.radius-distance))))
				mask.SetAlpha(x, y, color.Alpha{A: alpha})
			}
		}
		draw.DrawMask(picture, picture.Bounds(), image.NewUniform(badge.color), image.Point{}, mask, image.Point{}, draw.Over)
	}
	var output bytes.Buffer
	_ = png.Encode(&output, picture)
	data := output.Bytes()
	if runtime.GOOS == "windows" {
		// Windows icon directories can contain PNG-encoded images.
		header := make([]byte, 22)
		binary.LittleEndian.PutUint16(header[2:], 1)
		binary.LittleEndian.PutUint16(header[4:], 1)
		header[6], header[7] = 32, 32
		binary.LittleEndian.PutUint16(header[10:], 1)
		binary.LittleEndian.PutUint16(header[12:], 32)
		binary.LittleEndian.PutUint32(header[14:], uint32(len(data)))
		binary.LittleEndian.PutUint32(header[18:], 22)
		data = append(header, data...)
	}
	return data
}
