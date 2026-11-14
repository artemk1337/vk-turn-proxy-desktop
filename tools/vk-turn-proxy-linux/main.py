#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""GTK desktop interface for the shared VK TURN console backend."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import time

import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, Gio, GLib

from model import (MAX_INPUT, MAX_LOG, PHASE_ORDER, PendingLogs, load_preferences,
                   private_directory, write_private)
from translations import RUSSIAN

STYLE = b"""window { font-family: Sans; font-size: 14px; }
.content { padding: 24px; }
.brand { font-size: 25px; font-weight: bold; }
.subtitle, .hint { color: #66758a; }
.status { font-weight: bold; padding: 7px 12px; border-radius: 14px; background: alpha(#66758a,.12); }
.status.idle { color: #66758a; }
.status.busy { color: #397ddd; background: alpha(#397ddd,.12); }
.status.ready { color: #21865b; background: alpha(#21865b,.12); }
.status.failed { color: #d34b5b; background: alpha(#d34b5b,.12); }
.section-title { font-size: 17px; font-weight: bold; }
notebook { border-radius: 8px; }
notebook > header { padding: 4px; }
notebook > stack { padding: 18px; }
entry, textview { border-radius: 5px; }
textview text { padding: 10px; }
button { padding: 7px 13px; border-radius: 5px; }
button.suggested-action { background: #3276d3; color: white; }
button.suggested-action:disabled { background: #e6e8ec; color: #8994a3; }
.dark button.suggested-action:disabled { background: #3a3d42; color: #8994a3; }
progressbar trough { min-height: 6px; border-radius: 3px; }
progressbar progress { min-height: 6px; border-radius: 3px; }
progressbar.ready progress { background: #21865b; }
.dark .subtitle, .dark .hint { color: #a9b5c6; }
.dark .status.idle { color: #a9b5c6; }
.dark .status.busy { color: #80b2ff; }
.dark .status.ready { color: #77d6a7; }
.dark .status.failed { color: #ff9aa4; }
"""


