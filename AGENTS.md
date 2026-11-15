# Architecture

This repository develops the desktop GUI and console client for Windows, Linux and macOS. Shared transport code comes from anton48/vk-turn-proxy-ios and retains its license and authorship.

- `pkg/desktop-settings/settings.json` defines desktop languages, themes, transport defaults, log limits and VK check interval for both clients. Update this shared contract when changing these parameters. Saved profiles and the VK account have the same controls on both platforms.
- `pkg/proxy` and `pkg/turnbind` own the shared transport and WireGuard bind.
- `pkg/wireguard-profile` converts supported WireGuard configs into connection profiles.
- `pkg/routing-data` loads and atomically caches the Loyalsoldier GeoIP/GeoSite catalogs. `pkg/routing-policy` validates profile rules, matches domains and aggregates IPv4 route plans.
- `tools/vk-turn-proxy-console` owns profile parsing, the setup wizard, tunnel, routes, DNS and crash recovery.
- `tools/vk-turn-proxy-desktop` owns the shared Wails v2 GUI, embedded HTML/CSS/JavaScript and fonts, preferences, profiles and platform session control. See its local [AGENTS.md](tools/vk-turn-proxy-desktop/AGENTS.md).
- `tools/vk-turn-proxy-windows` retains legacy Walk sources and isolated WebView2 authentication support. See its local [AGENTS.md](tools/vk-turn-proxy-windows/AGENTS.md).
- `tools/vk-turn-proxy-linux` retains legacy GTK sources and optional isolated WebKitGTK authentication helpers. See its local [AGENTS.md](tools/vk-turn-proxy-linux/AGENTS.md).
- `installer` owns platform installation and removal. `Dockerfile.windows` and `Dockerfile.linux` build release packages.
- `installer/macos` builds native Apple SDK application bundles. See its local [AGENTS.md](installer/macos/AGENTS.md).

Persist confirmation that the TURN cache is reusable only after all allocation attempts have confirmed releases and transport sessions have joined. Consume the release confirmation durably before reusing cached credentials. Unknown or interrupted releases retain the quota cooldown.

Journal network changes before applying them. Route VK and TURN traffic through the physical gateway. Disconnect and parent exit run normal cleanup. Keep profile keys out of logs. Treat WireGuard configs as data and report unsupported directives before connecting. Each platform’s GUI and CLI share a machine-wide lock and recovery journal. Linux and macOS Wails run as the user. Linux elevates the console through polkit. macOS installs a root-owned LaunchDaemon helper through an administrator prompt, then uses an authenticated Unix socket for connections. The helper accepts bounded profile data and allowlisted VPN parameters, runs its protected console copy and keeps journal/cache paths fixed. macOS keeps its protected journal and credential cache in `/var/db/vk-turn-proxy-desktop`; parent exit and helper connection closure trigger normal cleanup.

Routing policies are per-profile snapshots. Missing routing settings preserve full-tunnel behavior. Apply IPv4 route plans through the recovery journal and keep IPv6 blocked. GeoSite and custom domains use the console's loopback UDP/TCP DNS forwarder and install routes before returning DNS answers, including HTTPS/SVCB IPv4 hints. Physical transport pins retain priority. Keep local destinations direct when requested and configured VPN DNS reachable through the tunnel. Download routing databases as the GUI user and retain the previous complete pair on update failure. Pass bounded resolved policy data through the macOS helper into its protected session directory.

## Documentation and releases

- Keep `README.md` in English, with links to separate `README.<language>.md` translations. Each translation links back to English.
- Explain supported profile formats, installer and portable setup, GUI and terminal workflows. Keep the wording direct and practical, with upstream attribution.
- Describe imported profiles by their data format in documentation and UI. Keep upstream attribution in Related projects and NOTICE.md.
- Release notes contain only a concise list of changes. Keep setup instructions and build details in the README.
- Build Windows and Linux releases locally in Docker. Build macOS application ZIPs natively with the Apple SDK. Publish all platforms for x64 and ARM64 with checksums.
- Use `assets/logo.png` as the canonical logo for frontend branding and platform application icons. Include localized main-window screenshots in both READMEs and their release packages.
- Run console, profile, preferences and transport tests. Verify Windows startup, autostart lifecycle, installer install/uninstall, both languages and themes, and connection progress before publishing.

- Keep iOS project files, unrelated server tools and upstream workflow definitions out of this desktop repository. Preserve attribution and license notices when reusing code.

- Verify Linux Wails under Xvfb, backend parent-exit cleanup, manual/profile setup and DEB installation/removal. Keep authentication data out of preferences and logs on both platforms.

## Changelog format

- Start each version with `## vX.Y.Z - YYYY-MM-DD`. Mark the initial release as "Первый релиз".
- Group entries using Keep a Changelog categories in the changelog language. Use Added, Changed, Deprecated, Removed, Fixed and Security as needed, and omit empty groups.
- Each entry names the change and briefly describes the user-visible result. Keep one change per entry. Avoid implementation history, build/test details and generic phrases such as "various improvements".
- Use Markdown section headings and the same concise groups in GitHub release notes.
- `.github/workflows/release-cleanup.yml` retains the five most recently published releases, including prereleases, and removes older releases with their assets. It runs on publication, daily and manually. Drafts and Git tags are preserved.
