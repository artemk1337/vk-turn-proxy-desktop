# SPDX-License-Identifier: MIT

import copy
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch

from model import write_private
from profile_store import (load_profile_store, load_vk_session, new_profile_id,
                           save_profile_store, save_vk_session)


class ProfileStoreTests(unittest.TestCase):
    def setUp(self):
        self.profile = {"id": new_profile_id(), "name": "Home", "mode": "wireguard",
                        "profileText": "", "wgConfig": "PrivateKey = secret", "vkLink": "https://vk.ru/call/join/test",
                        "endpoint": "192.0.2.1:51820", "serverName": "relay", "useSRTP": True}
        self.store = {"profiles": [self.profile], "activeID": self.profile["id"]}

    def test_round_trip_preserves_profiles_selection_and_private_permissions(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "app" / "profiles.json"
            self.assertEqual(load_profile_store(path), {"profiles": [], "activeID": ""})
            save_profile_store(path, self.store)
            self.assertEqual(load_profile_store(path), self.store)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)

    def test_invalid_updates_preserve_existing_store(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profiles.json"
            save_profile_store(path, self.store)
            invalid = copy.deepcopy(self.store)
            invalid["profiles"].append(invalid["profiles"][0])
            with self.assertRaises(ValueError):
                save_profile_store(path, invalid)
            self.assertEqual(load_profile_store(path), self.store)
            invalid = copy.deepcopy(self.store)
            invalid["activeID"] = "missing"
            with self.assertRaises(ValueError):
                save_profile_store(path, invalid)

    def test_interrupted_atomic_write_keeps_previous_data(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profiles.json"
            save_profile_store(path, self.store)
            with patch("model.os.replace", side_effect=OSError("failed")):
                with self.assertRaises(OSError):
                    save_profile_store(path, {"profiles": [], "activeID": ""})
            self.assertEqual(load_profile_store(path), self.store)
            self.assertEqual(list(Path(root).iterdir()), [path])

    def test_corrupt_store_is_reported(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profiles.json"
            for value in ("{", "[]", json.dumps({"profiles": [{}]})):
                write_private(path, value)
                with self.assertRaises(ValueError):
                    load_profile_store(path)

    def test_private_store_refuses_symlink(self):
        with tempfile.TemporaryDirectory() as root:
            target = Path(root) / "target"
            target.write_text("untouched")
            path = Path(root) / "profiles.json"
            os.symlink(target, path)
            with self.assertRaises(OSError):
                load_profile_store(path)
            with self.assertRaises(OSError):
                save_profile_store(path, self.store)
            self.assertEqual(target.read_text(), "untouched")


class VKSessionTests(unittest.TestCase):
    def test_session_is_separate_and_can_be_cleared(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "app" / "vk-auth.txt"
            self.assertEqual(load_vk_session(path), "")
            save_vk_session(path, "remixsid=secret; p=token")
            self.assertEqual(load_vk_session(path), "remixsid=secret; p=token")
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            save_vk_session(path, "")
            self.assertEqual(load_vk_session(path), "")
            save_vk_session(path, "")

    def test_oversized_session_preserves_existing_data(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "vk-auth.txt"
            save_vk_session(path, "remixsid=secret; p=token")
            with self.assertRaises(ValueError):
                save_vk_session(path, "x" * ((4 << 20) + 1))
            self.assertEqual(load_vk_session(path), "remixsid=secret; p=token")


if __name__ == "__main__":
    unittest.main()
