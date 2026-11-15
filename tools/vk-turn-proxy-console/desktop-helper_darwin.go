//go:build darwin

// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	routingpolicy "github.com/artemk1337/vk-turn-proxy-desktop/pkg/routing-policy"
	"golang.org/x/sys/unix"
)

const helperDirectory = "/var/run/vk-turn-proxy-desktop-helper"
const helperSocket = helperDirectory + "/control.sock"
const helperExecutable = "/Library/PrivilegedHelperTools/io.github.artemk1337.vkturnproxy.helper"
const helperPlist = "/Library/LaunchDaemons/io.github.artemk1337.vkturnproxy.helper.plist"
const helperLabel = "io.github.artemk1337.vkturnproxy.helper"
const helperRequestLimit = 64 * 1024 * 1024

type helperRequest struct {
	Args      []string `json:"args"`
	Profile   []byte   `json:"profile"`
	Link      []byte   `json:"link,omitempty"`
	Cookies   []byte   `json:"cookies,omitempty"`
	Routing   []byte   `json:"routing,omitempty"`
	WireGuard bool     `json:"wireguard"`
}

type helperFrame struct {
	Digest string `json:"digest,omitempty"`
	Log    []byte `json:"log,omitempty"`
	Exit   *int   `json:"exit,omitempty"`
	Error  string `json:"error,omitempty"`
}

func platformDesktopHelper(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	var err error
	switch args[0] {
	case "--desktop-helper-client":
		err = runHelperClient(args[1:])
	case "--desktop-helper-install", "--desktop-helper-serve":
		if os.Geteuid() != 0 {
			err = errors.New("desktop helper requires administrator privileges")
			break
		}
		var uid int
		if len(args) < 2 || len(args) > 3 || (args[0] == "--desktop-helper-serve" && len(args) != 2) {
			err = errors.New("desktop helper requires a user ID")
			break
		}
		uid, err = strconv.Atoi(args[1])
		if err != nil || uid <= 0 || uint64(uid) > 1<<32-1 {
			err = errors.New("invalid desktop helper user ID")
			break
		}
		if args[0] == "--desktop-helper-install" {
			expected := ""
			if len(args) == 3 {
				expected = args[2]
			}
			err = installDesktopHelper(uid, expected)
		} else {
			err = serveDesktopHelper(uint32(uid))
		}
	case "--desktop-helper-uninstall":
		if os.Geteuid() != 0 {
			err = errors.New("desktop helper requires administrator privileges")
		} else {
			err = uninstallDesktopHelper()
		}
	default:
		return false, 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "desktop helper:", err)
		return true, 1
	}
	return true, 0
}

func helperPeerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *unix.Xucred
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		credentials, peerErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return 0, err
	}
	if peerErr != nil {
		return 0, peerErr
	}
	return credentials.Uid, nil
}

func helperRequestFromOptions(o *options) (helperRequest, error) {
	allowed := map[string]bool{"config": true, "wg-config": true, "vk-link-file": true, "vk-cookie-file": true, "server": true, "server-transport": true, "turn-transport": true, "conns": true, "mtu": true, "peer": true, "block-ipv6": true, "stop-file": true, "parent-pid": true, "routing-file": true, "desktop-traffic": true}
	for name := range o.set {
		if !allowed[name] {
			return helperRequest{}, fmt.Errorf("-%s is unavailable through the desktop helper", name)
		}
	}
	request := helperRequest{WireGuard: o.wgConfigPath != ""}
	path := o.configPath
	if request.WireGuard {
		path = o.wgConfigPath
	}
	var err error
	request.Profile, err = readHelperInput(path)
	if err != nil {
		return helperRequest{}, err
	}
	if o.vkLinkFile != "" {
		request.Link, err = readHelperInput(o.vkLinkFile)
		if err != nil {
			return helperRequest{}, err
		}
	}
	if o.cookieFile != "" {
		request.Cookies, err = readHelperInput(o.cookieFile)
		if err != nil {
			return helperRequest{}, err
		}
	}
	if o.routingFile != "" {
		request.Routing, err = readRoutingPolicy(o.routingFile)
		if err != nil {
			return helperRequest{}, err
		}
	}
	for _, pair := range []struct{ name, value string }{{"server", o.serverName}, {"server-transport", o.serverTransport}, {"peer", o.peer}, {"turn-transport", o.turnTransport}, {"conns", strconv.Itoa(o.conns)}, {"mtu", strconv.Itoa(o.mtu)}} {
		if o.set[pair.name] {
			request.Args = append(request.Args, "-"+pair.name, pair.value)
		}
	}
	request.Args = append(request.Args, "-block-ipv6="+strconv.FormatBool(o.blockIPv6))
	if o.desktopTraffic {
		request.Args = append(request.Args, "-desktop-traffic=true")
	}
	return request, nil
}

