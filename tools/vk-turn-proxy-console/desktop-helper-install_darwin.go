//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func helperDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func dialDesktopHelper(digest string) (*net.UnixConn, error) {
	connection, err := net.DialTimeout("unix", helperSocket, time.Second)
	if err != nil {
		return nil, err
	}
	socket := connection.(*net.UnixConn)
	uid, err := helperPeerUID(socket)
	if err != nil || uid != 0 {
		socket.Close()
		return nil, errors.New("desktop helper peer is not root")
	}
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(socket, 8192)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		socket.Close()
		return nil, err
	}
	var frame helperFrame
	if err = json.Unmarshal(line, &frame); err != nil || frame.Digest != digest {
		socket.Close()
		return nil, errors.New("desktop helper update required")
	}
	_ = socket.SetReadDeadline(time.Time{})
	return socket, nil
}

func connectDesktopHelper(ctx context.Context) (*net.UnixConn, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	digest, err := helperDigest(executable)
	if err != nil {
		return nil, err
	}
	if connection, err := dialDesktopHelper(digest); err == nil {
		return connection, nil
	}
	installedDigest, _ := helperDigest(helperExecutable)
	if installedDigest == digest {
		deadline := time.NewTimer(5 * time.Second)
		tick := time.NewTicker(100 * time.Millisecond)
		defer deadline.Stop()
		defer tick.Stop()
	waitForDaemon:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				break waitForDaemon
			case <-tick.C:
				if connection, err := dialDesktopHelper(digest); err == nil {
					return connection, nil
				}
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// AppleScript receives executable and uid as separate quoted arguments.
	script := `on run arguments
set executablePath to item 1 of arguments
set userID to item 2 of arguments
set expectedDigest to item 3 of arguments
do shell script quoted form of executablePath & " --desktop-helper-install " & quoted form of userID & " " & quoted form of expectedDigest with administrator privileges
end run`
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, executable, strconv.Itoa(os.Getuid()), digest)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
	command.WaitDelay = 2 * time.Second
	if err = command.Run(); err != nil {
		return nil, errors.New("administrator authorization to install the desktop helper was cancelled or failed")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if connection, err := dialDesktopHelper(digest); err == nil {
			return connection, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("installed desktop helper did not start")
		case <-tick.C:
		}
	}
}

func helperLaunchPlist(uid int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--desktop-helper-serve</string><string>%d</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ProcessType</key><string>Background</string>
<key>ExitTimeOut</key><integer>30</integer>
</dict></plist>
`, helperLabel, helperExecutable, uid)
}

func installDesktopHelper(uid int, expectedDigest string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	if err = verifyHelperParent(filepath.Dir(helperExecutable)); err != nil {
		return err
	}
	if err = verifyHelperParent(filepath.Dir(helperPlist)); err != nil {
		return err
	}
	unlock, err := lockHelperInstallation()
	if err != nil {
		return err
	}
	defer unlock()
	files := []helperInstallFile{{path: helperExecutable}, {path: helperPlist}}
	defer cleanupHelperInstallation(files)
	for i := range files {
		file, err := os.CreateTemp(filepath.Dir(files[i].path), ".vk-turn-staged-")
		if err != nil {
			return err
		}
		files[i].staged = file.Name()
		if err = file.Close(); err != nil {
			return err
		}
	}
	if err = copyHelperExecutable(source, files[0].staged, expectedDigest); err != nil {
		return err
	}
	if err = writeHelperFile(files[1].staged, []byte(helperLaunchPlist(uid)), 0644); err != nil {
		return err
	}
	return replaceHelperInstallation(files, stopDesktopHelper, func() error { return exec.Command("/bin/launchctl", "bootstrap", "system", helperPlist).Run() })
}

func verifyHelperParent(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 {
			return errors.New("desktop helper installation path must be root-owned and protected")
		}
		if current == "/" {
			return nil
		}
	}
}

func copyHelperExecutable(source, destination, expectedDigest string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".vk-turn-helper-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err = io.Copy(temporary, file); err == nil {
		err = temporary.Chmod(0755)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if expectedDigest != "" {
		digest, err := helperDigest(name)
		if err != nil {
			return err
		}
		if digest != expectedDigest {
			return errors.New("desktop helper executable changed during installation")
		}
	}
	return os.Rename(name, destination)
}

func writeHelperFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".vk-turn-helper-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

func uninstallDesktopHelper() error {
	unlock, err := lockHelperInstallation()
	if err != nil {
		return err
	}
	defer unlock()
	if err := stopDesktopHelper(); err != nil {
		return err
	}
	return removeHelperInstallation([]string{helperPlist, helperExecutable, helperSocket}, helperDirectory)
}

func removeHelperInstallation(paths []string, directory string) error {
	var err error
	for _, path := range paths {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.Remove(directory); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func stopDesktopHelper() error {
	if err := exec.Command("/bin/launchctl", "print", "system/"+helperLabel).Run(); err != nil {
		return nil
	}
	if err := exec.Command("/bin/launchctl", "bootout", "system/"+helperLabel).Run(); err != nil {
		return errors.New("could not stop desktop helper")
	}
	return nil
}

// Installation and removal share a lock separate from the tunnel session lock.
func lockHelperInstallation() (func(), error) {
	const directory = "/var/db/vk-turn-proxy-desktop"
	if err := secureDarwinDirectory(directory); err != nil {
		return nil, err
	}
	fd, err := unix.Open(directory+"/helper-install.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "helper-install.lock")
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != 0 {
		file.Close()
		return nil, errors.New("invalid desktop helper installation lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = file.Close() }, nil
}

type helperInstallFile struct {
	path, staged, backup string
	hadPrevious          bool
}

func cleanupHelperInstallation(files []helperInstallFile) {
	for _, file := range files {
		if file.staged != "" {
			_ = os.Remove(file.staged)
		}
		if file.backup != "" {
			_ = os.Remove(file.backup)
		}
	}
}

func replaceHelperInstallation(files []helperInstallFile, stop, start func() error) error {
	for i := range files {
		backup, err := os.CreateTemp(filepath.Dir(files[i].path), ".vk-turn-previous-")
		if err != nil {
			return err
		}
		files[i].backup = backup.Name()
		if err = backup.Close(); err != nil {
			return err
		}
		if err = os.Remove(files[i].backup); err != nil {
			return err
		}
		if err = os.Link(files[i].path, files[i].backup); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		} else {
			files[i].hadPrevious = true
		}
	}
	if err := stop(); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.Rename(file.staged, file.path); err != nil {
			return errors.Join(err, restoreHelperInstallation(files, start))
		}
	}
	if err := start(); err != nil {
		if stopErr := stop(); stopErr != nil {
			return errors.Join(err, stopErr)
		}
		return errors.Join(err, restoreHelperInstallation(files, start))
	}
	return nil
}

func restoreHelperInstallation(files []helperInstallFile, start func() error) error {
	var result error
	previous := true
	for _, file := range files {
		if file.hadPrevious {
			result = errors.Join(result, os.Rename(file.backup, file.path))
		} else {
			previous = false
			if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
				result = errors.Join(result, err)
			}
		}
	}
	if result == nil && previous {
		return start()
	}
	return result
}
