# Shared desktop client

Follow the root [AGENTS.md](../../AGENTS.md).

- `main.go` embeds the static frontend and starts Wails with the `desktop` build tag. `app.go`, `store.go` and `session.go` own the bridge, private stores and console lifecycle. Platform files own privilege handling, atomic private storage, autostart and isolated website login. Tray files own native tray integration.
- Keep the frontend dependency-free HTML/CSS/JavaScript. Read defaults from `GetState().defaults`, backed by `pkg/desktop-settings`.
- Frontend design, shared controls, animations and WebView checks follow [frontend/AGENTS.md](frontend/AGENTS.md).
- Preserve existing Windows DPAPI profile/account files and Linux private JSON/account files. Presentation preferences contain no authentication data. Secrets cross the local Wails bridge and private session files only.
- Persist `activeID` when selecting a profile and restore it on startup. Preserve existing profile metadata when editing the visible fields.
- Backend progress follows retained console phases. Keep log bytes and lines bounded, preserve the reader's viewport, and keep theme/language changes independent from connection and editor state.
- Closing hides the window only while a tray host is available. Exit requests the console stop file and waits for cleanup. Linux elevates only the console through pkexec.
- macOS elevates only the console through the native administrator prompt. A private Unix socket accepts root peers, streams logs and requests shutdown when the authorization launcher exits. The launcher owns pending-prompt cancellation and the temporary socket directory. User autostart is an owned LaunchAgent loaded at sign-in.
- Run `go test ./tools/vk-turn-proxy-desktop`. Build with `-tags desktop,production` and add `webkit2_41` on Linux. `--smoke-test` opens an isolated native window and verifies frontend bridge readiness before exiting.
