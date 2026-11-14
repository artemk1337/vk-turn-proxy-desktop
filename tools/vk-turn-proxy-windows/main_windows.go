// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const maxProfileSize = 4 << 20

type application struct {
	window                                                   *walk.MainWindow
	tray                                                     *trayController
	tabs                                                     *walk.TabWidget
	profileChoice                                            *walk.ComboBox
	addProfileButton, editProfileButton, removeProfileButton *walk.PushButton
	authStatus                                               *walk.Label
	authButton, authDetailsButton, themeToggle               *walk.PushButton
	vkAuth                                                   vkAuthState
	vkCookieText, vkCookieFile                               string
	profiles                                                 profileStore
	profilesPath                                             string
	updatingProfiles                                         bool
	logLimitChoice                                           *walk.NumberEdit
	connect, disconnect                                      *walk.PushButton
	status, modeLabel                                        *walk.Label
	log                                                      *walk.TextEdit
	command                                                  *exec.Cmd
	stopFile                                                 string
	closing                                                  bool
	logView                                                  logView
	preferences                                              preferences
	preferencesPath                                          string
	preferencesError                                         error
	updatingPreferences                                      bool
	languageChoice                                           *walk.ComboBox
	startupChoice                                            *walk.CheckBox
	tooltipBindings                                          []tooltipBinding
	textBindings                                             []textBinding
	cueBindings                                              []cueBinding
	mutedLabels                                              map[*walk.Label]bool
	palette                                                  themePalette
	themeBrushes                                             []*walk.SolidColorBrush
	nativeInputBrush                                         win.HBRUSH
	nativeTabs                                               win.HWND
	progress                                                 *walk.CustomWidget
	phaseLabel                                               *walk.Label
	connectionState                                          string
	progressFrame                                            int
}

func main() {
	runtime.LockOSThread()
	if !windows.GetCurrentProcessToken().IsElevated() {
		executable, err := os.Executable()
		if err == nil {
			verb := windows.StringToUTF16Ptr("runas")
			err = windows.ShellExecute(0, verb, windows.StringToUTF16Ptr(executable), nil, nil, windows.SW_SHOWNORMAL)
		}
		if err != nil {
			walk.MsgBox(nil, "VK TURN Proxy", "Administrator access is required to create the VPN tunnel.\n\n"+err.Error(), walk.MsgBoxIconError)
		}
		return
	}
	a := &application{}
	if err := a.preparePreferences(); err != nil {
		walk.MsgBox(nil, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
		return
	}
	err := a.createWindow()
	if err != nil {
		walk.MsgBox(nil, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
		return
	}
	tray, err := newTrayController(a)
	if err != nil {
		a.fail(err)
	} else {
		a.tray = tray
	}
	a.window.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if a.tray != nil && a.tray.handleClose(canceled) {
			return
		}
		if a.command != nil {
			*canceled = true
			a.closing = true
			a.stop()
		}
	})
	a.animateProgress()
	a.window.Run()
	for _, brush := range a.themeBrushes {
		brush.Dispose()
	}
	win.DeleteObject(win.HGDIOBJ(a.nativeInputBrush))
}

func (a *application) fail(err error) {
	walk.MsgBox(a.window, "VK TURN Proxy", err.Error(), walk.MsgBoxIconError)
}

func (a *application) openConfigFile(target *walk.TextEdit, title, filter string) {
	dialog := walk.FileDialog{Title: title, Filter: a.tr(filter)}
	ok, err := dialog.ShowOpen(a.window)
	if err != nil {
		a.fail(err)
		return
	}
	if !ok {
		return
	}
	f, err := os.Open(dialog.FilePath)
	if err != nil {
		a.fail(err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxProfileSize+1))
	if err != nil {
		a.fail(err)
		return
	}
	if len(data) > maxProfileSize {
		a.fail(errors.New(a.tr("The profile is larger than 4 MiB.")))
		return
	}
	target.SetText(string(data))
}

func (a *application) openCookies() {
	dialog := walk.FileDialog{Title: a.tr("Open VK cookies in Netscape format"), Filter: a.tr("Cookie files (*.txt)|*.txt|All files (*.*)|*.*")}
	ok, err := dialog.ShowOpen(a.window)
	if err != nil {
		a.fail(err)
		return
	}
	if ok {
		a.vkCookieText = ""
		a.vkCookieFile = dialog.FilePath
		a.saveVKAuth()
		a.checkVKAuth()
	}
}

func (a *application) setCookieText(text, source string) {
	a.vkCookieFile = ""
	a.vkCookieText = text
	a.saveVKAuth()
	a.checkVKAuth()
}

