[Русский](build.ru.md) · [English](build.en.md)

[Home](../README.en.md)

# Build

Run the commands from the repository root.


The desktop applications embed the same HTML/CSS/JavaScript frontend in a Wails v2 Go executable. The frontend is bundled with the application, including its fonts, and requires no Node.js build step. Linux builds use CGO with GTK 3 and WebKitGTK 4.1. Docker builds both CPU architectures with native toolchains through buildx emulation.


```sh
docker build --build-arg VERSION=v1.11.0 --build-arg RUN_TESTS=false --output dist/windows -f Dockerfile.windows .
docker build --target packages --build-arg VERSION=v1.11.0 --build-arg RUN_TESTS=false --output dist/linux -f Dockerfile.linux .
```

Produces Windows ZIPs/installers and Linux archives/DEB packages for x64 and ARM64, with checksums.

Build macOS packages on a Mac with Xcode Command Line Tools and the Apple SDK

```sh
sh installer/macos/package.sh v1.11.0 dist/macos
```

Produces ad-hoc signed application ZIPs for Intel and Apple Silicon with checksums.

