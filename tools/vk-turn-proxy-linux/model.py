# SPDX-License-Identifier: MIT
"""Small state helpers shared by the GTK interface and its tests."""

import collections
import json
import os
from pathlib import Path
import threading
import time

MAX_INPUT = 4 << 20
MAX_LOG = 128 << 10
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
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as output:
        os.fchmod(output.fileno(), 0o600)
        output.write(text)


def load_preferences(path):
    try:
        value = json.loads(Path(path).read_text(encoding="utf-8"))
        if isinstance(value, dict):
            return {"language": "ru" if value.get("language") == "ru" else "en",
                    "theme": "dark" if value.get("theme") == "dark" else "light"}
    except (OSError, ValueError):
        pass
    return {"language": "en", "theme": "light"}


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
