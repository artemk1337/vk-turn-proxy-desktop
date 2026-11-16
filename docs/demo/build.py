#!/usr/bin/env python3
"""Build the browser demo from the current desktop frontend."""

import argparse
import shutil
from pathlib import Path


def build(output):
    root = Path(__file__).resolve().parents[2]
    frontend = root / "tools/vk-turn-proxy-desktop/frontend"
    output.mkdir(parents=True, exist_ok=True)
    for source in frontend.iterdir():
        if source.name == "AGENTS.md":
            continue
        target = output / source.name
        if source.is_dir():
            shutil.copytree(source, target, dirs_exist_ok=True)
        else:
            shutil.copy2(source, target)
    for name in ("demo.js", "demo.css"):
        shutil.copy2(root / "docs/demo" / name, output / name)
    shutil.copy2(root / "pkg/desktop-settings/settings.json", output / "settings.json")
    index = output / "index.html"
    html = index.read_text()
    html = html.replace('<link rel="stylesheet" href="routing.css" />',
                        '<link rel="stylesheet" href="routing.css" />\n    <link rel="stylesheet" href="demo.css" />')
    html = html.replace('<script type="module" src="app.js"></script>',
                        '<script type="module">await import("./demo.js"); await import("./app.js");</script>')
    index.write_text(html)
    (output / ".nojekyll").touch()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    build(parser.parse_args().output.resolve())
