# SPDX-License-Identifier: MIT
"""Optional VK website login in an isolated WebKitGTK context."""

import gi

from model import export_vk_cookies


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


def smoke_login(parent, completed):
    """Exercise the actual optional browser using local HTML and fixture cookies."""
    from gi.repository import Gtk

    WebKit2 = webkit_runtime()
    if WebKit2 is None:
        return False
    from gi.repository import Soup

    data = WebKit2.WebsiteDataManager.new_ephemeral()
    context = WebKit2.WebContext.new_with_website_data_manager(data)
    manager = context.get_cookie_manager()
    window = Gtk.Window(transient_for=parent, title="WebKitGTK fixture")
    window.set_default_size(400, 240)
    view = WebKit2.WebView.new_with_context(context)
    window.add(view)
    state = {"loaded": False, "cookies": False, "closed": False, "added": 0}

    def finish(error=None):
        if state["closed"]:
            return
        if error is None and not (state["loaded"] and state["cookies"]):
            return
        state["closed"] = True
        manager.delete_all_cookies()
        view.stop_loading()
        window.destroy()
        completed(error)

    def cookies_ready(source, result, _data=None):
        try:
            text = export_vk_cookies(cookie_records(source.get_all_cookies_finish(result)))
            if "\tremixsid\tfixture-session\n" not in text or "\tp\tfixture-token\n" not in text:
                raise AssertionError("WebKitGTK did not return the HttpOnly cookie pair")
            state["cookies"] = True
            finish()
        except Exception as error:
            finish(error)

    def added(source, result, _data=None):
        try:
            source.add_cookie_finish(result)
            state["added"] += 1
            if state["added"] == 2:
                source.get_all_cookies(None, cookies_ready, None)
        except Exception as error:
            finish(error)

    def loaded(_view, event):
        if event == WebKit2.LoadEvent.FINISHED:
            state["loaded"] = True
            finish()

    view.connect("load-changed", loaded)
    window.show_all()
    view.load_html("<html><body><h2>VK TURN Proxy</h2><p>Local browser fixture</p></body></html>", "https://vk.ru/")
    for name, value, domain in (("remixsid", "fixture-session", ".vk.ru"), ("p", "fixture-token", ".login.vk.ru")):
        cookie = Soup.Cookie.new(name, value, domain, "/", 3600)
        cookie.set_http_only(True)
        cookie.set_secure(True)
        manager.add_cookie(cookie, None, added, None)
    return True
