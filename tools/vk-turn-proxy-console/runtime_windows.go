// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var defaultPidFile = filepath.Join(os.Getenv("ProgramData"), "VKTurnProxy", "client.pid")
var defaultStateFile = filepath.Join(os.Getenv("ProgramData"), "VKTurnProxy", "network-state.json")
var defaultCredCache = filepath.Join(os.Getenv("LOCALAPPDATA"), "VKTurnProxyWindows", "credentials.json")

func prepareConsoleStorage(o *options) error {
	if strings.EqualFold(o.pidFile, defaultPidFile) || strings.EqualFold(o.stateFile, defaultStateFile) {
		if err := protectConsoleDirectory(filepath.Dir(defaultPidFile), "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"); err != nil {
			return err
		}
	}
	if strings.EqualFold(o.credCache, defaultCredCache) {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		return protectConsoleDirectory(filepath.Dir(defaultCredCache), "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+user.User.Sid.String()+")")
	}
	return nil
}

func protectConsoleDirectory(path, descriptor string) error {
	if !filepath.IsAbs(path) {
		return errors.New("Windows application data directory is unavailable")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	security, err := windows.SecurityDescriptorFromString(descriptor)
	if err != nil {
		return err
	}
	dacl, _, err := security.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func requireRoot() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("run as Administrator to create the VPN adapter and configure routes and DNS")
	}
	return nil
}

func ignoreBrokenPipe() {}

func hideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
