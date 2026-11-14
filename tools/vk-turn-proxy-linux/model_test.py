# SPDX-License-Identifier: MIT

import json
import os
from pathlib import Path
import stat
import tempfile
import unittest

from model import PendingLogs, export_vk_cookies, load_preferences, private_directory, write_private


class LogTests(unittest.TestCase):
    def test_overload_preserves_connection_phase_and_bounds_bytes(self):
        logs = PendingLogs()
        logs.append("DEFAULT ROUTE -> the tunnel\n")
        for _ in range(5000):
            logs.append("Я" * 9000)
        text, phase, dropped = logs.drain()
        self.assertEqual(phase, "connected")
        self.assertLessEqual(len(text.encode("utf-8")), 65536)
        self.assertGreater(dropped, 0)
        self.assertEqual(logs.drain(), ("", "", 0))

    def test_later_relay_allocation_does_not_erase_connected_phase(self):
        logs = PendingLogs()
        logs.append("DEFAULT ROUTE -> the tunnel\n")
        logs.append("proxy: TURN relay allocated: 192.0.2.1\n")
        self.assertEqual(logs.drain()[1], "connected")


class CookieTests(unittest.TestCase):
    def test_only_valid_vk_pair_is_exported(self):
        cookies = [
            {"name": "remixsid", "value": "expired", "domain": ".vk.ru", "expires": 90},
            {"name": "remixsid", "value": "fresh", "domain": ".vk.ru", "expires": 150},
            {"name": "p", "value": "token", "domain": ".login.vk.ru", "expires": 0},
            {"name": "remixsid", "value": "foreign", "domain": "evil-vk.ru", "expires": 1000},
        ]
        text = export_vk_cookies(cookies, now=100)
        self.assertIn("\tremixsid\tfresh\n", text)
        self.assertIn("\tp\ttoken\n", text)
        self.assertNotIn("expired", text)
        self.assertNotIn("foreign", text)

    def test_partial_or_invalid_pair_is_not_exported(self):
        self.assertEqual(export_vk_cookies([{"name": "remixsid", "value": "one", "domain": ".vk.ru"}]), "")
        self.assertEqual(export_vk_cookies([
            {"name": "remixsid", "value": "one\ninjected", "domain": ".vk.ru"},
            {"name": "p", "value": "two", "domain": ".login.vk.ru"},
        ]), "")


class PreferencesTests(unittest.TestCase):
    def test_private_storage_and_invalid_preferences(self):
        with tempfile.TemporaryDirectory() as root:
            directory = private_directory(Path(root) / "app")
            path = directory / "preferences.json"
            write_private(path, json.dumps({"language": "ru", "theme": "dark", "privateKey": "ignored"}))
            self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(load_preferences(path), {"language": "ru", "theme": "dark"})
            write_private(path, "[]")
            self.assertEqual(load_preferences(path), {"language": "en", "theme": "light"})

    def test_secret_writes_refuse_symlinks(self):
        with tempfile.TemporaryDirectory() as root:
            original = Path(root) / "original"
            original.write_text("untouched")
            link = Path(root) / "link"
            os.symlink(original, link)
            with self.assertRaises(OSError):
                write_private(link, "secret")
            self.assertEqual(original.read_text(), "untouched")


if __name__ == "__main__":
    unittest.main()
