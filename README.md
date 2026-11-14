# VK TURN Proxy Desktop

Desktop VPN client that sends WireGuard traffic through VK TURN relays. The current release is for Windows. Supports profile links, WireGuard configs, VK login, tray controls and autostart. English and Russian interface with light and dark themes.

[Русский](README.ru.md)

## Install

Download the package for **amd64 (x64)** or **arm64** from [Releases](https://github.com/artemk1337/vk-turn-proxy-desktop/releases/latest).

- **Installer** — run `*-setup.exe`. The app and terminal wizard appear in the Start menu.
- **Portable** — extract the ZIP and run `vk-turn-proxy-windows.exe`. Keep both executables and `wintun.dll` together.

Approve the administrator prompt to manage the VPN tunnel.

## Connect

1. Paste a `vkturnproxy://import?data=…` link in **Profile link**, or open a JSON profile or iOS backup. Plain JSON and Base64 are also accepted.
2. Alternatively, open **WireGuard config**, load a `.conf` and enter a VK call invitation. Set the TURN address, usually `host:56000`, or leave it empty to use `Endpoint`. Select SRTP or DTLS to match your server.
3. Click **Connect**. Use **Disconnect** to stop the tunnel.

Manual configs require one peer, IPv4 and `AllowedIPs = 0.0.0.0/0`. SRTP requires the server’s `-srtp` option.

For VK account access, open **Advanced** and click **Sign in to VK**, import Netscape `cookies.txt` or paste `remixsid=…; p=…`. Website login requires [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/).

Language, theme and autostart are in **Settings**. Closing the window hides it in the tray. Choose **Exit** in the tray menu to disconnect and close the app.

## Terminal

Open **Terminal setup** from the Start menu, or run PowerShell as Administrator in the portable folder

```powershell
.\vk-turn-proxy-console.exe -wizard
```

Follow the setup steps. Press **Ctrl+C** to disconnect. Add `-vk-cookie-file .\cookies.txt` if your profile requires VK login.

## Build

```sh
docker build --build-arg VERSION=v0.4.1 --output dist -f Dockerfile.windows .
```

Produces x64 and ARM64 ZIPs, installers and checksums.

## Related projects

Shared transport code comes from anton48’s project. [iOS client](https://github.com/anton48/vk-turn-proxy-ios) · [Server](https://github.com/anton48/vk-turn-proxy)

[GPL-3.0](LICENSE). Dependency license notices are included in the packages.
