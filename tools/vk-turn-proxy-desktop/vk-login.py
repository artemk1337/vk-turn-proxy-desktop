# SPDX-License-Identifier: MIT
"""Optional VK website login in an isolated WebKitGTK context."""

import gi

import time
def export_vk_cookies(cookies, now=None):
    now = time.time() if now is None else now
    selected = {}
    for cookie in cookies:
        name, value = cookie["name"], cookie["value"]
        domain = cookie["domain"].lower().lstrip(".")
        expires = cookie.get("expires", 0)
        valid_domain = ((name == "remixsid" and domain in ("vk.com", "vk.ru")) or
                        (name == "p" and domain in ("login.vk.com", "login.vk.ru")))
        if not valid_domain or not value or any(ord(c) <= 32 or c in ";\x7f" for c in value):
            continue
        if expires and expires <= now:
            continue
        previous = selected.get(name)
        if previous is None or (previous.get("expires", 0) and
                                (not expires or expires > previous["expires"])):
            selected[name] = cookie
    if len(selected) != 2:
        return ""
    lines = ["# Netscape HTTP Cookie File"]
    for name in ("remixsid", "p"):
        c = selected[name]
        lines.append("#HttpOnly_%s\tTRUE\t/\tTRUE\t%d\t%s\t%s" %
                     (c["domain"], c.get("expires", 0), name, c["value"]))
    return "\n".join(lines) + "\n"



def webkit_runtime():
    for version in ("4.1", "4.0"):
        try:
            gi.require_version("WebKit2", version)
            from gi.repository import WebKit2
            return WebKit2
        except (ValueError, ImportError):
            continue
    return None


def cookie_records(cookies):
    records = []
    for cookie in cookies:
        expiry = cookie.get_expires()
        records.append({"name": cookie.get_name(), "value": cookie.get_value(),
                        "domain": cookie.get_domain(),
                        "expires": (expiry.to_unix() if hasattr(expiry, "to_unix") else
                                    expiry.to_time_t()) if expiry else 0})
    return records


def login(parent, translate, completed):
    from gi.repository import Gtk, GLib

    WebKit2 = webkit_runtime()
    if WebKit2 is None:
        message = Gtk.MessageDialog(transient_for=parent, modal=True,
                                    message_type=Gtk.MessageType.INFO,
                                    buttons=Gtk.ButtonsType.OK,
                                    text=translate("Install WebKitGTK to sign in through VK."))
        message.format_secondary_text("Debian / Ubuntu: gir1.2-webkit2-4.1")
        message.run()
        message.destroy()
        return

    dialog = Gtk.Dialog(title=translate("Sign in to VK"), transient_for=parent, modal=True)
    dialog.set_default_size(650, 760)
    hint = Gtk.Label(label=translate("Complete VK login. This window closes when cookies are ready."))
    hint.set_margin_top(12)
    hint.set_margin_bottom(12)
    dialog.get_content_area().pack_start(hint, False, False, 0)
    data = WebKit2.WebsiteDataManager.new_ephemeral()
    context = WebKit2.WebContext.new_with_website_data_manager(data)
    view = WebKit2.WebView.new_with_context(context)
    dialog.get_content_area().pack_start(view, True, True, 0)
    manager = context.get_cookie_manager()
    state = {"closed": False, "pending": False}

    def read_finished(source, result, _data=None):
        state["pending"] = False
        if state["closed"]:
            return
        try:
            text = export_vk_cookies(cookie_records(source.get_all_cookies_finish(result)))
            if text:
                completed(text)
                dialog.response(Gtk.ResponseType.OK)
        except GLib.Error:
            hint.set_text(translate("Could not read VK cookies. Try again or import a file."))

    def poll(*_args):
        if state["closed"]:
            return False
        if not state["pending"]:
            state["pending"] = True
            manager.get_all_cookies(None, read_finished, None)
        return True

    timer = GLib.timeout_add_seconds(1, poll)
    view.load_uri("https://vk.ru/")
    dialog.show_all()
    dialog.run()
    state["closed"] = True
    GLib.source_remove(timer)
    view.stop_loading()
    manager.delete_all_cookies()
    dialog.destroy()



if __name__ == "__main__":
    import os
    import sys
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk
    if webkit_runtime() is None:
        raise SystemExit(2)
    def save(text):
        fd = os.open(sys.argv[1], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(text)
    translations = {
        "Install WebKitGTK to sign in through VK.": "Для входа в VK установите WebKitGTK.",
        "Sign in to VK": "Вход в VK",
        "Complete VK login. This window closes when cookies are ready.": "Войдите в VK. Окно закроется после получения cookies.",
        "Could not read VK cookies. Try again or import a file.": "Не удалось прочитать cookies VK. Повторите попытку или импортируйте файл.",
    }
    language = sys.argv[2] if len(sys.argv) > 2 else "en"
    login(None, lambda text: translations.get(text, text) if language == "ru" else text, save)