func (a *application) start() {
	if a.command != nil {
		return
	}
	a.checkVKAuth()
	profile, err := a.connectionProfile()
	if err != nil {
		a.fail(err)
		return
	}

	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		a.fail(err)
		return
	}
	root = filepath.Join(root, "VKTurnProxyWindows")
	if err := secureDirectory(root); err != nil {
		a.fail(fmt.Errorf("%s: %w", a.tr("Protect local application data"), err))
		return
	}
	machineRoot, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		a.fail(err)
		return
	}
	machineRoot = filepath.Join(machineRoot, "VKTurnProxy")
	if err := protectDirectory(machineRoot, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"); err != nil {
		a.fail(fmt.Errorf("%s: %w", a.tr("Protect shared network state"), err))
		return
	}
	session, err := os.MkdirTemp(root, "session-")
	if err != nil {
		a.fail(err)
		return
	}
	config := filepath.Join(session, "profile.txt")
	if err := os.WriteFile(config, profile, 0600); err != nil {
		os.RemoveAll(session)
		a.fail(err)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		os.RemoveAll(session)
		a.fail(err)
		return
	}
	a.stopFile = filepath.Join(session, "stop")
	args := []string{
		"-config", config,
		"-stop-file", a.stopFile,
		"-parent-pid", strconv.Itoa(os.Getpid()),
		"-state-file", filepath.Join(machineRoot, "network-state.json"),
		"-cred-cache", filepath.Join(root, "credentials.json"),
		"-log", filepath.Join(root, "client.log"),
		"-block-ipv6=true",
	}
	selected, _ := a.selectedProfile()
	if name := strings.TrimSpace(selected.ServerName); selected.Mode == "link" && name != "" {
		args = append(args, "-server", name)
	}
	cookiePath := ""
	if text := strings.TrimSpace(a.currentVKCookies()); text != "" {
		cookiePath = filepath.Join(session, "cookies.txt")
		if err := os.WriteFile(cookiePath, []byte(text), 0600); err != nil {
			os.RemoveAll(session)
			a.fail(err)
			return
		}
	}
	if cookiePath != "" {
		args = append(args, "-vk-cookie-file", cookiePath)
	}
	command := exec.Command(filepath.Join(filepath.Dir(executable), "vk-turn-proxy-console.exe"), args...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	reader, writer, err := os.Pipe()
	if err != nil {
		os.RemoveAll(session)
		a.fail(err)
		return
	}
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		os.RemoveAll(session)
		a.fail(fmt.Errorf("%s: %w", a.tr("Start VPN backend"), err))
		return
	}
	writer.Close()
	a.command = command
	a.setRunning(true)
	a.setConnectionState("credentials")
	a.logView.Reset(a.log)
	logs := newLogDispatcher(a.window.Synchronize, func(batch logBatch) {
		text := batch.text
		if batch.dropped > 0 {
			notice := fmt.Sprintf("log: skipped %d older entries while the interface was busy", batch.dropped)
			text = notice + "\n" + text
		}
		a.logView.Append(a.log, text)
		if batch.phase != "" {
			a.progressFromLog(batch.phase)
		}
	})
	disposeHandler := a.window.Disposing().Attach(logs.close)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer reader.Close()
		readBackendLogs(reader, logs.add)
	}()
	go func() {
		err := command.Wait()
		<-readDone
		os.RemoveAll(session)
		logs.finish(func() {
			a.window.Disposing().Detach(disposeHandler)
			a.command = nil
			a.stopFile = ""
			a.setRunning(false)
			a.setConnectionState("disconnected")
			if err != nil {
				a.appendLog("VPN backend stopped: " + err.Error())
				a.setConnectionState("failed")
			}
			if a.closing {
				a.window.Close()
			}
		})
	}()
}

func (a *application) setRunning(running bool) {
	a.profileChoice.SetEnabled(!running)
	a.addProfileButton.SetEnabled(!running)
	_, selected := a.selectedProfile()
	a.editProfileButton.SetEnabled(!running && selected)
	a.removeProfileButton.SetEnabled(!running && selected)
	a.authDetailsButton.SetEnabled(!running)
	a.authButton.SetEnabled(!running)
	a.connect.SetEnabled(!running)
	a.disconnect.SetEnabled(running)
}

func (a *application) stop() {
	if a.command == nil {
		return
	}
	if err := os.WriteFile(a.stopFile, nil, 0600); err != nil {
		a.closing = false
		a.fail(fmt.Errorf("%s: %w", a.tr("Request disconnect"), err))
		return
	}
	a.disconnect.SetEnabled(false)
	a.setConnectionState("disconnecting")
}

func (a *application) appendLog(line string) {
	a.logView.Append(a.log, line)
	a.progressFromLog(line)
}

// Go's file modes do not restrict Windows ACLs. Protect the directory before
// writing profile keys, the credential cache, or logs into it.
func secureDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return protectDirectory(path, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+user.User.Sid.String()+")")
}

func protectDirectory(path, sddl string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
