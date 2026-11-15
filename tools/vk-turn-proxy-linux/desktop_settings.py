# SPDX-License-Identifier: MIT
"""Desktop defaults shared with the Windows client."""

import json
import locale
from pathlib import Path

_directory = Path(__file__).resolve().parent
_path = _directory / "desktop-settings.json"
if not _path.is_file():
    _path = _directory.parent.parent / "pkg" / "desktop-settings" / "settings.json"
SETTINGS = json.loads(_path.read_text(encoding="utf-8"))

LANGUAGES = tuple(SETTINGS["languages"])
THEMES = tuple(SETTINGS["themes"])
DEFAULT_THEME = SETTINGS["defaultTheme"]
DEFAULT_LOG_LINES = SETTINGS["logLines"]["default"]
MIN_LOG_LINES = SETTINGS["logLines"]["min"]
MAX_LOG_LINES = SETTINGS["logLines"]["max"]
LOG_MAX_BYTES = SETTINGS["logMaxBytes"]
VK_CHECK_SECONDS = SETTINGS["vkCheckSeconds"]
DEFAULT_TRANSPORT = SETTINGS["defaultTransport"]


def system_language():
    language = locale.getlocale()[0] or ""
    language = language.split("_", 1)[0].split("-", 1)[0].lower()
    return language if language in LANGUAGES else SETTINGS["defaultLanguage"]
