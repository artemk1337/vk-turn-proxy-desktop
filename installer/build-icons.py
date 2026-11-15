#!/usr/bin/env python3
"""Regenerate desktop branding from the canonical logo with Pillow."""

from pathlib import Path

from PIL import Image


root = Path(__file__).resolve().parent.parent
desktop = root / "tools/vk-turn-proxy-desktop"
with Image.open(root / "assets/logo.png") as source:
    logo = source.convert("RGBA")
    logo.save(root / "assets/logo.ico", sizes=[(size, size) for size in (16, 24, 32, 48, 64, 128, 256)])
    icon = logo.resize((256, 256), Image.Resampling.LANCZOS)
    icon.save(root / "assets/logo-256.png")
    icon.save(desktop / "frontend/logo.png")
    logo.resize((32, 32), Image.Resampling.LANCZOS).save(desktop / "tray-logo.png")
