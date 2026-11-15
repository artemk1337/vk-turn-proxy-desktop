// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func darwinDesktopLogPair(t *testing.T) (*os.File, *net.UnixConn) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "vktp-log-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "log.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := openDesktopLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	return file, peer
}

func TestDarwinDesktopLogStreamsBeforeExit(t *testing.T) {
	file, peer := darwinDesktopLogPair(t)
	if _, err := file.WriteString("connection stage\n"); err != nil {
		t.Fatal(err)
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	data := make([]byte, len("connection stage\n"))
	if _, err := io.ReadFull(peer, data); err != nil || string(data) != "connection stage\n" {
		t.Fatalf("live log = %q, %v", data, err)
	}
}

func TestDarwinDesktopLogDisconnectCancelsTunnel(t *testing.T) {
	file, peer := darwinDesktopLogPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchDesktopLog(ctx, file, cancel)
	peer.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("desktop socket closure did not cancel the tunnel")
	}
}

func TestDarwinCanceledDesktopLogDoesNotStopLater(t *testing.T) {
	file, peer := darwinDesktopLogPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{}, 1)
	watchDesktopLog(ctx, file, func() { stopped <- struct{}{} })
	cancel()
	peer.Close()
	select {
	case <-stopped:
		t.Fatal("canceled desktop socket watcher called stop")
	case <-time.After(400 * time.Millisecond):
	}
}
