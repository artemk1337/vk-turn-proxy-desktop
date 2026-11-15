# SPDX-License-Identifier: MIT

import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import desktop_settings


class DesktopSettingsTests(unittest.TestCase):
    def test_packaged_settings_use_the_shared_contract(self):
        source = Path(desktop_settings.__file__).resolve()
        contract = source.parent.parent.parent / "pkg" / "desktop-settings" / "settings.json"
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            shutil.copyfile(source, directory / "desktop_settings.py")
            shutil.copyfile(contract, directory / "desktop-settings.json")
            spec = importlib.util.spec_from_file_location("packaged_settings", directory / "desktop_settings.py")
            packaged = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(packaged)
        self.assertEqual(packaged.SETTINGS, json.loads(contract.read_text()))
        self.assertEqual(packaged.DEFAULT_LOG_LINES, 1000)
        self.assertEqual(packaged.MIN_LOG_LINES, 100)
        self.assertEqual(packaged.MAX_LOG_LINES, 10000)
        self.assertEqual(packaged.LOG_MAX_BYTES, 4 << 20)
        self.assertEqual(packaged.VK_CHECK_SECONDS, 300)
        self.assertEqual(packaged.DEFAULT_TRANSPORT, "srtp")

    def test_system_language(self):
        for language, expected in (("ru_RU", "ru"), ("en_US", "en"), ("de_DE", "en"), (None, "en")):
            with self.subTest(language=language), patch("desktop_settings.locale.getlocale", return_value=(language, "UTF-8")):
                self.assertEqual(desktop_settings.system_language(), expected)


if __name__ == "__main__":
    unittest.main()