class Desktop(Gtk.Application):
    def __init__(self, args):
        super().__init__(application_id="io.github.artemk1337.vkturnproxy",
                         flags=Gio.ApplicationFlags.FLAGS_NONE)
        self.args = args
        self.test_home = tempfile.TemporaryDirectory(prefix="vkturn-ui-test-") if args.smoke_test else None
        base = Path(self.test_home.name) if self.test_home else Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
        self.config = private_directory(base / "vk-turn-proxy-desktop")
        self.preferences_path = self.config / "preferences.json"
        self.preferences = load_preferences(self.preferences_path)
        self.bindings = []
        self.placeholders = []
        self.window = None
        self.process = None
        self.session = None
        self.reader_done = threading.Event()
        self.logs = PendingLogs()
        self.state = "disconnected"
        self.input_mode = 0
        self.closing = False
        self.translating = False
        self.indicator = None
        self.exit_code = 0
        self.smoke_complete = False
        self.connect("activate", self.activate_window)

    def tr(self, text):
        return RUSSIAN.get(text, text) if self.preferences["language"] == "ru" else text

    def bind(self, widget, text, method="set_label"):
        self.bindings.append((widget, text, method))
        getattr(widget, method)(self.tr(text))
        return widget

    def label(self, text, css=""):
        widget = self.bind(Gtk.Label(xalign=0), text, "set_text")
        widget.set_line_wrap(True)
        if css:
            widget.get_style_context().add_class(css)
        return widget

    def button(self, text, clicked):
        widget = self.bind(Gtk.Button(), text)
        widget.connect("clicked", clicked)
        return widget

    def entry(self, placeholder=""):
        widget = Gtk.Entry()
        if placeholder:
            self.bind(widget, placeholder, "set_placeholder_text")
        return widget

    def editor(self, placeholder="", height=180, editable=True):
        editor = Gtk.TextView()
        editor.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        editor.set_monospace(True)
        editor.set_editable(editable)
        editor.set_cursor_visible(editable)
        scroll = Gtk.ScrolledWindow()
        scroll.set_shadow_type(Gtk.ShadowType.IN)
        scroll.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        scroll.set_min_content_height(height)
        scroll.add(editor)
        if not placeholder:
            return editor, scroll
        overlay = Gtk.Overlay()
        overlay.add(scroll)
        hint = Gtk.Label(label=placeholder, xalign=0, yalign=0)
        hint.set_halign(Gtk.Align.START)
        hint.set_valign(Gtk.Align.START)
        hint.set_margin_start(12)
        hint.set_margin_top(12)
        hint.get_style_context().add_class("hint")
        overlay.add_overlay(hint)
        overlay.set_overlay_pass_through(hint, True)
        hint.set_no_show_all(True)
        editor.get_buffer().connect("changed", lambda buf: hint.set_visible(buf.get_char_count() == 0))
        self.placeholders.append(hint)
        return editor, overlay

    @staticmethod
    def text(editor):
        buf = editor.get_buffer()
        return buf.get_text(buf.get_start_iter(), buf.get_end_iter(), True)

    @staticmethod
    def page():
        return Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)

    @staticmethod
    def row(*widgets):
        box = Gtk.Box(spacing=8)
        for widget in widgets:
            box.pack_start(widget, False, False, 0)
        return box

    def activate_window(self, _app):
        try:
            self.create_window()
        except Exception as error:
            print("Could not open GTK window:", error, file=sys.stderr)
            self.exit_code = 1
            if self.window and not self.args.smoke_test:
                self.error(str(error))
            self.quit()

    def create_window(self):
        if self.window:
            self.window.present()
            return
        self.window = Gtk.ApplicationWindow(application=self, title="VK TURN Proxy Desktop")
        self.window.set_default_size(850, 730)
        self.window.set_size_request(730, 640)
        icon = Path(__file__).with_name("vk-turn-proxy-desktop.svg")
        if icon.exists():
            try:
                self.window.set_icon_from_file(str(icon))
            except GLib.Error:
                self.window.set_icon_name("network-vpn")
        self.window.connect("delete-event", self.close_requested)
        content = self.page()
        content.get_style_context().add_class("content")
        self.window.add(content)
        header = Gtk.Box(spacing=16)
        brand = self.page()
        brand.set_spacing(4)
        brand.pack_start(Gtk.Label(label="VK TURN Proxy", xalign=0), False, False, 0)
        brand.get_children()[0].get_style_context().add_class("brand")
        brand.pack_start(self.label("WireGuard through VK calls", "subtitle"), False, False, 0)
        header.pack_start(brand, True, True, 0)
        self.status = Gtk.Label()
        self.status.set_valign(Gtk.Align.START)
        self.status.get_style_context().add_class("status")
        header.pack_end(self.status, False, False, 0)
        content.pack_start(header, False, False, 8)
        self.tabs = Gtk.Notebook()
        self.pages = [self.profile_page(), self.manual_page(), self.advanced_page(),
                      self.log_page(), self.settings_page()]
        for page, title in zip(self.pages, ("Profile link", "WireGuard config", "Advanced", "Log", "Settings")):
            self.tabs.append_page(page, self.bind(Gtk.Label(), title, "set_text"))
        self.tabs.connect("switch-page", self.mode_changed)
        content.pack_start(self.tabs, True, True, 0)
        self.phase_label = Gtk.Label(xalign=0)
        self.phase_label.get_style_context().add_class("hint")
        content.pack_start(self.phase_label, False, False, 0)
        self.progress = Gtk.ProgressBar()
        content.pack_start(self.progress, False, False, 0)
        controls = Gtk.Box(spacing=10)
        self.close_hint = self.label("Closing disconnects the VPN.", "hint")
        controls.pack_start(self.close_hint, True, True, 0)
        self.disconnect_button = self.button("Disconnect", lambda _: self.stop())
        self.connect_button = self.button("Connect", lambda _: self.start())
        self.connect_button.get_style_context().add_class("suggested-action")
        controls.pack_end(self.connect_button, False, False, 0)
        controls.pack_end(self.disconnect_button, False, False, 0)
        content.pack_start(controls, False, False, 0)
        provider = Gtk.CssProvider()
        provider.load_from_data(STYLE)
        Gtk.StyleContext.add_provider_for_screen(Gdk.Screen.get_default(), provider,
                                                 Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        self.apply_preferences()
        self.set_running(False)
        self.make_tray()
        self.window.show_all()
        for hint in self.placeholders:
            hint.show()
        self.close_hint.set_visible(not self.tray_available())
        GLib.timeout_add(100, self.tick)
        if self.args.smoke_test:
            GLib.timeout_add(250, self.smoke_begin)

    def profile_page(self):
        page = self.page()
        page.pack_start(self.label("Connection profile", "section-title"), False, False, 0)
        page.pack_start(self.label("Paste a connection link or JSON profile.", "hint"), False, False, 0)
        self.profile, widget = self.editor("vkturnproxy://import?data=<base64 JSON>", 220)
        page.pack_start(widget, True, True, 0)
        page.pack_start(self.row(self.button("Open profile…", lambda _: self.open_file(self.profile))), False, False, 0)
        return page

    def manual_page(self):
        page = self.page()
        page.pack_start(self.row(self.label("WireGuard configuration", "section-title"),
                                self.button("Open .conf…", lambda _: self.open_file(self.wireguard))), False, False, 0)
        self.wireguard, widget = self.editor("[Interface]\nPrivateKey = …\nAddress = …\n\n[Peer]\nPublicKey = …\nEndpoint = …", 150)
        page.pack_start(widget, True, True, 0)
        page.pack_start(self.label("VK call link"), False, False, 0)
        self.call_link = self.entry("https://vk.ru/call/join/…")
        page.pack_start(self.call_link, False, False, 0)
        grid = Gtk.Grid(column_spacing=14, row_spacing=8)
        grid.attach(self.label("TURN server (optional)"), 0, 0, 1, 1)
        grid.attach(self.label("Server transport"), 1, 0, 1, 1)
        self.endpoint = self.entry("Use Endpoint from the config")
        self.endpoint.set_hexpand(True)
        self.transport = Gtk.ComboBoxText()
        grid.attach(self.endpoint, 0, 1, 1, 1)
        grid.attach(self.transport, 1, 1, 1, 1)
        page.pack_start(grid, False, False, 0)
        return page

    def advanced_page(self):
        page = self.page()
        page.pack_start(self.label("Server name", "section-title"), False, False, 0)
        page.pack_start(self.label("For JSON profiles containing several servers.", "hint"), False, False, 0)
        self.server_name = self.entry()
        page.pack_start(self.server_name, False, False, 0)
        page.pack_start(self.label("VK login", "section-title"), False, False, 8)
        self.cookie_source = Gtk.Label(xalign=0)
        self.cookie_source.get_style_context().add_class("hint")
        self.cookies, widget = self.editor("remixsid=…; p=…", 120)
        page.pack_start(self.label("Paste Netscape cookies or remixsid=…; p=…", "hint"), False, False, 0)
        page.pack_start(widget, True, True, 0)
        page.pack_start(self.cookie_source, False, False, 0)
        page.pack_start(self.row(self.button("Choose cookies…", lambda _: self.open_file(self.cookies, cookies=True)),
                                self.button("Sign in to VK", self.website_login),
                                self.button("Clear", self.clear_cookies)), False, False, 0)
        page.pack_start(self.label("Cookies stay in this session.", "hint"), False, False, 0)
        return page

    def log_page(self):
        self.log, scroll = self.editor(height=260, editable=False)
        self.log_scroll = scroll
        return scroll

    def settings_page(self):
        page = self.page()
        page.pack_start(self.label("Language", "section-title"), False, False, 0)
        self.language = Gtk.ComboBoxText()
        self.language.append("en", "English")
        self.language.append("ru", "Русский")
        self.language.connect("changed", self.preference_changed)
        page.pack_start(self.language, False, False, 0)
        page.pack_start(self.label("Appearance", "section-title"), False, False, 8)
        self.theme = Gtk.ComboBoxText()
        self.theme.connect("changed", self.preference_changed)
        page.pack_start(self.theme, False, False, 0)
        self.autostart = self.bind(Gtk.CheckButton(), "Start with Linux")
        self.autostart.connect("toggled", self.autostart_changed)
        page.pack_start(self.autostart, False, False, 8)
        page.pack_start(self.label("Autostart opens the app. Connect when you are ready.", "hint"), False, False, 0)
        return page

    def preference_changed(self, _widget):
        if self.translating:
            return
        self.preferences = {"language": self.language.get_active_id() or "en",
                            "theme": self.theme.get_active_id() or "light"}
        try:
            write_private(self.preferences_path, json.dumps(self.preferences))
            self.apply_preferences()
        except OSError as error:
            self.error(str(error))

    def mode_changed(self, _tabs, _page, index):
        if index in (0, 1):
            self.input_mode = index

    def apply_preferences(self):
        self.translating = True
        for widget, source, method in self.bindings:
            getattr(widget, method)(self.tr(source))
        self.language.set_active_id(self.preferences["language"])
        self.theme.remove_all()
        self.theme.append("light", self.tr("Light"))
        self.theme.append("dark", self.tr("Dark gray"))
        self.theme.set_active_id(self.preferences["theme"])
        transport = max(0, self.transport.get_active())
        self.transport.remove_all()
        for text in ("Native SRTP", "Legacy DTLS"):
            self.transport.append_text(self.tr(text))
        self.transport.set_active(transport)
        self.autostart.set_active(self.autostart_path().exists())
        dark = self.preferences["theme"] == "dark"
        Gtk.Settings.get_default().set_property("gtk-application-prefer-dark-theme", dark)
        context = self.window.get_style_context()
        context.add_class("dark") if dark else context.remove_class("dark")
        self.update_state()
        self.translating = False

    def autostart_path(self):
        return self.config.parent / "autostart" / "vk-turn-proxy-desktop.desktop"

    def autostart_changed(self, checkbox):
        if self.translating:
            return
        try:
            path = self.autostart_path()
            if checkbox.get_active():
                launcher = os.environ.get("VKTURN_DESKTOP_LAUNCHER", str(Path(__file__).with_name("vk-turn-proxy-desktop")))
                if any(c in launcher for c in "\r\n"):
                    raise ValueError("Invalid launcher path")
                escaped = launcher.replace("%", "%%").replace("\\", "\\\\").replace('"', '\\"').replace("`", "\\`").replace("$", "\\$")
                private_directory(path.parent)
                write_private(path, '[Desktop Entry]\nType=Application\nName=VK TURN Proxy\nExec="%s" --autostart\nTryExec=%s\nTerminal=false\n' % (escaped, launcher))
            else:
                path.unlink(missing_ok=True)
        except (OSError, ValueError) as error:
            self.translating = True
            checkbox.set_active(not checkbox.get_active())
            self.translating = False
            self.error(str(error))

    def make_tray(self):
        try:
            gi.require_version("AyatanaAppIndicator3", "0.1")
            from gi.repository import AyatanaAppIndicator3 as Indicator
        except (ValueError, ImportError):
            return
        self.indicator = Indicator.Indicator.new("vk-turn-proxy-desktop", "network-vpn-symbolic",
                                                Indicator.IndicatorCategory.APPLICATION_STATUS)
        menu = Gtk.Menu()
        self.tray_items = {}
        for title, callback in (("Open", lambda _: self.window.present()),
                                ("Connect", lambda _: self.start()),
                                ("Disconnect", lambda _: self.stop()),
                                ("Exit", lambda _: self.request_exit())):
            item = self.bind(Gtk.MenuItem(), title)
            item.connect("activate", callback)
            menu.append(item)
            self.tray_items[title] = item
        menu.show_all()
        self.indicator.set_menu(menu)
        self.indicator.set_status(Indicator.IndicatorStatus.ACTIVE)
        if self.indicator.find_property("connected"):
            self.indicator.connect("notify::connected", lambda *_: self.close_hint.set_visible(not self.tray_available()))
        self.update_state()

    def tray_available(self):
        return bool(self.indicator and self.indicator.find_property("connected") and
                    self.indicator.get_property("connected"))

    def close_requested(self, _window, _event):
        if self.tray_available() and not self.closing:
            self.window.hide()
            return True
        self.request_exit()
        return True

    def request_exit(self):
        self.closing = True
        if self.process:
            self.stop()
        else:
            self.quit()

    def open_file(self, target, cookies=False):
        dialog = Gtk.FileChooserDialog(title=self.tr("Open file"), transient_for=self.window,
                                       action=Gtk.FileChooserAction.OPEN)
        dialog.add_buttons(self.tr("Cancel"), Gtk.ResponseType.CANCEL, self.tr("Open"), Gtk.ResponseType.OK)
        if dialog.run() == Gtk.ResponseType.OK:
            try:
                with open(dialog.get_filename(), "rb") as source:
                    data = source.read(MAX_INPUT + 1)
                if len(data) > MAX_INPUT:
                    raise ValueError(self.tr("The input is larger than 4 MiB."))
                target.get_buffer().set_text(data.decode("utf-8-sig"))
                if cookies:
                    self.cookie_source.set_text(Path(dialog.get_filename()).name)
            except (OSError, ValueError, UnicodeError) as error:
                self.error(str(error))
        dialog.destroy()

    def clear_cookies(self, _button):
        self.cookies.get_buffer().set_text("")
        self.cookie_source.set_text("")

    def website_login(self, _button):
        from auth import login
        login(self.window, self.tr, lambda text: self.cookies.get_buffer().set_text(text))

    def backend_path(self):
        if self.args.smoke_test and os.environ.get("VKTURN_TEST_BACKEND"):
            return Path(os.environ["VKTURN_TEST_BACKEND"])
        return Path(__file__).resolve().with_name("vk-turn-proxy-console")

    def start(self):
        if self.process:
            return
        session = None
        try:
            backend = self.backend_path()
            if not backend.is_file() or not os.access(backend, os.X_OK):
                raise ValueError(self.tr("The terminal client is missing. Extract the complete package."))
            if os.geteuid() and not shutil.which("pkexec"):
                raise ValueError(self.tr("Install pkexec to authorize network changes."))
            manual = self.input_mode == 1
            raw = self.text(self.wireguard if manual else self.profile).strip()
            cookies = self.text(self.cookies).strip()
            if len(raw.encode("utf-8")) > MAX_INPUT or len(cookies.encode("utf-8")) > MAX_INPUT:
                raise ValueError(self.tr("The input is larger than 4 MiB."))
            if manual and (not raw or not self.call_link.get_text().strip()):
                raise ValueError(self.tr("Add a WireGuard configuration and VK call link."))
            if not manual and not raw:
                raise ValueError(self.tr("Paste a connection profile first."))
            session = Path(tempfile.mkdtemp(prefix="session-", dir=self.config))
            profile = session / "profile.txt"
            write_private(profile, raw)
            args = [str(backend)]
            if manual:
                link_path = session / "vk-link.txt"
                write_private(link_path, self.call_link.get_text().strip())
                args += ["-wg-config", str(profile), "-vk-link-file", str(link_path),
                         "-server-transport", "srtp" if self.transport.get_active() == 0 else "dtls"]
                if self.endpoint.get_text().strip():
                    args += ["-peer", self.endpoint.get_text().strip()]
            else:
                args += ["-config", str(profile)]
                if self.server_name.get_text().strip():
                    args += ["-server", self.server_name.get_text().strip()]
            if cookies:
                path = session / "cookies.txt"
                write_private(path, cookies)
                args += ["-vk-cookie-file", str(path)]
            args += ["-stop-file", str(session / "stop"), "-parent-pid", str(os.getpid()), "-block-ipv6=true"]
            if os.geteuid():
                args.insert(0, "pkexec")
            self.start_process(args, session)
        except (OSError, ValueError) as error:
            if session:
                shutil.rmtree(session)
            self.error(str(error))

    def start_process(self, command, session):
        self.process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                        stderr=subprocess.STDOUT, bufsize=8192)
        self.session = session
        self.reader_done.clear()
        self.logs = PendingLogs()
        self.log.get_buffer().set_text("")
        self.state = "credentials"
        self.set_running(True)
        self.update_state()

        def read_logs():
            try:
                while True:
                    raw = self.process.stdout.readline(8193)
                    if not raw:
                        break
                    line = raw[:8192].decode("utf-8", errors="replace")
                    if len(raw) > 8192:
                        while raw and not raw.endswith(b"\n"):
                            raw = self.process.stdout.readline(8193)
                        line += " [truncated]\n"
                    self.logs.append(line)
            finally:
                self.process.stdout.close()
                self.reader_done.set()
        threading.Thread(target=read_logs, name="backend-logs", daemon=True).start()

    def stop(self):
        if not self.process or self.state == "disconnecting":
            return
        try:
            write_private(self.session / "stop", "")
            self.state = "disconnecting"
            self.disconnect_button.set_sensitive(False)
            self.update_state()
        except OSError as error:
            self.closing = False
            self.error(str(error))

    def set_running(self, running):
        for page in self.pages[:3]:
            page.set_sensitive(not running)
        self.connect_button.set_sensitive(not running)
        self.disconnect_button.set_sensitive(running)

    def update_state(self):
        texts = {"disconnected": ("Disconnected", "", "idle"),
                 "credentials": ("Connecting…", "Fetching VK access", "busy"),
                 "relay": ("Connecting…", "Connecting to VK relay", "busy"),
                 "handshake": ("Connecting…", "Establishing WireGuard tunnel", "busy"),
                 "connected": ("Connected", "", "ready"),
                 "disconnecting": ("Disconnecting…", "Restoring network settings", "busy"),
                 "failed": ("Connection failed", "", "failed")}
        label, phase, css = texts[self.state]
        self.status.set_text(self.tr(label))
        self.phase_label.set_text(self.tr(phase))
        context = self.status.get_style_context()
        for name in ("idle", "busy", "ready", "failed"):
            context.remove_class(name)
        context.add_class(css)
        progress_context = self.progress.get_style_context()
        progress_context.add_class("ready") if css == "ready" else progress_context.remove_class("ready")
        if css != "busy":
            self.progress.set_fraction(1 if self.state == "connected" else 0)
        if self.indicator:
            self.indicator.set_icon_full("network-vpn-symbolic" if self.state == "connected" else "network-offline-symbolic",
                                         self.tr(label))
            self.indicator.set_title("VK TURN Proxy · " + self.tr(label))
            self.tray_items["Connect"].set_sensitive(self.process is None)
            self.tray_items["Disconnect"].set_sensitive(self.process is not None and self.state != "disconnecting")

    def append_log(self, text):
        if not text:
            return
        adjustment = self.log_scroll.get_vadjustment()
        follow = adjustment.get_value() + adjustment.get_page_size() >= adjustment.get_upper() - 20
        buffer = self.log.get_buffer()
        anchor = None
        if not follow:
            _found, at = self.log.get_iter_at_location(0, self.log.get_visible_rect().y)
            anchor = buffer.create_mark(None, at, True)
        buffer.insert(buffer.get_end_iter(), text)
        excess = buffer.get_char_count() - MAX_LOG
        if excess > 0:
            end = buffer.get_iter_at_offset(excess)
            end.forward_line()
            buffer.delete(buffer.get_start_iter(), end)

        def restore():
            if anchor:
                self.log.scroll_to_mark(anchor, 0, True, 0, 0)
                buffer.delete_mark(anchor)
            elif follow:
                adjustment.set_value(max(0, adjustment.get_upper() - adjustment.get_page_size()))
            return False
        GLib.idle_add(restore)

    def tick(self):
        text, phase, dropped = self.logs.drain()
        if dropped:
            text = "log: skipped %d earlier entries\n" % dropped + text
        self.append_log(text)
        if (phase and self.state in ("credentials", "relay", "handshake") and
                PHASE_ORDER[phase] > PHASE_ORDER[self.state]):
            self.state = phase
            self.update_state()
        if self.state in ("credentials", "relay", "handshake", "disconnecting"):
            self.progress.pulse()
        if self.process and self.process.poll() is not None and self.reader_done.is_set():
            code = self.process.returncode
            self.process = None
            shutil.rmtree(self.session)
            self.session = None
            self.state = "failed" if code else "disconnected"
            self.set_running(False)
            self.update_state()
            if self.closing:
                if self.args.smoke_test:
                    assert not self.smoke_exit_session.exists()
                    self.smoke_complete = True
                    print("GTK smoke passed: languages, themes, tabs, autostart, log bounds/history, backend disconnect, active Exit and session cleanup")
                self.quit()
        return True

    def error(self, text):
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True,
                                   message_type=Gtk.MessageType.ERROR,
                                   buttons=Gtk.ButtonsType.OK, text="VK TURN Proxy")
        dialog.format_secondary_text(text)
        dialog.run()
        dialog.destroy()

    def smoke_begin(self):
        try:
            if not self.backend_path().is_file():
                raise AssertionError("backend binary is missing")
            self.smoke_capture_step = 0
            self.smoke_variants = []
            for language, theme in (("en", "light"), ("ru", "dark")):
                self.smoke_variants.extend((language, theme, page, "disconnected") for page in range(5))
                self.smoke_variants.append((language, theme, 0, "credentials"))
            GLib.timeout_add(120, self.smoke_capture)
        except Exception as error:
            self.smoke_fail(error)
        return False

    def smoke_capture(self):
        try:
            if self.smoke_capture_step < len(self.smoke_variants):
                language, theme, page, state = self.smoke_variants[self.smoke_capture_step]
                self.state = state
                self.preferences = {"language": language, "theme": theme}
                self.apply_preferences()
                self.set_running(state != "disconnected")
                self.tabs.set_current_page(page)
                self.smoke_capture_step += 1
                GLib.timeout_add(100, self.capture_current)
                return True
            self.state = "disconnected"
            self.set_running(False)
            self.update_state()
            self.autostart.set_active(True)
            assert self.autostart_path().exists()
            self.autostart.set_active(False)
            assert not self.autostart_path().exists()
            self.tabs.set_current_page(1)
            self.tabs.set_current_page(2)
            assert self.input_mode == 1
            self.tabs.set_current_page(3)
            self.log.get_buffer().set_text("".join("history line %04d\n" % n for n in range(1000)))
            GLib.timeout_add(150, self.smoke_history_scroll)
        except Exception as error:
            self.smoke_fail(error)
        return False

    def smoke_history_scroll(self):
        try:
            buffer = self.log.get_buffer()
            self.log.scroll_to_iter(buffer.get_iter_at_line(200), 0, True, 0, 0)
            GLib.timeout_add(150, self.smoke_history_append)
        except Exception as error:
            self.smoke_fail(error)
        return False

    def smoke_history_append(self):
        try:
            _found, first = self.log.get_iter_at_location(0, self.log.get_visible_rect().y)
            self.smoke_first_line = first.get_line()
            self.append_log("a new log entry while reading history\n")
            GLib.timeout_add(150, self.smoke_history_check)
        except Exception as error:
            self.smoke_fail(error)
        return False

    def smoke_history_check(self):
        try:
            _found, first = self.log.get_iter_at_location(0, self.log.get_visible_rect().y)
            assert abs(first.get_line() - self.smoke_first_line) <= 1, "new logs moved the history viewport"
            self.append_log("x" * (2 * MAX_LOG))
            assert self.log.get_buffer().get_char_count() <= MAX_LOG
            session = Path(tempfile.mkdtemp(dir=self.config, prefix="fixture-"))
            script = "import pathlib,time,sys\np=pathlib.Path(sys.argv[1])\nfor i in range(2000): print('fixture log '+str(i))\nprint('DEFAULT ROUTE -> the tunnel',flush=True)\nwhile not p.exists(): time.sleep(.05)\n"
            self.start_process([sys.executable, "-c", script, str(session / "stop")], session)
            self.smoke_deadline = time.monotonic() + 10
            self.smoke_stopped = False
            GLib.timeout_add(100, self.smoke_backend)
        except Exception as error:
            self.smoke_fail(error)
        return False

    def capture_current(self):
        try:
            if self.args.capture_ui:
                path = Path(self.args.capture_ui)
                path.mkdir(parents=True, exist_ok=True)
                language, theme, page, state = self.smoke_variants[self.smoke_capture_step - 1]
                width, height = self.window.get_size()
                image = Gdk.pixbuf_get_from_window(self.window.get_window(), 0, 0, width, height)
                name = "%s-%s-progress.png" % (language, theme) if state != "disconnected" else "%s-%s-tab-%d.png" % (language, theme, page)
                image.savev(str(path / name), "png", [], [])
        except Exception as error:
            self.smoke_fail(error)
        return False

    def smoke_backend(self):
        try:
            if time.monotonic() > self.smoke_deadline:
                raise AssertionError("fixture backend lifecycle timed out")
            if self.state == "connected" and not self.smoke_stopped:
                assert self.log.get_buffer().get_char_count() <= MAX_LOG
                assert not self.connect_button.get_sensitive()
                self.logs.append("proxy: TURN relay allocated: 192.0.2.1\n")
                self.tick()
                assert self.state == "connected", "background relay event changed connected state"
                self.smoke_session = self.session
                self.smoke_stopped = True
                self.stop()
            elif self.smoke_stopped and self.process is None:
                assert not self.smoke_session.exists()
                assert self.connect_button.get_sensitive()
                assert self.state == "disconnected"
                from auth import smoke_login
                if not smoke_login(self.window, self.smoke_webkit_finished):
                    if os.environ.get("VKTURN_REQUIRE_WEBKIT_TEST") == "1":
                        raise AssertionError("WebKitGTK runtime required by this smoke run")
                    self.smoke_webkit_finished(None, available=False)
                return False
        except Exception as error:
            self.smoke_fail(error)
            return False
        return True

    def smoke_webkit_finished(self, error, available=True):
        if error:
            self.smoke_fail(error)
            return
        if available:
            print("WebKitGTK fixture passed: isolated context, local HTML and HttpOnly cookie pair")
        else:
            print("WebKitGTK optional test skipped: runtime missing")
        session = Path(tempfile.mkdtemp(dir=self.config, prefix="exit-fixture-"))
        script = "import pathlib,time,sys\np=pathlib.Path(sys.argv[1])\nwhile not p.exists(): time.sleep(.05)\n"
        self.start_process([sys.executable, "-c", script, str(session / "stop")], session)
        self.smoke_exit_session = session
        self.request_exit()

    def smoke_fail(self, error):
        print("GTK smoke failed:", error, file=sys.stderr)
        self.exit_code = 1
        if self.process:
            self.process.kill()
            self.process.wait(timeout=3)
        self.quit()


def main():
    parser = argparse.ArgumentParser(description="VK TURN Proxy Desktop for Linux")
    parser.add_argument("--autostart", action="store_true")
    parser.add_argument("--smoke-test", action="store_true")
    parser.add_argument("--capture-ui", "--capture-dir", dest="capture_ui")
    args = parser.parse_args()
    app = Desktop(args)
    if args.smoke_test:
        GLib.timeout_add_seconds(30, lambda: app.smoke_fail("native GTK smoke timeout"))
    code = app.run([sys.argv[0]])
    if app.test_home:
        app.test_home.cleanup()
    return app.exit_code or (1 if args.smoke_test and not app.smoke_complete else code)


if __name__ == "__main__":
    raise SystemExit(main())
