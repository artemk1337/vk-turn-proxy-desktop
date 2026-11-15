# SPDX-License-Identifier: MIT
"""Saved profile selection and editing controls."""

from pathlib import Path

import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk

from profile_dialog import edit_profile
from profile_store import load_profile_store, save_profile_store


class ProfilePanel:
    def __init__(self, parent, config, tr, validate, on_change=None):
        self.parent = parent
        self.tr = tr
        self.validate = validate
        self.on_change = on_change
        self.path = Path(config) / "profiles.json"
        self.running = False
        self.updating = False
        self.load_error = ""
        self.store = {"profiles": [], "activeID": ""}
        try:
            self.store = load_profile_store(self.path)
        except (OSError, ValueError) as error:
            self.load_error = str(error)

        self.widget = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
        self.title = Gtk.Label(xalign=0)
        self.title.get_style_context().add_class("section-title")
        self.widget.pack_start(self.title, False, False, 0)
        self.choice = Gtk.ComboBoxText()
        self.choice.set_hexpand(True)
        self.choice.connect("changed", self.selection_changed)
        self.widget.pack_start(self.choice, False, False, 0)
        controls = Gtk.Box(spacing=8)
        self.add_button = Gtk.Button()
        self.edit_button = Gtk.Button()
        self.remove_button = Gtk.Button()
        self.add_button.connect("clicked", lambda _: self.edit())
        self.edit_button.connect("clicked", lambda _: self.edit(self.selected()))
        self.remove_button.connect("clicked", lambda _: self.remove())
        for button in (self.add_button, self.edit_button, self.remove_button):
            controls.pack_start(button, False, False, 0)
        self.widget.pack_start(controls, False, False, 0)
        self.mode_label = Gtk.Label(xalign=0)
        self.mode_label.get_style_context().add_class("hint")
        self.widget.pack_start(self.mode_label, False, False, 0)
        self.error_label = Gtk.Label(xalign=0)
        self.error_label.set_line_wrap(True)
        self.error_label.get_style_context().add_class("error")
        self.widget.pack_start(self.error_label, False, False, 0)
        self.refresh()
        self.apply_language()

    def selected(self):
        return next((dict(profile) for profile in self.store["profiles"]
                     if profile["id"] == self.store["activeID"]), None)

    def refresh(self):
        self.updating = True
        self.choice.remove_all()
        for profile in self.store["profiles"]:
            self.choice.append(profile["id"], profile["name"])
        self.choice.set_active_id(self.store["activeID"])
        self.updating = False
        self.set_running(self.running)
        self.update_mode()
        self.error_label.set_text(self.load_error)

    def update_mode(self):
        selected = self.selected()
        text = "No saved profiles"
        if selected:
            text = "WireGuard + VK call" if selected["mode"] == "wireguard" else "Profile link"
        self.mode_label.set_text(self.tr(text))

    def apply_language(self):
        for widget, label in ((self.title, "Connection profile"), (self.add_button, "Add profile"),
                              (self.edit_button, "Edit"), (self.remove_button, "Remove")):
            widget.set_label(self.tr(label))
        self.update_mode()

    def set_running(self, running):
        self.running = running
        enabled = not running and not self.load_error
        self.choice.set_sensitive(enabled)
        self.add_button.set_sensitive(enabled)
        selected = self.selected() is not None
        self.edit_button.set_sensitive(enabled and selected)
        self.remove_button.set_sensitive(enabled and selected)

    def persist(self, store):
        if self.load_error:
            raise ValueError(self.load_error)
        save_profile_store(self.path, store)
        self.store = store
        self.refresh()
        if self.on_change:
            self.on_change()

    def save(self, profile):
        self.validate(profile)
        profiles = [dict(item) for item in self.store["profiles"]]
        index = next((i for i, item in enumerate(profiles) if item["id"] == profile["id"]), len(profiles))
        if index == len(profiles):
            profiles.append(dict(profile))
        else:
            profiles[index] = dict(profile)
        self.persist({"profiles": profiles, "activeID": profile["id"]})

    def select(self, profile_id):
        if not any(profile["id"] == profile_id for profile in self.store["profiles"]):
            raise ValueError(self.tr("Selected profile does not exist."))
        self.persist({"profiles": self.store["profiles"], "activeID": profile_id})

    def selection_changed(self, choice):
        if self.updating:
            return
        profile_id = choice.get_active_id()
        if profile_id:
            try:
                self.select(profile_id)
            except (OSError, ValueError) as error:
                self.refresh()
                self.error_label.set_text(str(error))

    def edit(self, profile=None):
        edit_profile(self.parent, self.tr, profile, self.save)

    def remove(self):
        selected = self.selected()
        if selected:
            profiles = [item for item in self.store["profiles"] if item["id"] != selected["id"]]
            try:
                self.persist({"profiles": profiles, "activeID": profiles[0]["id"] if profiles else ""})
            except (OSError, ValueError) as error:
                self.error_label.set_text(str(error))
