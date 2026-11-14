// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	wgprofile "github.com/artemk1337/vk-turn-proxy-desktop/pkg/wireguard-profile"
	"golang.org/x/term"
)

const wizardInputLimit = 4 << 20

type setupWizard struct {
	input    io.Reader
	reader   *bufio.Reader
	output   io.Writer
	language string
}

func autoSetupWizard(args []string, configPath string) bool {
	if len(args) != 0 || !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	_, err := os.Stat(configPath)
	return errors.Is(err, os.ErrNotExist)
}

// The wizard keeps imported keys in memory and reuses the normal connection path.
func runSetupWizard(input io.Reader, output io.Writer, o *options) (*backupFile, error) {
	w := setupWizard{input: input, reader: bufio.NewReader(input), output: output, language: o.language}
	if w.language == "" {
		fmt.Fprintln(output, "VK TURN Proxy\n\nLanguage / Язык\n1. English\n2. Русский")
		choice, err := w.choice("[1]: ", 1, 2)
		if err != nil {
			return nil, err
		}
		w.language = "en"
		if choice == 2 {
			w.language = "ru"
		}
	}
	fmt.Fprintln(output, w.text("\nConnection setup\n1. Profile link or JSON file\n2. WireGuard configuration", "\nНастройка подключения\n1. Ссылка профиля или JSON-файл\n2. Конфигурация WireGuard"))
	mode, err := w.choice(w.text("Choose [1]: ", "Выберите [1]: "), 1, 2)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if mode == 1 {
		raw, err = w.profile()
	} else {
		raw, err = w.wireGuard()
	}
	if err != nil {
		return nil, err
	}
	profile, err := parseProfile(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", w.text("Profile", "Профиль"), err)
	}
	if o.list {
		return profile, nil
	}
	if err := w.selectServer(profile, o); err != nil {
		return nil, err
	}
	server, err := profile.selectedServer(o.serverName)
	if err != nil {
		return nil, err
	}
	settings := buildSettings(profile, server)
	if _, err := o.applyTo(&settings); err != nil {
		return nil, err
	}
	if err := settings.validate(); err != nil {
		return nil, err
	}
	if o.cookieFile == "" {
		o.cookieFile, err = w.line(w.text("VK cookies.txt file [anonymous access]: ", "Файл VK cookies.txt [анонимный доступ]: "), false)
		if err != nil {
			return nil, err
		}
		if settings.VKAuth && o.cookieFile == "" {
			return nil, errors.New(w.text("This profile requires a VK cookies.txt file", "Для этого профиля нужен файл VK cookies.txt"))
		}
	}
	if !o.set["block-ipv6"] {
		o.blockIPv6 = true
	}
	fmt.Fprintf(output, w.text("\nServer %q\nAddress %s\nTransport %s\n", "\nСервер %q\nАдрес %s\nТранспорт %s\n"), server.Name, settings.PeerAddress, server.mode())
	if _, err := w.line(w.text("Press Enter to connect. Ctrl+C cancels or disconnects. ", "Нажмите Enter для подключения. Ctrl+C отменяет или отключает соединение. "), false); err != nil {
		return nil, err
	}
	return profile, nil
}

func (w *setupWizard) text(en, ru string) string {
	if w.language == "ru" {
		return ru
	}
	return en
}

func (w *setupWizard) choice(prompt string, fallback, maximum int) (int, error) {
	for {
		value, err := w.line(prompt, false)
		if err != nil {
			return 0, err
		}
		if value == "" {
			return fallback, nil
		}
		choice, err := strconv.Atoi(value)
		if err == nil && choice >= 1 && choice <= maximum {
			return choice, nil
		}
		fmt.Fprintln(w.output, w.text("Enter a number from the menu.", "Введите номер из меню."))
	}
}

