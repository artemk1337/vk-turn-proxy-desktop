# Desktop client

Follow the root [AGENTS.md](../../AGENTS.md).

- Keep link import, manual WireGuard setup, advanced settings, logs and preferences in distinct UI areas.
- Show the expected link format as a placeholder. Preserve profile input when changing language or theme.
- Update language and theme in place during a connection. Keep all labels readable in light and gray dark palettes.
- Progress follows backend events. Use indeterminate animation while waiting and show the active connection stage.
- Bound pending log bytes, lines and UI callbacks, then append batches. Read backend output continuously. Retain connection-state events under log overload.
- Append logs incrementally. Follow the bottom while reading new entries and preserve viewport and selection while browsing history.
- Store preferences and private session files in the current user's ACL-protected LocalAppData directory. The machine recovery journal is administrator-protected ProgramData.
- Autostart opens the app at Windows sign-in through an owned current-user scheduled task. Reconcile the checkbox with Windows. Change the task on explicit user interaction.
- User profiles and preferences outlive uninstall. Installer cleanup targets the installed copy's own startup task.
- Windows tests capture light/dark progress images when `VKTURN_UI_CAPTURE_DIR` is set. Inspect the images along with every setup tab before releasing UI changes.

- Closing the window hides it in the tray. Explicit Exit runs normal network cleanup before terminating. Tray actions, tooltip and icon reflect the current connection state and language.

- Cookie imports and website login share the backend cookie validation. Never put cookies in preferences, logs or command-line arguments; pass them through private session files and remove those after the backend exits.
- Website login uses an isolated temporary WebView2 profile. Extract only the VK authentication cookie pair, close the controller on success or cancellation, and remove the browser profile. Show an actionable message when the Runtime is missing.

- Tab headers have one buffered renderer. Handle native paint messages before the default Walk renderer to prevent duplicate hover text. Preserve keyboard focus and selection.
- Keep settings controls in bounded columns and align the startup checkbox explicitly to the left. Check layout at default and minimum window sizes in both languages and themes.
