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
sudo apt install ./vk-turn-proxy-linux-v1.2.1-amd64.deb
```

Для portable-версии распакуйте `.tar.gz` и запустите `./vk-turn-proxy-desktop`. Нужны Python 3, GTK 3, PyGObject, iproute2 и polkit. При подключении приложение запросит права администратора.

## Подключение

В разделе **Подключение** нажмите **Добавить профиль** (в Windows **Добавить**), выберите ссылку/JSON профиля или WireGuard-конфиг с приглашением в VK-звонок и сохраните. Выберите профиль из списка и нажмите **Подключить**. Можно вставить JSON или Base64. В обоих клиентах доступны сохранение, выбор, редактирование и удаление именованных профилей.
При ручной настройке укажите TURN-сервер, обычно `host:56000`, или оставьте поле пустым для использования `Endpoint`. Выберите SRTP или DTLS в соответствии с сервером.

Ручной режим требует один peer, IPv4 и `AllowedIPs = 0.0.0.0/0`. Для SRTP сервер запускают с флагом `-srtp`.

Кнопка **Войти в VK** находится на главном экране. Учётная запись общая для всех профилей, сохраняется отдельно и проверяется каждые пять минут. Через **Cookies** можно загрузить Netscape-файл или вставить `remixsid=…; p=…`. Для входа через сайт нужен [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) в Windows или WebKitGTK (`gir1.2-webkit2-4.1`) в Linux.

В **Настройках** обоих клиентов доступны язык, тема, автозапуск и лимит журнала. По умолчанию сохраняются 1000 строк, диапазон настройки от 100 до 10 000, дополнительное ограничение размера составляет 4 МиБ. Закрытие окна сворачивает клиент в трей, если он доступен. **Выход** отключает туннель и закрывает приложение.

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
docker build --build-arg VERSION=v1.2.1 --output dist/windows -f Dockerfile.windows .
docker build --build-arg VERSION=v1.2.1 --output dist/linux -f Dockerfile.linux .
```

В `dist` появятся ZIP и установщики Windows, архивы и DEB-пакеты Linux для x64 и ARM64 с контрольными суммами.

## Связанные проекты

Общий код транспорта взят из проекта anton48. [Клиент для iOS](https://github.com/anton48/vk-turn-proxy-ios) · [Сервер](https://github.com/anton48/vk-turn-proxy)

[GPL-3.0](LICENSE). Лицензии зависимостей входят в пакеты.
