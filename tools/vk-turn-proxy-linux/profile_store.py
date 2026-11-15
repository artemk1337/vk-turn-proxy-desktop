# SPDX-License-Identifier: MIT
"""Private persistent profiles and the account shared by all profiles."""

import json
import os
from pathlib import Path
import secrets

from model import MAX_INPUT, private_directory, write_private

MAX_PROFILE_STORE = 32 << 20
PROFILE_TEXT_FIELDS = ("id", "name", "mode", "profileText", "wgConfig", "vkLink",
                       "endpoint", "serverName")


def new_profile_id():
    return secrets.token_hex(16)


def validate_profile_store(store):
    if not isinstance(store, dict) or not isinstance(store.get("profiles"), list):
        raise ValueError("Invalid profile storage.")
    ids = set()
    for profile in store["profiles"]:
        if not isinstance(profile, dict):
            raise ValueError("Invalid saved profile.")
        if any(not isinstance(profile.get(field, ""), str) for field in PROFILE_TEXT_FIELDS):
            raise ValueError("Invalid saved profile.")
        profile_id = profile.get("id", "")
        if not profile_id or profile_id in ids or not profile.get("name", "").strip():
            raise ValueError("Invalid saved profile.")
        if profile.get("mode") not in ("link", "wireguard"):
            raise ValueError("Unknown profile type.")
        if not isinstance(profile.get("useSRTP", True), bool):
            raise ValueError("Invalid profile transport.")
        ids.add(profile_id)
    active = store.get("activeID", "")
    if not isinstance(active, str) or (active and active not in ids):
        raise ValueError("Selected profile does not exist.")


def read_private(path, limit):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as source:
        if os.fstat(source.fileno()).st_uid != os.getuid():
            raise OSError("Private storage must belong to the current user")
        os.fchmod(source.fileno(), 0o600)
        data = source.read(limit + 1)
    if not data or len(data) > limit:
        raise ValueError("Invalid private storage size.")
    return data.decode("utf-8")


def load_profile_store(path):
    try:
        store = json.loads(read_private(path, MAX_PROFILE_STORE))
    except FileNotFoundError:
        return {"profiles": [], "activeID": ""}
    validate_profile_store(store)
    return store


def save_profile_store(path, store):
    validate_profile_store(store)
    text = json.dumps(store, ensure_ascii=False)
    if len(text.encode("utf-8")) > MAX_PROFILE_STORE:
        raise ValueError("Profile storage exceeds 32 MiB.")
    private_directory(Path(path).parent)
    write_private(path, text)


def load_vk_session(path):
    try:
        return read_private(path, MAX_INPUT)
    except FileNotFoundError:
        return ""


def save_vk_session(path, text):
    if not text:
        try:
            Path(path).unlink()
        except FileNotFoundError:
            pass
        return
    if len(text.encode("utf-8")) > MAX_INPUT:
        raise ValueError("VK session exceeds 4 MiB.")
    private_directory(Path(path).parent)
    write_private(path, text)
