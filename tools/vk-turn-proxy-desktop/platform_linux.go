//go:build linux

// SPDX-License-Identifier: MIT
package main

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed vk-login.py
var linuxLoginScript string

func platformCommand(backend string, args []string) *exec.Cmd {
	if os.Geteuid() != 0 {
		return exec.Command("pkexec", append([]string{backend}, args...)...)
	}
	return exec.Command(backend, args...)
}

func autostartPath() (string, error) {
	root, err := defaultDataDir()
	return filepath.Join(filepath.Dir(root), "autostart", "vk-turn-proxy-desktop.desktop"), err
}

func desktopExecutable(executable string) string {
	value := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$", "%", "%%").Replace(executable)
	return "\"" + value + "\""
}

func autostartEnabled(executable string) (bool, error) {
	if launcher := os.Getenv("VKTURN_DESKTOP_LAUNCHER"); launcher != "" {
		executable = launcher
	}
	path, err := autostartPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expected := "Exec=" + desktopExecutable(executable) + " --autostart"
	for _, line := range strings.Split(string(data), "\n") {
		if line == expected {
			return true, nil
		}
	}
	return false, nil
}

func setAutostart(enabled bool, executable string) error {
	if launcher := os.Getenv("VKTURN_DESKTOP_LAUNCHER"); launcher != "" {
		executable = launcher
	}
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if strings.ContainsAny(executable, "\r\n") {
		return errors.New("invalid executable path")
	}
	text := "[Desktop Entry]\nType=Application\nName=VK TURN Proxy\nExec=" + desktopExecutable(executable) + " --autostart\nTryExec=" + executable + "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
	return writePrivate(path, []byte(text))
}

func loginVK(ctx context.Context, language string) (string, error) {
	root, err := defaultDataDir()
	if err != nil {
		return "", err
	}
	if err := secureDirectory(root); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(root, "vk-login-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	output := filepath.Join(directory, "cookies.txt")
	command := exec.CommandContext(ctx, "python3", "-c", linuxLoginScript, output, language)
	if err := command.Run(); err != nil {
		return "", errors.New(loginText(language, "VK website login could not start. Install python3-gi and gir1.2-webkit2-4.1 or import cookies"))
	}
	data, err := readSecret(output, 4<<20)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}
