# Linux desktop

Follow the root [AGENTS.md](../../AGENTS.md).

- `main.py` owns the Connection, Log and Settings tabs, preferences, XDG autostart and the console process lifecycle. `profile_panel.py` and `profile_dialog.py` own saved profile controls. `profile_store.py` persists profiles and the global VK session in separate atomic private files. `vk_session.py` validates cookies through the shared console and checks the account every five minutes. `model.py` owns bounded logs and private storage. `auth.py` owns optional ephemeral WebKitGTK login.
- Read defaults and limits from `desktop_settings.py`, backed by the JSON contract shared with Windows. Keep language, theme, autostart, log-line limits and profile parameters aligned across clients. Persistent profile and session files use mode 0600 inside the current user's mode-0700 config directory.
- Run the GUI as the desktop user. Elevate only the console through pkexec. Keep the root backend's journal, credential cache and PID lock at its machine defaults.
- Pass profiles, WireGuard configs, call invitations and cookies through private session files. Keep secrets out of preferences, logs and command arguments. Remove session files after the console exits.
- Closing with a connected tray host hides the window. Explicit Exit and closing without a tray host wait for normal backend cleanup. Parent death also stops the backend.
- Keep profile input when switching editor modes. Language and theme changes preserve the selected profile, authentication and connection state.
- Bound pending log bytes and lines, retain connection events under overload, and batch GTK changes on the main loop. Preserve the visible log history while scrolling.
- Autostart is a current-user XDG desktop entry and opens the UI without connecting. The entry includes TryExec so removal of the package does not start a stale launcher.
- Run `python3 -m unittest discover -s tools/vk-turn-proxy-linux -p '*_test.py'`. Under Xvfb, `main.py --smoke-test --capture-ui <directory>` checks native GTK controls and a local fixture backend, without connecting a VPN.
