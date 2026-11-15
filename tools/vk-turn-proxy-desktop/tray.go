// SPDX-License-Identifier: MIT
//go:build windows || linux

package main

import (
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/energye/systray"
)

type desktopTray struct {
	mu                                      sync.Mutex
	status, show, connect, disconnect, exit *systray.MenuItem
	state, language                         string
	closed                                  bool
}

func startTray(callbacks TrayCallbacks) (Tray, error) {
	if !trayHostAvailable() {
		return nil, errors.New("no desktop tray host is available")
	}
	t := &desktopTray{}
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		systray.Run(func() {
			t.mu.Lock()
			t.status = systray.AddMenuItem("Disconnected", "")
			t.status.Disable()
			systray.AddSeparator()
			t.show = systray.AddMenuItem("Open window", "")
			t.connect = systray.AddMenuItem("Connect", "")
			t.disconnect = systray.AddMenuItem("Disconnect", "")
			systray.AddSeparator()
			t.exit = systray.AddMenuItem("Exit", "")
			t.show.Click(callbacks.Show)
			t.connect.Click(callbacks.Connect)
			t.disconnect.Click(callbacks.Disconnect)
			t.exit.Click(callbacks.Exit)
			systray.SetOnClick(func(systray.IMenu) { callbacks.Show() })
			t.mu.Unlock()
			t.Update("disconnected", "en")
			close(ready)
		}, func() {})
	}()
	select {
	case <-ready:
		return t, nil
	case <-time.After(5 * time.Second):
		systray.Quit()
		return nil, errors.New("desktop tray initialization timed out")
	}
}

func (t *desktopTray) Update(state, language string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || (t.state == state && t.language == language) {
		return
	}
	p := describeTray(state, language)
	t.status.SetTitle(p.status)
	t.show.SetTitle(p.show)
	t.connect.SetTitle(p.connect)
	t.disconnect.SetTitle(p.disconnect)
	t.exit.SetTitle(p.exit)
	if p.canConnect {
		t.connect.Enable()
	} else {
		t.connect.Disable()
	}
	if p.canDisconnect {
		t.disconnect.Enable()
	} else {
		t.disconnect.Disable()
	}
	systray.SetTitle("VK TURN Proxy")
	systray.SetTooltip("VK TURN Proxy · " + p.status)
	if t.state != state {
		systray.SetIcon(trayIcon(p.color))
	}
	t.state, t.language = state, language
}

func (t *desktopTray) Available() bool {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	return !closed && trayHostAvailable()
}

func (t *desktopTray) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		systray.Quit()
	}
}
