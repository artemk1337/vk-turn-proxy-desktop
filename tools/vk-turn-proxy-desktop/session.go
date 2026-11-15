// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type backendSession struct {
	stopFile string
	done     chan struct{}
}

type Logs struct {
	Lines    []string `json:"lines"`
	Revision uint64   `json:"revision"`
}

func (a *App) validateProfile(p Profile) error {
	directory, err := os.MkdirTemp(a.root, "validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	args, err := profileArguments(p, directory)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, a.backend, append([]string{"-validate-profile"}, args...)...)
	prepareBackendCommand(command)
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("Profile validation timed out. Try again.")
		}
		return errors.New("Invalid connection profile. Check its fields.")
	}
	return nil
}

func (a *App) Connect() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return a.snapshot(), errors.New("The application is closing.")
	}
	if a.session != nil {
		return a.snapshot(), nil
	}
	var selected Profile
	for _, p := range a.profiles.Profiles {
		if p.ID == a.profiles.ActiveID {
			selected = p
			break
		}
	}
	if selected.ID == "" {
		return a.snapshot(), errors.New("Add a connection profile first.")
	}
	if err := requireDesktopPrivileges(); err != nil {
		return a.snapshot(), err
	}
	if err := prepareMachineDirectory(); err != nil {
		return a.snapshot(), err
	}
	if _, err := os.Stat(a.backend); err != nil {
		return a.snapshot(), errors.New("The terminal client is missing. Extract the complete package.")
	}
	directory, err := os.MkdirTemp(a.root, "session-")
	if err != nil {
		return a.snapshot(), err
	}
	started := false
	defer func() {
		if !started {
			os.RemoveAll(directory)
		}
	}()
	args, err := profileArguments(selected, directory)
	if err != nil {
		return a.snapshot(), err
	}
	if a.cookies != "" {
		cookiePath := filepath.Join(directory, "cookies.txt")
		if err := writePrivate(cookiePath, []byte(a.cookies)); err != nil {
			return a.snapshot(), err
		}
		args = append(args, "-vk-cookie-file", cookiePath)
	}
	session := &backendSession{stopFile: filepath.Join(directory, "stop"), done: make(chan struct{})}
	args = append(args, "-stop-file", session.stopFile, "-parent-pid", strconv.Itoa(os.Getpid()), "-block-ipv6=true")
	args = append(args, backendArguments(a.root)...)
	reader, writer, err := os.Pipe()
	if err != nil {
		return a.snapshot(), err
	}
	command, finish, err := startPlatformCommand(a.backend, args, writer, session.stopFile)
	if err != nil {
		reader.Close()
		writer.Close()
		return a.snapshot(), errors.New("Could not start the VPN backend. Check administrator authorization.")
	}
	writer.Close()
	started = true
	a.session = session
	a.connection = "credentials"
	a.lastError = ""
	a.logs.Lines = []string{}
	a.logs.Revision++
	a.logBytes = 0
	go a.waitBackend(command, finish, reader, directory, session)
	return a.snapshot(), nil
}

func (a *App) waitBackend(command *exec.Cmd, finish func(), reader *os.File, directory string, session *backendSession) {
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer reader.Close()
		readBackendLogs(reader, a.appendLog)
	}()
	err := command.Wait()
	finish()
	<-readDone
	os.RemoveAll(directory)
	a.mu.Lock()
	a.session = nil
	a.connection = "disconnected"
	if err != nil {
		a.connection = "failed"
		a.lastError = "VPN backend stopped. Check the connection log."
	}
	a.mu.Unlock()
	close(session.done)
}

func (a *App) Disconnect() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil || a.connection == "disconnecting" {
		return a.snapshot(), nil
	}
	if err := writePrivate(a.session.stopFile, nil); err != nil {
		return a.snapshot(), errors.New("Could not request disconnect.")
	}
	a.connection = "disconnecting"
	return a.snapshot(), nil
}

func readBackendLogs(source io.Reader, appendLine func(string)) {
	reader := bufio.NewReaderSize(source, 8192)
	for {
		line, more, err := reader.ReadLine()
		if len(line) > 0 {
			text := strings.ToValidUTF8(string(line), "�")
			if more {
				text += " [truncated]"
			}
			appendLine(text)
		}
		for more && err == nil {
			_, more, err = reader.ReadLine()
		}
		if err != nil {
			return
		}
	}
}

func logPhase(line string) string {
	switch {
	case strings.Contains(line, "DEFAULT ROUTE") && strings.Contains(line, "tunnel"):
		return "connected"
	case strings.Contains(line, "first connection up"), strings.Contains(line, "tunnel:"):
		return "handshake"
	case strings.Contains(line, "TURN relay allocated"):
		return "relay"
	default:
		return ""
	}
}

func connectionRank(state string) int {
	switch state {
	case "credentials":
		return 1
	case "relay":
		return 2
	case "handshake":
		return 3
	case "connected":
		return 4
	default:
		return 0
	}
}

func (a *App) appendLog(line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	phase := logPhase(line)
	if connectionRank(a.connection) > 0 && connectionRank(phase) > connectionRank(a.connection) {
		a.connection = phase
	}
	a.logs.Lines = append(a.logs.Lines, line)
	a.logBytes += len(line) + 1
	a.trimLogs()
	a.logs.Revision++
}

func (a *App) trimLogs() {
	removed := 0
	for removed < len(a.logs.Lines) && (len(a.logs.Lines)-removed > a.preferences.LogLines || a.logBytes > defaults.LogMaxBytes) {
		a.logBytes -= len(a.logs.Lines[removed]) + 1
		a.logs.Lines[removed] = ""
		removed++
	}
	a.logs.Lines = a.logs.Lines[removed:]
}

func (a *App) GetLogs() Logs {
	a.mu.Lock()
	defer a.mu.Unlock()
	lines := append([]string{}, a.logs.Lines...)
	return Logs{Lines: lines, Revision: a.logs.Revision}
}

func (a *App) ClearLogs() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs.Lines = []string{}
	a.logBytes = 0
	a.logs.Revision++
	return a.snapshot(), nil
}
