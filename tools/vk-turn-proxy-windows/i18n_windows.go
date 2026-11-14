// SPDX-License-Identifier: MIT

package main

import "github.com/lxn/walk"

var russianText = map[string]string{
	"Show": "Открыть", "Exit": "Выход",
	"Profiles (*.json;*.txt)|*.json;*.txt|All files (*.*)|*.*": "Профили (*.json;*.txt)|*.json;*.txt|Все файлы (*.*)|*.*",
	"WireGuard (*.conf)|*.conf|All files (*.*)|*.*":            "WireGuard (*.conf)|*.conf|Все файлы (*.*)|*.*",
	"Cookie files (*.txt)|*.txt|All files (*.*)|*.*":           "Файлы cookie (*.txt)|*.txt|Все файлы (*.*)|*.*",
	"Preferences could not be loaded":                          "Не удалось прочитать настройки",
	"WireGuard through VK calls":                               "WireGuard через звонки VK",
	"Profile link":                                             "Ссылка профиля", "WireGuard config": "Конфигурация WireGuard", "Advanced": "Дополнительно", "Log": "Журнал", "Settings": "Настройки",
	"Paste your connection link": "Вставьте ссылку подключения",
	"The link contains a Base64-encoded JSON profile. You can also open a JSON profile.": "Ссылка содержит JSON-профиль в Base64. Также можно открыть JSON-профиль.",
	"Open profile...": "Открыть профиль…", "WireGuard configuration": "Конфигурация WireGuard", "Open .conf...": "Открыть .conf…",
	"Full-tunnel .conf with one peer.": "Конфигурация .conf с одним сервером для всего трафика.", "VK call link": "Ссылка на звонок VK",
	"VK TURN server (optional)": "Сервер VK TURN (необязательно)", "Server transport": "Транспорт сервера",
	"Use Endpoint from WireGuard config":                                                 "Использовать Endpoint из WireGuard-конфига",
	"VK TURN listener, usually port 56000. Leave empty to use Endpoint from the config.": "Адрес VK TURN, обычно порт 56000. Пустое поле использует Endpoint из конфига.",
	"The server address must point to the VK TURN listener, usually port 56000.":         "Укажите адрес VK TURN сервера, обычно с портом 56000.",
	"Native SRTP": "Native SRTP", "Legacy DTLS": "Legacy DTLS",
	"Server name": "Имя сервера",
	"Choose a server by name from a multi-server JSON profile.": "Укажите имя сервера из JSON-профиля с несколькими серверами.",
	"Use the active server": "Использовать активный сервер", "VK login": "Вход в VK",
	"Open a cookies.txt file or paste Netscape cookies or remixsid=...; p=... below.": "Откройте cookies.txt или вставьте ниже cookies Netscape либо remixsid=...; p=...",
	"Pasted cookies take priority over the selected file.":                            "Вставленный текст используется вместо выбранного файла.",
	"Sign in to VK":    "Войти в VK",
	"VK website login": "Вход через сайт VK",
	"Complete VK login. This window closes when the required cookies are ready.":                                        "Войдите в аккаунт VK. Окно закроется после получения cookies.",
	"Could not complete VK website login. Try again or import cookies.":                                                 "Не удалось войти через сайт VK. Повторите вход или импортируйте cookies.",
	"Install Microsoft Edge WebView2 Runtime to sign in through VK.\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703": "Для входа через VK установите Microsoft Edge WebView2 Runtime.\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703",
	"Anonymous access": "Анонимный доступ", "Choose...": "Выбрать…", "Clear": "Очистить", "Connection log": "Журнал подключения",
	"Connect": "Подключить", "Disconnect": "Отключить", "Using profile link": "Подключение по ссылке", "Using WireGuard config": "Подключение по конфигу WireGuard",
	"Disconnected": "Отключено", "Connecting…": "Подключение…", "Connected": "Подключено", "Disconnecting…": "Отключение…", "Connection failed": "Ошибка подключения",
	"Language": "Язык", "Appearance": "Оформление", "Light": "Светлое", "Dark gray": "Тёмно-серое",
	"Start with Windows": "Запускать вместе с Windows", "Startup opens the app. Connect when you are ready.": "Автозапуск открывает приложение. Подключение запускается кнопкой.",
	"Fetching VK access": "Получаем доступ к VK", "Connecting to VK relay": "Соединяемся с ретранслятором VK", "Establishing WireGuard tunnel": "Создаём туннель WireGuard", "Restoring network settings": "Восстанавливаем настройки сети",
	"Open JSON connection profile": "Открыть JSON-профиль подключения", "Open WireGuard configuration": "Открыть конфигурацию WireGuard", "Open VK cookies in Netscape format": "Открыть cookies VK в формате Netscape",
	"The profile is larger than 4 MiB.": "Размер профиля превышает 4 МиБ.", "Paste a connection link or open a profile first.": "Вставьте ссылку подключения или откройте профиль.",
	"Protect local application data": "Защита данных приложения", "Protect shared network state": "Защита журнала сетевых настроек", "Start VPN backend": "Запуск VPN-клиента", "Request disconnect": "Запрос отключения",
	"Preferences could not be saved": "Не удалось сохранить настройки", "Windows startup could not be changed": "Не удалось изменить автозапуск Windows",
	"Administrator access is required to create the VPN tunnel.": "Для создания VPN-туннеля нужен запуск от имени администратора.",
}

