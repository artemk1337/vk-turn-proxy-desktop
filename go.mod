module github.com/artemk1337/vk-turn-proxy-desktop

go 1.26.0

require (
	github.com/andybalholm/brotli v1.2.0
	github.com/bogdanfinn/fhttp v0.6.8
	github.com/bogdanfinn/tls-client v1.14.0
	github.com/bogdanfinn/utls v1.7.7-barnius
	github.com/cbeuw/connutil v1.0.1
	github.com/energye/systray v1.0.3
	github.com/godbus/dbus/v5 v5.1.0
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.18.2
	github.com/lxn/walk v0.0.0-20210112085537-c389da54e794
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e
	github.com/pion/dtls/v3 v3.0.10
	github.com/pion/logging v0.2.4
	github.com/pion/rtp v1.10.1
	github.com/pion/srtp/v3 v3.0.10
	github.com/pion/stun/v3 v3.1.1
	github.com/pion/turn/v5 v5.0.2
	github.com/refraction-networking/utls v1.8.2
	github.com/wailsapp/go-webview2 v1.0.23
	github.com/wailsapp/wails/v2 v2.11.0
	golang.org/x/crypto v0.47.0
	golang.org/x/net v0.48.0
	golang.org/x/sys v0.40.0
	golang.org/x/term v0.39.0
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb
)

require (
	github.com/bdandy/go-errors v1.2.2 // indirect
	github.com/bdandy/go-socks4 v1.2.3 // indirect
	github.com/bep/debounce v1.2.1 // indirect
	github.com/bogdanfinn/quic-go-utls v1.0.9-utls // indirect
	github.com/bogdanfinn/websocket v1.5.5-barnius // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/jchv/go-winloader v0.0.0-20210711035445-715c2860da7e // indirect
	github.com/labstack/echo/v4 v4.13.3 // indirect
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/leaanthony/go-ansi-parser v1.6.1 // indirect
	github.com/leaanthony/gosod v1.0.4 // indirect
	github.com/leaanthony/slicer v1.6.0 // indirect
	github.com/leaanthony/u v1.1.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/transport/v4 v4.0.1 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/samber/lo v1.49.1 // indirect
	github.com/tam7t/hpkp v0.0.0-20160821193359-2b70b4024ed5 // indirect
	github.com/tkrajina/go-reflector v0.5.8 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/wailsapp/mimetype v1.4.1 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/text v0.35.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0 // indirect
)

replace github.com/lxn/walk => ./third_party/walk
