//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const macElevationScript = `on run arguments
set commandText to "exec "
repeat with argument in arguments
set commandText to commandText & quoted form of argument & " "
end repeat
do shell script commandText with administrator privileges
end run`

// The watchdog also owns the authorization prompt before the privileged console connects.
const macElevationLauncher = `directory=$1
stop=$2
script=$3
shift 3
group=$$
watchdog=""
cleanup() {
  trap '' TERM
  kill -TERM -"$group" 2>/dev/null
  [ -z "$watchdog" ] || wait "$watchdog" 2>/dev/null
  /bin/rm -rf "$directory"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'if [ -f "$stop" ]; then exit 0; else exit 143; fi' TERM
(
  IFS= read -r line <&3
  kill -TERM -"$group" 2>/dev/null
) &
watchdog=$!
/usr/bin/osascript -e "$script" "$@" &
authorization=$!
wait "$authorization"
status=$?
exit "$status"
`

func macCommand(backend string, args []string, directory, stopFile string) *exec.Cmd {
	argv := []string{"-c", macElevationLauncher, "vk-turn-desktop", directory, stopFile, macElevationScript, backend}
	argv = append(argv, args...)
	argv = append(argv, "-desktop-log-socket", filepath.Join(directory, "log.sock"))
	command := exec.Command("/bin/sh", argv...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return command
}

func rootSocketPeer(connection *net.UnixConn) bool {
	raw, err := connection.SyscallConn()
	if err != nil {
		return false
	}
	var credentials *unix.Xucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, peerErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil || peerErr != nil {
		return false
	}
	return credentials.Uid == 0
}

func startPlatformCommand(backend string, args []string, output *os.File, stopFile string) (*exec.Cmd, func(), error) {
	directory, err := os.MkdirTemp("/tmp", "vk-turn-desktop-")
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(directory, "log.sock"), Net: "unix"})
	if err != nil {
		os.RemoveAll(directory)
		return nil, nil, err
	}
	if err := os.Chmod(listener.Addr().String(), 0600); err != nil {
		listener.Close()
		os.RemoveAll(directory)
		return nil, nil, err
	}
	fd, err := syscall.Dup(int(output.Fd()))
	if err != nil {
		listener.Close()
		os.RemoveAll(directory)
		return nil, nil, err
	}
	syscall.CloseOnExec(fd)
	streamOutput := os.NewFile(uintptr(fd), "desktop-console-output")
	parentReader, parentWriter, err := os.Pipe()
	if err != nil {
		streamOutput.Close()
		listener.Close()
		os.RemoveAll(directory)
		return nil, nil, err
	}
	command := macCommand(backend, args, directory, stopFile)
	command.ExtraFiles = []*os.File{parentReader}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		parentReader.Close()
		parentWriter.Close()
		streamOutput.Close()
		listener.Close()
		os.RemoveAll(directory)
		return nil, nil, err
	}
	parentReader.Close()

	accepted := make(chan struct{})
	streamDone := make(chan struct{})
	finished := make(chan struct{})
	var connection *net.UnixConn
	go func() {
		defer close(streamDone)
		defer streamOutput.Close()
		defer listener.Close()
		for {
			candidate, err := listener.AcceptUnix()
			if err != nil {
				close(accepted)
				return
			}
			if !rootSocketPeer(candidate) {
				candidate.Close()
				continue
			}
			connection = candidate
			close(accepted)
			_, _ = io.Copy(streamOutput, connection)
			connection.Close()
			return
		}
	}()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-accepted:
				return
			case <-finished:
				return
			case <-ticker.C:
				if _, err := os.Stat(stopFile); err == nil {
					_ = command.Process.Signal(syscall.SIGTERM)
					return
				}
			}
		}
	}()
	finish := func() {
		close(finished)
		parentWriter.Close()
		listener.Close()
		<-accepted
		if connection != nil {
			_, _ = connection.Write([]byte{0})
		}
		<-streamDone
		os.RemoveAll(directory)
	}
	return command, finish, nil
}