func validateHelperRequest(request helperRequest) error {
	if len(request.Routing) > routingpolicy.MaxPolicyBytes {
		return errors.New("invalid desktop helper routing size")
	}
	if len(request.Routing) > 0 {
		if _, err := decodeRoutingPolicy(request.Routing); err != nil {
			return err
		}
	}
	if len(request.Profile) == 0 || len(request.Profile) > wizardInputLimit || len(request.Link) > wizardInputLimit || len(request.Cookies) > wizardInputLimit {
		return errors.New("invalid desktop helper input size")
	}
	if len(request.Args) > 14 {
		return errors.New("too many desktop helper arguments")
	}
	for i := 0; i < len(request.Args); i++ {
		switch request.Args[i] {
		case "-block-ipv6=true", "-block-ipv6=false", "-desktop-traffic=true":
		case "-mtu":
			i++
			if i >= len(request.Args) {
				return errors.New("missing MTU")
			}
			mtu, err := strconv.Atoi(request.Args[i])
			if err != nil || mtu < mtuMinimum || mtu > mtuMaximum {
				return errors.New("invalid MTU")
			}
		case "-conns":
			i++
			if i >= len(request.Args) {
				return errors.New("missing connection count")
			}
			count, err := strconv.Atoi(request.Args[i])
			if err != nil || count < 1 || count > maxConns {
				return errors.New("invalid connection count")
			}
		case "-turn-transport":
			i++
			if i >= len(request.Args) || (request.Args[i] != "tcp" && request.Args[i] != "udp") {
				return errors.New("invalid TURN transport")
			}
		case "-server", "-server-transport", "-peer":
			i++
			if i >= len(request.Args) || len(request.Args[i]) > 4096 {
				return errors.New("invalid desktop helper argument")
			}
		default:
			return errors.New("unsupported desktop helper argument")
		}
	}
	return nil
}

func runHelperClient(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	request, err := helperRequestFromOptions(o)
	if err != nil {
		return err
	}
	if err = validateHelperRequest(request); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = watchParent(ctx, o.parentPID, cancel); err != nil {
		return err
	}
	if o.stopFile != "" {
		go watchStopFile(ctx, o.stopFile, cancel, func(string, ...any) {})
	}
	parent := os.NewFile(3, "desktop-parent")
	defer parent.Close()
	go func() { var b [1]byte; _, _ = parent.Read(b[:]); cancel() }()
	connection, err := connectDesktopHelper(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer connection.Close()
	go func() { <-ctx.Done(); _ = connection.CloseWrite() }()
	if err = json.NewEncoder(connection).Encode(request); err != nil {
		return err
	}
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 8192), 128*1024)
	for scanner.Scan() {
		var frame helperFrame
		if err = json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		if frame.Error != "" {
			return errors.New(frame.Error)
		}
		if len(frame.Log) > 0 {
			_, _ = os.Stdout.Write(frame.Log)
		}
		if frame.Exit != nil {
			if *frame.Exit < 0 && ctx.Err() != nil {
				return nil
			}
			if *frame.Exit != 0 {
				return fmt.Errorf("VPN backend exited with status %d", *frame.Exit)
			}
			return nil
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return errors.New("desktop helper connection closed")
}

func serveDesktopHelper(uid uint32) error {
	if err := secureDarwinDirectory(helperDirectory); err != nil {
		return err
	}
	if err := os.Chmod(helperDirectory, 0711); err != nil {
		return err
	}
	_ = os.Remove(helperSocket)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: helperSocket, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chown(helperSocket, int(uid), -1); err != nil {
		return err
	}
	if err = os.Chmod(helperSocket, 0600); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	var handlers sync.WaitGroup
	defer func() { stop(); listener.Close(); handlers.Wait() }()
	go func() { <-ctx.Done(); listener.Close() }()
	busy := make(chan struct{}, 1)
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		handlers.Add(1)
		go func() { defer handlers.Done(); handleHelperConnection(ctx, connection, uid, busy) }()
	}
}

