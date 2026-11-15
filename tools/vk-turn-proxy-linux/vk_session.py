# SPDX-License-Identifier: MIT
"""Persistent VK account controls and asynchronous shared-backend checks."""

import json
from pathlib import Path
import subprocess
import tempfile
import threading

import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, GLib

from desktop_settings import VK_CHECK_SECONDS
from model import MAX_INPUT, write_private
from profile_store import load_vk_session, save_vk_session


class VKSession:
    def __init__(self, parent, config, backend_path, tr, smoke_test=False):
        self.parent = parent
        self.config = Path(config)
        self.path = self.config / "vk-session.txt"
        self.backend_path = backend_path
        self.tr = tr
        self.smoke_test = smoke_test
        self.cookies = ""
        self.status = "unavailable"
        self.checking = False
        self.saving = False
        self.closed = False
        self.generation = 0
        self.processes = set()
        self.lock = threading.Lock()
        self.widget = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.label = Gtk.Label(xalign=0)
        self.widget.pack_start(self.label, False, False, 0)
        controls = Gtk.Box(spacing=8)
        self.login_button = Gtk.Button()
        self.login_button.connect("clicked", self.website_login)
        self.cookies_button = Gtk.Button()
        self.cookies_button.connect("clicked", self.manage_cookies)
        controls.pack_start(self.login_button, False, False, 0)
        controls.pack_start(self.cookies_button, False, False, 0)
        self.widget.pack_start(controls, False, False, 0)
        try:
            self.cookies = load_vk_session(self.path)
        except (OSError, ValueError):
            GLib.idle_add(self.error, "VK session could not be loaded. Sign in again.")
        self.apply_language()
        self.timer = GLib.timeout_add_seconds(VK_CHECK_SECONDS, self.check)
        GLib.idle_add(self.check_once)

    def apply_language(self):
        message = "VK signed out"
        if self.cookies:
            message = {"valid": "VK connected", "rejected": "VK session expired"}.get(
                self.status, "VK check unavailable")
        if self.saving or (self.checking and self.cookies):
            message = "Checking VK…"
        self.label.set_text(self.tr(message))
        self.login_button.set_label(self.tr("Sign in again" if self.status == "rejected" else "Sign in to VK"))
        self.cookies_button.set_label(self.tr("Cookies"))
        self.login_button.set_sensitive(not self.saving)
        self.cookies_button.set_sensitive(not self.saving)

    def error(self, message):
        if self.closed:
            return False
        dialog = Gtk.MessageDialog(transient_for=self.parent, modal=True,
                                   message_type=Gtk.MessageType.ERROR,
                                   buttons=Gtk.ButtonsType.OK, text="VK TURN Proxy")
        dialog.format_secondary_text(self.tr(message))
        dialog.run()
        dialog.destroy()
        return False

    def command(self, mode, text):
        with tempfile.TemporaryDirectory(prefix="vk-check-", dir=self.config) as folder:
            path = Path(folder) / "cookies.txt"
            write_private(path, text)
            with self.lock:
                if self.closed:
                    return "unavailable"
                process = subprocess.Popen([str(self.backend_path()), mode, "-vk-cookie-file", str(path)],
                                           stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                           stderr=subprocess.DEVNULL)
                self.processes.add(process)
            try:
                output, _ = process.communicate(timeout=25)
                if process.returncode or len(output) > 4096:
                    return "unavailable"
                result = json.loads(output)
                status = result.get("status") if isinstance(result, dict) else None
                return status if status in ("valid", "rejected", "unavailable") else "unavailable"
            except (OSError, ValueError, subprocess.TimeoutExpired):
                process.kill()
                process.communicate()
                return "unavailable"
            finally:
                with self.lock:
                    self.processes.discard(process)

    def set_cookies(self, text, completed=None):
        """Validate and persist an account without blocking the GTK main loop."""
        if self.closed or self.saving:
            return False
        if len(text.encode("utf-8")) > MAX_INPUT:
            self.error("The input is larger than 4 MiB.")
            return False
        text = text.strip()
        if not text:
            try:
                save_vk_session(self.path, "")
            except (OSError, ValueError):
                self.error("VK session could not be saved.")
                return False
            self.generation += 1
            self.cookies = ""
            self.status = "unavailable"
            self.apply_language()
            if completed:
                completed(True)
            return True
        self.generation += 1
        generation = self.generation
        self.saving = True
        self.apply_language()

        def validate():
            try:
                valid = self.command("-validate-vk-cookies", text) == "valid"
            except (OSError, ValueError):
                valid = False
            GLib.idle_add(self.saved, generation, text, valid, completed)
        threading.Thread(target=validate, name="vk-session-save", daemon=True).start()
        return True

    def saved(self, generation, text, valid, completed):
        if self.closed or generation != self.generation:
            return False
        self.saving = False
        message = "VK cookies are invalid or expired. Sign in again."
        if valid:
            try:
                save_vk_session(self.path, text)
                self.cookies = text
                self.status = "unavailable"
            except (OSError, ValueError):
                valid = False
                message = "VK session could not be saved."
        self.apply_language()
        if completed:
            completed(valid)
        if valid:
            self.check_once()
        else:
            self.error(message)
        return False

    def check_once(self):
        self.check()
        return False

    def check(self):
        if self.closed:
            return False
        if self.smoke_test or self.checking or self.saving or not self.cookies:
            return True
        generation, text = self.generation, self.cookies
        self.checking = True
        self.apply_language()

        def worker():
            try:
                status = self.command("-check-vk-session", text)
            except (OSError, ValueError):
                status = "unavailable"
            GLib.idle_add(self.checked, generation, status)
        threading.Thread(target=worker, name="vk-session-check", daemon=True).start()
        return True

    def checked(self, generation, status):
        if self.closed:
            return False
        self.checking = False
        if generation != self.generation:
            self.check_once()
        else:
            self.status = status
        self.apply_language()
        return False

    def website_login(self, _button):
        from auth import login
        login(self.parent, self.tr, self.set_cookies)

    def manage_cookies(self, _button):
        dialog = Gtk.Dialog(title=self.tr("Cookies"), transient_for=self.parent, modal=True)
        dialog.set_default_size(550, 330)
        dialog.add_button(self.tr("Cancel"), Gtk.ResponseType.CANCEL)
        dialog.add_button(self.tr("Save"), Gtk.ResponseType.OK)
        editor = Gtk.TextView()
        editor.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        editor.set_monospace(True)
        editor.get_buffer().set_text(self.cookies)
        scroll = Gtk.ScrolledWindow()
        scroll.add(editor)
        content = dialog.get_content_area()
        content.set_spacing(8)
        content.set_border_width(12)
        content.pack_start(scroll, True, True, 0)
        controls = Gtk.Box(spacing=8)
        import_button = Gtk.Button(label=self.tr("Import file…"))
        import_button.connect("clicked", lambda _: self.import_cookies(dialog, editor))
        clear_button = Gtk.Button(label=self.tr("Clear session"))
        clear_button.connect("clicked", lambda _: editor.get_buffer().set_text(""))
        controls.pack_start(import_button, False, False, 0)
        controls.pack_start(clear_button, False, False, 0)
        content.pack_start(controls, False, False, 0)
        dialog.show_all()

        def saved(ok):
            if ok:
                dialog.destroy()
            else:
                dialog.set_sensitive(True)

        def response(_dialog, result):
            if result != Gtk.ResponseType.OK:
                dialog.destroy()
                return
            buffer = editor.get_buffer()
            text = buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), True)
            dialog.set_sensitive(False)
            if not self.set_cookies(text, saved):
                dialog.set_sensitive(True)

        dialog.connect("response", response)

    def import_cookies(self, parent, editor):
        chooser = Gtk.FileChooserDialog(title=self.tr("Open file"), transient_for=parent,
                                        action=Gtk.FileChooserAction.OPEN)
        chooser.add_buttons(self.tr("Cancel"), Gtk.ResponseType.CANCEL, self.tr("Open"), Gtk.ResponseType.OK)
        if chooser.run() == Gtk.ResponseType.OK:
            try:
                with open(chooser.get_filename(), "rb") as source:
                    data = source.read(MAX_INPUT + 1)
                if len(data) > MAX_INPUT:
                    raise ValueError("The input is larger than 4 MiB.")
                editor.get_buffer().set_text(data.decode("utf-8-sig"))
            except (OSError, ValueError, UnicodeError):
                self.error("VK cookie file could not be read or exceeds 4 MiB.")
        chooser.destroy()

    def close(self):
        self.closed = True
        GLib.source_remove(self.timer)
        with self.lock:
            for process in self.processes:
                try:
                    process.terminate()
                except ProcessLookupError:
                    pass
