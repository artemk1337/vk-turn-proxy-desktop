# SPDX-License-Identifier: MIT
"""Small state helpers shared by the GTK interface and its tests."""

import collections
import json
import os
from pathlib import Path
import threading
import time
import tempfile

from desktop_settings import (DEFAULT_LOG_LINES, DEFAULT_THEME, LOG_MAX_BYTES,
                              MAX_LOG_LINES, MIN_LOG_LINES, LANGUAGES, THEMES, system_language)

MAX_INPUT = 4 << 20
MAX_LOG_BYTES = LOG_MAX_BYTES
MAX_LOG = MAX_LOG_BYTES
PHASE_ORDER = {"": 0, "credentials": 1, "relay": 2, "handshake": 3, "connected": 4}


def connection_phase(line):
    if "DEFAULT ROUTE" in line and "tunnel" in line:
        return "connected"
    if "first connection up" in line:
        return "handshake"
    if "TURN relay allocated" in line:
        return "relay"
    return ""


class PendingLogs:
    def __init__(self):
        self.lock = threading.Lock()
        self.lines = collections.deque()
        self.size = 0
        self.dropped = 0
        self.phase = ""

    def append(self, line):
        line = line.encode("utf-8")[:8192].decode("utf-8", errors="ignore")
        with self.lock:
            phase = connection_phase(line)
            if PHASE_ORDER[phase] > PHASE_ORDER[self.phase]:
                self.phase = phase
            size = len(line.encode("utf-8"))
            while self.lines and (len(self.lines) >= 256 or self.size + size > 65536):
                self.size -= len(self.lines.popleft().encode("utf-8"))
                self.dropped += 1
            self.lines.append(line)
            self.size += size

    def drain(self):
        with self.lock:
            result = ("".join(self.lines), self.phase, self.dropped)
            self.lines.clear()
            self.size = self.dropped = 0
            self.phase = ""
            return result


def private_directory(path):
    path = Path(path)
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.is_symlink() or path.stat().st_uid != os.getuid():
        raise OSError("Application directory must belong to the current user")
    path.chmod(0o700)
    return path


def write_private(path, text):
    path = Path(path)
    if path.is_symlink():
        raise OSError("Private storage must not be a symbolic link")
    fd, temporary = tempfile.mkstemp(prefix=".private-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            output.write(text)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def normalize_log_limit(value):
    if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
        return DEFAULT_LOG_LINES
    return min(MAX_LOG_LINES, max(MIN_LOG_LINES, value))


def log_trim_offset(text, limit):
    offset = 0
    for _ in range(max(0, text.count("\n") - normalize_log_limit(limit))):
        offset = text.index("\n", offset) + 1
    encoded = text[offset:].encode("utf-8")
    if len(encoded) > MAX_LOG_BYTES:
        suffix = encoded[-MAX_LOG_BYTES:].decode("utf-8", errors="ignore")
        end = suffix.find("\n")
        if 0 <= end < len(suffix) - 1:
            suffix = suffix[end + 1:]
        elif suffix.startswith("\n"):
            suffix = suffix[1:]
        offset = len(text) - len(suffix)
    return offset


def load_preferences(path):
    try:
        value = json.loads(Path(path).read_text(encoding="utf-8"))
        if isinstance(value, dict):
            return {"language": value.get("language") if value.get("language") in LANGUAGES else system_language(),
                    "theme": value.get("theme") if value.get("theme") in THEMES else DEFAULT_THEME,
                    "logLines": normalize_log_limit(value.get("logLines"))}
    except (OSError, ValueError):
        pass
    return {"language": system_language(), "theme": DEFAULT_THEME, "logLines": DEFAULT_LOG_LINES}


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