func handleHelperConnection(ctx context.Context, connection *net.UnixConn, uid uint32, busy chan struct{}) {
	defer connection.Close()
	peer, err := helperPeerUID(connection)
	if err != nil || peer != uid {
		return
	}
	encoder := json.NewEncoder(connection)
	executable, err := os.Executable()
	if err != nil {
		return
	}
	digest, err := helperDigest(executable)
	if err != nil {
		return
	}
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if encoder.Encode(helperFrame{Digest: digest}) != nil {
		return
	}
	_ = connection.SetReadDeadline(time.Now().Add(15 * time.Second))
	var request helperRequest
	decoder := json.NewDecoder(io.LimitReader(connection, helperRequestLimit))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return
	}
	if err = validateHelperRequest(request); err != nil {
		_ = encoder.Encode(helperFrame{Error: err.Error()})
		return
	}
	select {
	case busy <- struct{}{}:
		defer func() { <-busy }()
	default:
		_ = encoder.Encode(helperFrame{Error: "another desktop connection is active"})
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	_ = connection.SetWriteDeadline(time.Time{})
	if err = runHelperConsole(ctx, connection, encoder, request); err != nil {
		_ = encoder.Encode(helperFrame{Error: "could not run the VPN backend"})
	}
}

// Every child receives private data snapshots and fixed machine-wide state paths.
func runHelperConsole(ctx context.Context, connection *net.UnixConn, encoder *json.Encoder, request helperRequest) error {
	if err := secureDarwinDirectory("/var/db/vk-turn-proxy-desktop"); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("/var/db/vk-turn-proxy-desktop", "helper-session-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	args := append([]string{}, request.Args...)
	args = append(args, "-parent-pid", strconv.Itoa(os.Getpid()))
	for _, file := range []struct {
		name, flag string
		data       []byte
	}{{"profile", "-config", request.Profile}, {"link", "-vk-link-file", request.Link}, {"cookies", "-vk-cookie-file", request.Cookies}, {"routing", "-routing-file", request.Routing}} {
		if len(file.data) == 0 {
			continue
		}
		flag := file.flag
		if file.name == "profile" && request.WireGuard {
			flag = "-wg-config"
		}
		path := directory + "/" + file.name
		if err = os.WriteFile(path, file.data, 0600); err != nil {
			return err
		}
		args = append(args, flag, path)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, args...)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=/var/root", "LANG=en_US.UTF-8"}
	writer := &helperLogWriter{connection: connection, encoder: encoder}
	command.Stdout, command.Stderr = writer, writer
	if err = command.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go watchHelperConsole(ctx, connection, command, done)
	err = command.Wait()
	close(done)
	code := 0
	if err != nil {
		code = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return encoder.Encode(helperFrame{Exit: &code})
}

type helperLogWriter struct {
	mu         sync.Mutex
	connection *net.UnixConn
	encoder    *json.Encoder
}

func (writer *helperLogWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	written := 0
	for len(data) > 0 {
		size := min(len(data), 8192)
		_ = writer.connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := writer.encoder.Encode(helperFrame{Log: data[:size]}); err != nil {
			writer.connection.Close()
			return written, err
		}
		written += size
		data = data[size:]
	}
	return written, nil
}

func watchHelperConsole(ctx context.Context, connection *net.UnixConn, command *exec.Cmd, done <-chan struct{}) {
	disconnected := make(chan struct{})
	go func() { var data [1]byte; _, _ = connection.Read(data[:]); close(disconnected) }()
	select {
	case <-done:
		return
	case <-ctx.Done():
	case <-disconnected:
	}
	_ = command.Process.Signal(syscall.SIGTERM)
}

func readHelperInput(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("could not open desktop input")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("desktop input must be a regular file owned by the current user")
	}
	data, err := io.ReadAll(io.LimitReader(file, wizardInputLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > wizardInputLimit {
		return nil, errors.New("desktop input exceeds 4 MiB")
	}
	return data, nil
}