func (w *setupWizard) line(prompt string, secret bool) (string, error) {
	fmt.Fprint(w.output, prompt)
	if file, ok := w.input.(*os.File); secret && ok && term.IsTerminal(int(file.Fd())) && w.reader.Buffered() == 0 {
		data, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(w.output)
		if len(data) > wizardInputLimit {
			return "", errors.New(w.text("Input exceeds 4 MiB", "Ввод превышает 4 МиБ"))
		}
		return strings.TrimSpace(string(data)), err
	}
	var data []byte
	for {
		chunk, err := w.reader.ReadSlice('\n')
		if len(data)+len(chunk) > wizardInputLimit {
			return "", errors.New(w.text("Input exceeds 4 MiB", "Ввод превышает 4 МиБ"))
		}
		data = append(data, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !(errors.Is(err, io.EOF) && len(data) > 0) {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
}

func (w *setupWizard) profile() ([]byte, error) {
	value, err := w.line(w.text("Profile link, JSON, or file path (input hidden): ", "Ссылка профиля, JSON или путь к файлу (ввод скрыт): "), true)
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, errors.New(w.text("Enter a profile link or file path", "Введите ссылку профиля или путь к файлу"))
	}
	if strings.HasPrefix(value, "{") && !json.Valid([]byte(value)) {
		fmt.Fprintln(w.output, w.text("Finish the JSON, then enter .end on a new line.", "Завершите JSON, затем введите .end на новой строке."))
		return w.block(value)
	}
	if strings.HasPrefix(strings.ToLower(value), "vkturnproxy:") || strings.HasPrefix(value, "{") {
		return []byte(value), nil
	}
	if decoded, err := decodeConnectionBase64(value); err == nil && json.Valid(decoded) {
		return []byte(value), nil
	}
	return w.readFile(value)
}

func (w *setupWizard) wireGuard() ([]byte, error) {
	path, err := w.line(w.text("WireGuard .conf path [Enter to paste]: ", "Путь к WireGuard .conf [Enter для вставки]: "), false)
	if err != nil {
		return nil, err
	}
	var config []byte
	if path != "" {
		config, err = w.readFile(path)
	} else {
		fmt.Fprintln(w.output, w.text("Paste the configuration, then enter .end on a new line.", "Вставьте конфигурацию, затем введите .end на новой строке."))
		config, err = w.block("")
	}
	if err != nil {
		return nil, err
	}
	link, err := w.line(w.text("VK call link (input hidden): ", "Ссылка на VK-звонок (ввод скрыт): "), true)
	if err != nil {
		return nil, err
	}
	peer, err := w.line(w.text("VK TURN server host:port [Endpoint from config]: ", "VK TURN сервер host:port [Endpoint из конфига]: "), false)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(w.output, w.text("1. Native SRTP\n2. Legacy DTLS", "1. Native SRTP\n2. Legacy DTLS"))
	transport, err := w.choice(w.text("Transport [1]: ", "Транспорт [1]: "), 1, 2)
	if err != nil {
		return nil, err
	}
	return wgprofile.Build(wgprofile.Options{Config: string(config), VKLink: link, PeerAddress: peer, UseSrtp: transport == 1})
}

func (w *setupWizard) block(first string) ([]byte, error) {
	var text strings.Builder
	if first != "" {
		text.WriteString(first)
		text.WriteByte('\n')
	}
	for {
		line, err := w.line("", false)
		if err != nil {
			return nil, err
		}
		if line == ".end" {
			return []byte(text.String()), nil
		}
		if text.Len()+len(line)+1 > wizardInputLimit {
			return nil, errors.New(w.text("Configuration exceeds 4 MiB", "Конфигурация превышает 4 МиБ"))
		}
		text.WriteString(line)
		text.WriteByte('\n')
	}
}

func (w *setupWizard) readFile(path string) ([]byte, error) {
	file, err := os.Open(strings.Trim(path, "\""))
	if err != nil {
		return nil, errors.New(w.text("Unable to open the configuration file", "Не удалось открыть файл конфигурации"))
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, wizardInputLimit+1))
	if err != nil {
		return nil, errors.New(w.text("Unable to read the configuration file", "Не удалось прочитать файл конфигурации"))
	}
	if len(data) > wizardInputLimit {
		return nil, errors.New(w.text("Configuration exceeds 4 MiB", "Конфигурация превышает 4 МиБ"))
	}
	return data, nil
}

func (w *setupWizard) selectServer(profile *backupFile, o *options) error {
	servers := profile.servers()
	if len(servers) <= 1 || o.serverName != "" {
		return nil
	}
	fmt.Fprintln(w.output, w.text("\nServers in profile", "\nСерверы в профиле"))
	fallback := 1
	selected, selectionErr := profile.selectedServer("")
	for i, server := range servers {
		fmt.Fprintf(w.output, "%d. %q (%s)\n", i+1, server.Name, server.mode())
		if selectionErr == nil && server.Name == selected.Name {
			fallback = i + 1
		}
	}
	choice, err := w.choice(fmt.Sprintf(w.text("Server [%d]: ", "Сервер [%d]: "), fallback), fallback, len(servers))
	if err != nil {
		return err
	}
	o.serverName = servers[choice-1].Name
	return nil
}
