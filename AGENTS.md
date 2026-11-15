# Architecture

This repository develops the desktop GUI and console client. Windows and Linux have desktop interfaces. Shared transport code comes from anton48/vk-turn-proxy-ios and retains its license and authorship.

- `pkg/desktop-settings/settings.json` defines desktop languages, themes, transport defaults, log limits and VK check interval for both clients. Update this shared contract when changing these parameters. Saved profiles and the VK account have the same controls on both platforms.
- `pkg/proxy` and `pkg/turnbind` own the shared transport and WireGuard bind.
- `pkg/wireguard-profile` converts supported WireGuard configs into connection profiles.
- `tools/vk-turn-proxy-console` owns profile parsing, the setup wizard, tunnel, routes, DNS and crash recovery.
- `tools/vk-turn-proxy-windows` owns the desktop UI, preferences, current-user autostart and isolated VK website login through WebView2. See its local [AGENTS.md](tools/vk-turn-proxy-windows/AGENTS.md).
- `tools/vk-turn-proxy-linux` owns the GTK desktop interface and unprivileged session control. See its local [AGENTS.md](tools/vk-turn-proxy-linux/AGENTS.md).
- `installer` owns platform installation and removal. `Dockerfile.windows` and `Dockerfile.linux` build release packages.

Journal network changes before applying them. Route VK and TURN traffic through the physical gateway. Disconnect and parent exit run normal cleanup. Keep profile keys out of logs. Treat WireGuard configs as data and report unsupported directives before connecting. Each platform’s GUI and CLI share a machine-wide lock and recovery journal. Linux GTK runs as the user and elevates only the console through polkit.

## Documentation and releases

- Keep `README.md` in English, with links to separate `README.<language>.md` translations. Each translation links back to English.
- Explain supported profile formats, installer and portable setup, GUI and terminal workflows. Keep the wording direct and practical, with upstream attribution.
- Describe imported profiles by their data format in documentation and UI. Keep upstream attribution in Related projects and NOTICE.md.
- Release notes contain only a concise list of changes. Keep setup instructions and build details in the README.
- Build releases locally in Docker. Publish Windows ZIPs/installers and Linux TAR/DEB packages for x64 and ARM64 with checksums.
- Run console, profile, preferences and transport tests. Verify Windows startup, autostart lifecycle, installer install/uninstall, both languages and themes, and connection progress before publishing.

- Keep iOS project files, unrelated server tools and upstream workflow definitions out of this desktop repository. Preserve attribution and license notices when reusing code.

- Verify Linux GTK under Xvfb, backend parent-exit cleanup, manual/profile setup and DEB installation/removal. Keep authentication data out of preferences and logs on both platforms.

## Changelog format

- Start each version with `## vX.Y.Z - YYYY-MM-DD`. Mark the initial release as "Первый релиз".
- Group entries using Keep a Changelog categories in the changelog language. Use Added, Changed, Deprecated, Removed, Fixed and Security as needed, and omit empty groups.
- Each entry names the change and briefly describes the user-visible result. Keep one change per entry. Avoid implementation history, build/test details and generic phrases such as "various improvements".
- Use Markdown section headings and the same concise groups in GitHub release notes.
- `.github/workflows/release-cleanup.yml` retains the five most recently published releases, including prereleases, and removes older releases with their assets. It runs on publication, daily and manually. Drafts and Git tags are preserved.
