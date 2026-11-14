# VK TURN Proxy Desktop

Desktop VPN client that sends WireGuard traffic through VK TURN relays. Available for Windows and Linux. Supports profile links, WireGuard configs, VK login, tray controls and autostart. English and Russian interface with light and dark themes.

[Русский](README.ru.md)

## Install

Download the package for **amd64 (x64)** or **arm64** from [Releases](https://github.com/artemk1337/vk-turn-proxy-desktop/releases/latest).

### Windows

- **Installer** — run `*-setup.exe`. The app and terminal wizard appear in the Start menu.
- **Portable** — extract the ZIP and run `vk-turn-proxy-windows.exe`. Keep both executables and `wintun.dll` together.

Approve the administrator prompt to manage the VPN tunnel.

### Linux

Install the `.deb` on Debian or Ubuntu and open **VK TURN Proxy** from the applications menu

```sh
sudo apt install ./vk-turn-proxy-linux-v1.0.1-amd64.deb
```

For portable use, extract the `.tar.gz` and run `./vk-turn-proxy-desktop`. It requires Python 3, GTK 3, PyGObject, iproute2 and polkit. The app requests administrator access when connecting.

## Connect

1. Paste a `vkturnproxy://import?data=…` link in **Profile link**, or open a JSON profile. Plain JSON and Base64 are also accepted.
2. Alternatively, open **WireGuard config**, load a `.conf` and enter a VK call invitation. Set the TURN address, usually `host:56000`, or leave it empty to use `Endpoint`. Select SRTP or DTLS to match your server.
3. Click **Connect**. Use **Disconnect** to stop the tunnel.

Manual configs require one peer, IPv4 and `AllowedIPs = 0.0.0.0/0`. SRTP requires the server’s `-srtp` option.

For VK account access, open **Advanced** and click **Sign in to VK**, import Netscape `cookies.txt` or paste `remixsid=…; p=…`. Website login uses [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) on Windows and WebKitGTK (`gir1.2-webkit2-4.1`) on Linux.

Language, theme and autostart are in **Settings**. Closing the window hides it in the tray when available. Choose **Exit** in the tray menu to disconnect and close the app.

## Terminal

On Windows, open **Terminal setup** from the Start menu, or run PowerShell as Administrator in the portable folder

```powershell
.\vk-turn-proxy-console.exe -wizard
```

On Linux, run the installed terminal client

```sh
sudo vk-turn-proxy-console -wizard
```

For the portable package, use `sudo ./vk-turn-proxy-console -wizard`. Follow the setup steps. Press **Ctrl+C** to disconnect. Add `-vk-cookie-file .\cookies.txt` if your profile requires VK login.

## Build

```sh
docker build --build-arg VERSION=v1.0.1 --output dist/windows -f Dockerfile.windows .
docker build --build-arg VERSION=v1.0.1 --output dist/linux -f Dockerfile.linux .
```

Produces Windows ZIPs/installers and Linux archives/DEB packages for x64 and ARM64, with checksums.

## Related projects

Shared transport code comes from anton48’s project. [iOS client](https://github.com/anton48/vk-turn-proxy-ios) · [Server](https://github.com/anton48/vk-turn-proxy)

[GPL-3.0](LICENSE). Dependency license notices are included in the packages.
