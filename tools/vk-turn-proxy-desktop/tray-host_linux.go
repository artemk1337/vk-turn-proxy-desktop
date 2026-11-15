// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

func trayHostAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer connection.Close()
	watcher := connection.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	var result dbus.Variant
	err = watcher.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
		"org.kde.StatusNotifierWatcher", "IsStatusNotifierHostRegistered").Store(&result)
	available, _ := result.Value().(bool)
	return err == nil && available
}
