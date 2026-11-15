# Shared desktop client

Follow the root [AGENTS.md](../../AGENTS.md).

- `main.go` embeds the static frontend and starts Wails with the `desktop` build tag. `app.go`, `store.go` and `session.go` own the bridge, private stores and console lifecycle. Platform files own privilege handling, atomic private storage, autostart and isolated website login. Tray files own native tray integration.
- Keep the frontend dependency-free HTML/CSS/JavaScript. Read defaults from `GetState().defaults`, backed by `pkg/desktop-settings`.
- `routing.go` owns per-profile routing settings, catalog bridge methods and background database updates. Resolve GeoIP/GeoSite into a bounded immutable console policy in the session directory. Catalog downloads use their own lock and run without holding the app-state lock. Routing edits apply on the next connection.
- Frontend design, shared controls, animations and WebView checks follow [frontend/AGENTS.md](frontend/AGENTS.md).
- Preserve existing Windows DPAPI profile/account files and Linux private JSON/account files. Presentation preferences contain no authentication data. Secrets cross the local Wails bridge and private session files only.
- Persist `activeID` when selecting a profile and restore it on startup. Preserve existing profile metadata when editing the visible fields.
- The console streams `desktop-traffic <txBytes> <rxBytes>` once per second through its existing private log pipe when `-desktop-traffic` is enabled. Consume these counters before the visible log, expose their measured rate in connection state, and reset stale or ended sessions.
- Backend progress follows retained console phases. Keep log bytes and lines bounded, preserve the reader's viewport, and keep theme/language changes independent from connection and editor state.
- Closing hides the window only while a tray host is available. Exit requests the console stop file and waits for cleanup. Linux elevates only the console through pkexec.
- macOS website login uses an isolated nonpersistent WKWebView on the AppKit main queue. Closing or cancellation discards the web session. Only the required VK cookies reach the existing private account store.
- macOS starts an unprivileged console relay with `--desktop-helper-client`. It installs/updates the root-owned LaunchDaemon helper through an administrator prompt, authenticates its Unix socket peer and streams logs and exit status. The inherited parent pipe cancels pending installation and closes the helper connection on GUI exit. The helper owns private profile snapshots and invokes its protected console copy with fixed machine-wide storage. User autostart is an owned LaunchAgent loaded at sign-in.
- Run `go test ./tools/vk-turn-proxy-desktop`. Build with `-tags desktop,production` and add `webkit2_41` on Linux. `--smoke-test` opens an isolated native window and verifies frontend bridge readiness before exiting.
