[Русский](build.ru.md) · [English](build.en.md)

[Главная](../README.md)

# Сборка

Выполняйте команды из корня репозитория.


Все платформы используют общий интерфейс HTML/CSS/JavaScript, встроенный в Go-приложение на Wails v2. Интерфейс и шрифты входят в приложение. Сборка через Node.js не требуется. Для Linux используются CGO, GTK 3 и WebKitGTK 4.1. Docker собирает обе архитектуры штатными компиляторами через эмуляцию buildx.


```sh
docker build --build-arg VERSION=v1.10.0 --build-arg RUN_TESTS=false --output dist/windows -f Dockerfile.windows .
docker build --target packages --build-arg VERSION=v1.10.0 --build-arg RUN_TESTS=false --output dist/linux -f Dockerfile.linux .
```

В `dist` появятся ZIP и установщики Windows, архивы и DEB-пакеты Linux для x64 и ARM64 с контрольными суммами.

Пакеты macOS собираются на Mac с Xcode Command Line Tools и Apple SDK

```sh
sh installer/macos/package.sh v1.10.0 dist/macos
```

Команда создаёт ZIP для Intel и Apple Silicon с ad-hoc подписью и контрольными суммами.

