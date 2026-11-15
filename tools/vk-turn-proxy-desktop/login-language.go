// SPDX-License-Identifier: MIT
package main

import "strings"

func loginText(language, text string) string {
	if language != "ru" {
		return text
	}
	translations := map[string]string{
		"Sign in to VK": "Вход в VK",
		"Complete VK login. This window closes when the required cookies are ready.": "Войдите в VK. Окно закроется после получения необходимых cookies.",
		"install Microsoft Edge WebView2 Runtime to sign in through VK":              "Для входа в VK установите Microsoft Edge WebView2 Runtime",
		"WebView2 startup timed out":                                                 "Истекло время запуска WebView2",
		"WebView2 requires an updated Runtime":                                       "Обновите Microsoft Edge WebView2 Runtime",
		"WebView2 environment":                                                       "Окружение WebView2",
		"WebView2 controller":                                                        "Контроллер WebView2",
		"WebView2 view":                                                              "Окно WebView2",
		"WebView2 cookie manager":                                                    "Менеджер cookies WebView2",
		"WebView2 navigation":                                                        "Переход в WebView2",
		"WebView2 cookies":                                                           "Cookies WebView2",
		"VK website login could not start. Install python3-gi and gir1.2-webkit2-4.1 or import cookies": "Не удалось запустить вход в VK. Установите python3-gi и gir1.2-webkit2-4.1 или импортируйте cookies",
		"website login is supported on Windows and Linux; import VK cookies":                            "Вход через сайт доступен в Windows и Linux. Импортируйте cookies VK",
	}
	if translated, ok := translations[text]; ok {
		return translated
	}
	prefix, suffix, found := strings.Cut(text, ":")
	if translated, ok := translations[prefix]; found && ok {
		return translated + ":" + suffix
	}
	return text
}
