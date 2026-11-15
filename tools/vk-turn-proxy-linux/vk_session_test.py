# SPDX-License-Identifier: MIT
"""VK session request lifecycle without a browser or network."""

from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch

try:
    from vk_session import VKSession
except ImportError:
    VKSession = None


@unittest.skipIf(VKSession is None, "GTK runtime is unavailable")
class VKSessionTest(unittest.TestCase):
    def session(self, folder):
        value = object.__new__(VKSession)
        value.config = Path(folder)
        value.path = value.config / "vk-session.txt"
        value.closed = False
        value.generation = 2
        value.cookies = "current account"
        value.status = "valid"
        value.checking = True
        value.saving = False
        value.processes = set()
        value.lock = threading.Lock()
        value.apply_language = Mock()
        value.check_once = Mock()
        value.error = Mock()
        return value

    def test_previous_account_response_rechecks_current_account(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            self.assertFalse(session.checked(1, "rejected"))
            self.assertEqual(session.status, "valid")
            self.assertFalse(session.checking)
            session.check_once.assert_called_once()

    def test_closed_session_ignores_pending_result(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            session.closed = True
            session.checked(2, "rejected")
            self.assertEqual(session.status, "valid")
            session.apply_language.assert_not_called()

    def test_invalid_replacement_preserves_current_session(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            callback = Mock()
            session.saved(2, "invalid replacement", False, callback)
            self.assertEqual(session.cookies, "current account")
            self.assertEqual(session.status, "valid")
            self.assertFalse(session.path.exists())
            callback.assert_called_once_with(False)

    def test_failed_storage_preserves_current_account(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            callback = Mock()
            with patch("vk_session.save_vk_session", side_effect=OSError("fixture")):
                session.saved(2, "new account", True, callback)
            self.assertEqual(session.cookies, "current account")
            callback.assert_called_once_with(False)

    def test_command_passes_private_file_and_removes_it(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            session.backend_path = lambda: Path("/fixture/backend")
            process = Mock(returncode=0)
            process.communicate.return_value = (b'{"status":"valid"}', None)
            paths = []

            def launch(args, **kwargs):
                path = Path(args[-1])
                paths.append(path)
                self.assertEqual(path.read_text(), "secret cookies")
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(args[1:3], ["-check-vk-session", "-vk-cookie-file"])
                self.assertNotIn("secret cookies", args)
                return process

            with patch("vk_session.subprocess.Popen", side_effect=launch):
                self.assertEqual(session.command("-check-vk-session", "secret cookies"), "valid")
            self.assertFalse(paths[0].exists())
            self.assertFalse(session.processes)

    def test_timeout_terminates_request_and_removes_private_file(self):
        with tempfile.TemporaryDirectory() as folder:
            session = self.session(folder)
            session.backend_path = lambda: Path("/fixture/backend")
            process = Mock(returncode=0)
            process.communicate.side_effect = [subprocess.TimeoutExpired("fixture", 25), (b"", None)]
            with patch("vk_session.subprocess.Popen", return_value=process):
                self.assertEqual(session.command("-check-vk-session", "secret"), "unavailable")
            process.kill.assert_called_once()
            self.assertFalse(list(Path(folder).iterdir()))
            self.assertFalse(session.processes)


if __name__ == "__main__":
    unittest.main()
