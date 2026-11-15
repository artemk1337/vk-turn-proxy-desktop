# SPDX-License-Identifier: MIT
"""Prepare console arguments with connection secrets in private files."""

from model import MAX_INPUT, write_private


def profile_arguments(profile, directory):
    manual = profile["mode"] == "wireguard"
    raw = profile["wgConfig" if manual else "profileText"].strip()
    if not raw:
        raise ValueError("Add a connection profile first.")
    if len(raw.encode("utf-8")) > MAX_INPUT:
        raise ValueError("The input is larger than 4 MiB.")
    path = directory / "profile.txt"
    write_private(path, raw)
    if not manual:
        args = ["-config", str(path)]
        if profile["serverName"].strip():
            args += ["-server", profile["serverName"].strip()]
        return args
    link = profile["vkLink"].strip()
    if not link:
        raise ValueError("Add a WireGuard configuration and VK call link.")
    link_path = directory / "vk-link.txt"
    write_private(link_path, link)
    args = ["-wg-config", str(path), "-vk-link-file", str(link_path),
            "-server-transport", "srtp" if profile["useSRTP"] else "dtls"]
    if profile["endpoint"].strip():
        args += ["-peer", profile["endpoint"].strip()]
    return args
