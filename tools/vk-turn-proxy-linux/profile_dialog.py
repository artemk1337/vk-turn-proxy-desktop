# SPDX-License-Identifier: MIT
"""Saved connection profile editor for the GTK client."""

from pathlib import Path
import uuid

import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk

from model import MAX_INPUT
from desktop_settings import DEFAULT_TRANSPORT


class ProfileDialog(Gtk.Dialog):
    def __init__(self, parent, tr, profile=None):
        super().__init__(title=tr("Connection profile"), transient_for=parent, modal=True)
        self.tr = tr
        profile = profile or {}
        self.profile_id = profile.get("id") or uuid.uuid4().hex
        self.set_default_size(600, 550)
        self.set_size_request(540, 480)
        self.add_buttons(tr("Cancel"), Gtk.ResponseType.CANCEL, tr("Save"), Gtk.ResponseType.OK)
        self.set_default_response(Gtk.ResponseType.OK)
        if parent.get_style_context().has_class("dark"):
            self.get_style_context().add_class("dark")
        page = self.get_content_area()
        page.set_spacing(10)
        page.set_border_width(16)
        page.pack_start(self.label("Profile name"), False, False, 0)
        self.name = self.entry(profile.get("name", ""))
        self.name.set_activates_default(True)
        page.pack_start(self.name, False, False, 0)

        self.mode = Gtk.ComboBoxText()
        self.mode.append("link", tr("Profile link"))
        self.mode.append("wireguard", tr("WireGuard + VK call"))
        self.mode.set_active_id(profile.get("mode", "link"))
        self.mode.connect("changed", lambda _: self.update_mode())
        page.pack_start(self.mode, False, False, 0)

        self.link_area = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.profile, editor = self.editor(profile.get("profileText", ""), 160)
        self.profile.set_tooltip_text("vkturnproxy://import?data=<base64 JSON>")
        self.link_area.pack_start(self.heading("Connection link", "Open file…", self.profile), False, False, 0)
        self.link_area.pack_start(editor, True, True, 0)
        self.link_area.pack_start(self.label("vkturnproxy://import?data=<base64 JSON>"), False, False, 0)
        page.pack_start(self.link_area, True, True, 0)

        self.manual_area = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        self.wireguard, editor = self.editor(profile.get("wgConfig", ""), 120)
        self.manual_area.pack_start(self.heading("WireGuard configuration", "Open .conf…", self.wireguard), False, False, 0)
        self.manual_area.pack_start(editor, True, True, 0)
        self.manual_area.pack_start(self.label("VK call link"), False, False, 0)
        self.call_link = self.entry(profile.get("vkLink", ""), "https://vk.ru/call/join/…")
        self.manual_area.pack_start(self.call_link, False, False, 0)
        grid = Gtk.Grid(column_spacing=12, row_spacing=6)
        grid.attach(self.label("TURN server (optional)"), 0, 0, 1, 1)
        grid.attach(self.label("Server transport"), 1, 0, 1, 1)
        self.endpoint = self.entry(profile.get("endpoint", ""), tr("Use Endpoint from the config"))
        self.endpoint.set_hexpand(True)
        self.transport = Gtk.ComboBoxText()
        self.transport.append("srtp", "SRTP")
        self.transport.append("dtls", "DTLS")
        self.transport.set_active_id("srtp" if profile.get("useSRTP", DEFAULT_TRANSPORT == "srtp") else "dtls")
        grid.attach(self.endpoint, 0, 1, 1, 1)
        grid.attach(self.transport, 1, 1, 1, 1)
        self.manual_area.pack_start(grid, False, False, 0)
        page.pack_start(self.manual_area, True, True, 0)

        page.pack_start(self.label("Server name"), False, False, 0)
        self.server_name = self.entry(profile.get("serverName", ""), tr("Default"))
        self.server_name.set_tooltip_text(tr("For JSON profiles containing several servers."))
        page.pack_start(self.server_name, False, False, 0)
        self.error_label = Gtk.Label(xalign=0)
        self.error_label.set_line_wrap(True)
        self.error_label.get_style_context().add_class("error")
        page.pack_start(self.error_label, False, False, 0)
        self.show_all()
        self.link_area.set_no_show_all(True)
        self.manual_area.set_no_show_all(True)
        self.update_mode()

    def label(self, text):
        return Gtk.Label(label=self.tr(text), xalign=0)

    @staticmethod
    def entry(text, placeholder=""):
        entry = Gtk.Entry()
        entry.set_text(text)
        entry.set_placeholder_text(placeholder)
        return entry

    @staticmethod
    def editor(text, height):
        editor = Gtk.TextView()
        editor.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        editor.set_monospace(True)
        editor.get_buffer().set_text(text)
        scroll = Gtk.ScrolledWindow()
        scroll.set_shadow_type(Gtk.ShadowType.IN)
        scroll.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
        scroll.set_min_content_height(height)
        scroll.add(editor)
        return editor, scroll

    def heading(self, title, button_text, target):
        row = Gtk.Box(spacing=8)
        row.pack_start(self.label(title), True, True, 0)
        button = Gtk.Button(label=self.tr(button_text))
        button.connect("clicked", lambda _: self.open_file(target))
        row.pack_end(button, False, False, 0)
        return row

    def update_mode(self):
        manual = self.mode.get_active_id() == "wireguard"
        self.link_area.set_visible(not manual)
        self.manual_area.set_visible(manual)

    @staticmethod
    def text(editor):
        buffer = editor.get_buffer()
        return buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), True)

    def candidate(self):
        candidate = {
            "id": self.profile_id,
            "name": self.name.get_text().strip(),
            "mode": self.mode.get_active_id(),
            "profileText": self.text(self.profile),
            "wgConfig": self.text(self.wireguard),
            "vkLink": self.call_link.get_text().strip(),
            "endpoint": self.endpoint.get_text().strip(),
            "serverName": self.server_name.get_text().strip(),
            "useSRTP": self.transport.get_active_id() == "srtp",
        }
        if not candidate["name"]:
            raise ValueError(self.tr("Enter a profile name."))
        if any(len(value.encode("utf-8")) > MAX_INPUT for value in candidate.values() if isinstance(value, str)):
            raise ValueError(self.tr("The input is larger than 4 MiB."))
        if candidate["mode"] == "link" and not candidate["profileText"].strip():
            raise ValueError(self.tr("Paste a connection profile first."))
        if candidate["mode"] == "wireguard" and (not candidate["wgConfig"].strip() or not candidate["vkLink"]):
            raise ValueError(self.tr("Add a WireGuard configuration and VK call link."))
        return candidate

    def open_file(self, target):
        dialog = Gtk.FileChooserDialog(title=self.tr("Open file"), transient_for=self,
                                       action=Gtk.FileChooserAction.OPEN)
        dialog.add_buttons(self.tr("Cancel"), Gtk.ResponseType.CANCEL, self.tr("Open"), Gtk.ResponseType.OK)
        try:
            if dialog.run() == Gtk.ResponseType.OK:
                with Path(dialog.get_filename()).open("rb") as source:
                    data = source.read(MAX_INPUT + 1)
                if len(data) > MAX_INPUT:
                    raise ValueError(self.tr("The input is larger than 4 MiB."))
                target.get_buffer().set_text(data.decode("utf-8-sig"))
                self.error_label.set_text("")
        except (OSError, ValueError, UnicodeError) as error:
            self.error_label.set_text(str(error))
        finally:
            dialog.destroy()


def edit_profile(parent, tr, profile=None, validate=None):
    dialog = ProfileDialog(parent, tr, profile)
    try:
        while dialog.run() == Gtk.ResponseType.OK:
            try:
                candidate = dialog.candidate()
                if validate:
                    validate(candidate)
                return candidate
            except (OSError, ValueError) as error:
                dialog.error_label.set_text(str(error))
        return None
    finally:
        dialog.destroy()
