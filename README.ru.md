# VK TURN Proxy Desktop

Настольный VPN-клиент, который передаёт трафик WireGuard через TURN-серверы VK. Доступен для Windows и Linux. Поддерживает ссылки профилей, WireGuard-конфиги, вход в VK, управление из трея и автозапуск. Есть русский и английский языки, светлая и тёмная темы.

[English](README.md)

## Установка

Скачайте пакет для **amd64 (x64)** или **arm64** из [Releases](https://github.com/artemk1337/vk-turn-proxy-desktop/releases/latest).

### Windows

- **Установщик** - запустите `*-setup.exe`. Приложение и мастер настройки в терминале появятся в меню «Пуск».
- **Portable** - распакуйте ZIP и запустите `vk-turn-proxy-windows.exe`. Оставьте оба `.exe` и `wintun.dll` в одной папке.

Подтвердите запрос администратора для управления VPN-туннелем.

### Linux

Установите `.deb` в Debian или Ubuntu и откройте **VK TURN Proxy** из меню приложений

```sh
sudo apt install ./vk-turn-proxy-linux-v1.0.2-amd64.deb
```

Для portable-версии распакуйте `.tar.gz` и запустите `./vk-turn-proxy-desktop`. Нужны Python 3, GTK 3, PyGObject, iproute2 и polkit. При подключении приложение запросит права администратора.

## Подключение

1. Вставьте `vkturnproxy://import?data=…` на вкладке **Ссылка профиля** или откройте JSON-профиль. Можно вставить сам JSON или Base64.
2. Для ручной настройки откройте **Конфигурация WireGuard**, загрузите `.conf` и укажите приглашение в VK-звонок. Введите адрес TURN-сервера, обычно `host:56000`, или оставьте поле пустым для использования `Endpoint`. Выберите SRTP или DTLS в соответствии с сервером.
3. Нажмите **Подключить**. Кнопка **Отключить** завершает соединение.

Ручной режим требует один peer, IPv4 и `AllowedIPs = 0.0.0.0/0`. Для SRTP сервер запускают с флагом `-srtp`.

Для доступа через учётную запись VK откройте **Дополнительно**. Нажмите **Войти в VK**, загрузите Netscape-файл `cookies.txt` или вставьте `remixsid=…; p=…`. Для входа через сайт нужен [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) в Windows или WebKitGTK (`gir1.2-webkit2-4.1`) в Linux.

Язык, тема и автозапуск доступны в **Настройках**. Закрытие окна сворачивает клиент в трей, если он доступен. Пункт **Выход** в меню трея отключает туннель и закрывает приложение.

## Терминал

В Windows откройте **Terminal setup** из меню «Пуск» или запустите PowerShell от имени администратора в папке portable-версии

```powershell
.\vk-turn-proxy-console.exe -wizard
```

В Linux запустите установленный терминальный клиент

```sh
sudo vk-turn-proxy-console -wizard
```

Для portable-версии используйте `sudo ./vk-turn-proxy-console -wizard`. Следуйте шагам мастера. **Ctrl+C** отключает туннель. Если профиль требует входа в VK, добавьте `-vk-cookie-file .\cookies.txt`.

## Сборка

```sh
docker build --build-arg VERSION=v1.0.2 --output dist/windows -f Dockerfile.windows .
docker build --build-arg VERSION=v1.0.2 --output dist/linux -f Dockerfile.linux .
```

В `dist` появятся ZIP и установщики Windows, архивы и DEB-пакеты Linux для x64 и ARM64 с контрольными суммами.

## Связанные проекты

Общий код транспорта взят из проекта anton48. [Клиент для iOS](https://github.com/anton48/vk-turn-proxy-ios) · [Сервер](https://github.com/anton48/vk-turn-proxy)

[GPL-3.0](LICENSE). Лицензии зависимостей входят в пакеты.
