# Linux desktop

Follow the root [AGENTS.md](../../AGENTS.md).

- `main.py` owns GTK controls, preferences, XDG autostart and the console process lifecycle. `model.py` owns bounded pending logs, cookie selection and private storage. `auth.py` owns optional ephemeral WebKitGTK login.
- Run the GUI as the desktop user. Elevate only the console through pkexec. Keep the root backend's journal, credential cache and PID lock at its machine defaults.
- Pass profiles, WireGuard configs, call invitations and cookies through private session files. Keep secrets out of preferences, logs and command arguments. Remove session files after the console exits.
- Closing with a connected tray host hides the window. Explicit Exit and closing without a tray host wait for normal backend cleanup. Parent death also stops the backend.
- Keep input mode when visiting Advanced, Log or Settings. Language and theme changes preserve profile input and connection state.
- Bound pending log bytes and lines, retain connection events under overload, and batch GTK changes on the main loop. Preserve the visible log history while scrolling.
- Autostart is a current-user XDG desktop entry and opens the UI without connecting. The entry includes TryExec so removal of the package does not start a stale launcher.
- Run `python3 -m unittest discover -s tools/vk-turn-proxy-linux -p '*_test.py'`. Under Xvfb, `main.py --smoke-test --capture-ui <directory>` checks native GTK controls and a local fixture backend, without connecting a VPN.