func (a *application) tr(text string) string {
	if a.preferences.Language == "ru" {
		if translated, ok := russianText[text]; ok {
			return translated
		}
	}
	return text
}

type textControl interface {
	Text() string
	SetText(string) error
}

type textBinding struct {
	control textControl
	source  string
}
type cueBinding struct {
	control *walk.LineEdit
	source  string
}

func (a *application) collectText(container walk.Container) {
	children := container.Children()
	for i := 0; i < children.Len(); i++ {
		child := children.At(i)
		if control, ok := child.(textControl); ok {
			switch child {
			case a.status, a.modeLabel, a.phaseLabel, a.profile, a.wgConfig, a.log, a.vkLink, a.endpoint, a.server, a.cookies, a.cookieText, a.languageChoice, a.themeChoice, a.transport:
			default:
				if text := control.Text(); text != "" {
					a.textBindings = append(a.textBindings, textBinding{control, text})
				}
			}
		}
		if edit, ok := child.(*walk.LineEdit); ok {
			if cue := edit.CueBanner(); cue != "" {
				a.cueBindings = append(a.cueBindings, cueBinding{edit, cue})
			}
		}
		if label, ok := child.(*walk.Label); ok {
			a.mutedLabels[label] = label.TextColor() == mutedText
		}
		if nested, ok := child.(walk.Container); ok {
			a.collectText(nested)
		}
	}
}

func (a *application) applyLanguage() {
	a.updatingPreferences = true
	defer func() { a.updatingPreferences = false }()
	for _, binding := range a.textBindings {
		binding.control.SetText(a.tr(binding.source))
	}
	for _, binding := range a.cueBindings {
		binding.control.SetCueBanner(a.tr(binding.source))
	}
	titles := []string{"Profile link", "WireGuard config", "Advanced", "Log", "Settings"}
	for i, title := range titles {
		a.tabs.Pages().At(i).SetTitle(a.tr(title))
	}
	selected := a.transport.CurrentIndex()
	a.transport.SetModel([]string{a.tr("Native SRTP"), a.tr("Legacy DTLS")})
	a.transport.SetCurrentIndex(selected)
	a.themeChoice.SetModel([]string{a.tr("Light"), a.tr("Dark gray")})
	if a.preferences.Theme == "dark" {
		a.themeChoice.SetCurrentIndex(1)
	} else {
		a.themeChoice.SetCurrentIndex(0)
	}
	if a.preferences.Language == "ru" {
		a.languageChoice.SetCurrentIndex(1)
	} else {
		a.languageChoice.SetCurrentIndex(0)
	}
	a.endpoint.SetToolTipText(a.tr("VK TURN listener, usually port 56000. Leave empty to use Endpoint from the config."))
	a.updateModeLabel()
	a.updateConnectionState()
	a.tabs.Invalidate()
}

func (a *application) updateModeLabel() {
	text := "Using profile link"
	if a.inputMode == 1 {
		text = "Using WireGuard config"
	}
	a.modeLabel.SetText(a.tr(text))
}
