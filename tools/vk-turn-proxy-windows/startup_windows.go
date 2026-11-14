// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func setAutostart(enabled bool, executable string) error {
	sid, err := startupUserSID()
	if err != nil {
		return err
	}
	operation := "disable"
	if enabled {
		operation = "enable"
	}
	_, err = runStartupTask(executable, sid, startupTaskPrefix+sid, operation)
	return err
}

func autostartEnabled(executable string) (bool, error) {
	sid, err := startupUserSID()
	if err != nil {
		return false, err
	}
	out, err := runStartupTask(executable, sid, startupTaskPrefix+sid, "query")
	return strings.TrimSpace(out) == "true", err
}

func startupUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func runStartupTask(executable, sid, taskName, operation string) (string, error) {
	script := startupTaskScript(executable, sid, taskName, operation)
	units := utf16.Encode([]rune(script))
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("Windows autostart: %w: %s", err, strings.TrimSpace(stderr.String()+"\n"+string(output)))
	}
	return string(output), nil
}
