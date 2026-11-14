# Architecture

This repository develops the desktop GUI and console client. Windows is the current GUI platform. Shared transport code comes from anton48/vk-turn-proxy-ios and retains its license and authorship.

- `pkg/proxy` and `pkg/turnbind` own the shared transport and WireGuard bind.
- `pkg/wireguard-profile` converts supported WireGuard configs into connection profiles.
- `tools/vk-turn-proxy-console` owns profile parsing, the setup wizard, tunnel, routes, DNS and crash recovery.
- `tools/vk-turn-proxy-windows` owns the desktop UI, preferences, current-user autostart and isolated VK website login through WebView2. See its local [AGENTS.md](tools/vk-turn-proxy-windows/AGENTS.md).
- `installer` owns Windows installation and removal. `Dockerfile.windows` builds release packages.

Journal network changes before applying them. Route VK and TURN traffic through the physical gateway. Disconnect and parent exit run normal cleanup. Keep profile keys out of logs. Treat WireGuard configs as data and report unsupported directives before connecting. Windows GUI and CLI share the machine-wide lock and recovery journal.

## Documentation and releases

- Keep `README.md` in English, with links to separate `README.<language>.md` translations. Each translation links back to English.
- Explain supported profile formats, installer and portable setup, GUI and terminal workflows. Keep the wording direct and practical, with upstream attribution.
- Release notes contain only a concise list of changes. Keep setup instructions and build details in the README.
- Build releases locally in Docker. Publish portable ZIPs and installers for x64 and ARM64 with checksums for every package.
- Run console, profile, preferences and transport tests. Verify Windows startup, autostart lifecycle, installer install/uninstall, both languages and themes, and connection progress before publishing.

- Keep iOS project files, unrelated server tools and upstream workflow definitions out of this desktop repository. Preserve attribution and license notices when reusing code.
