#!/bin/sh
set -eu

version=${1:?release version is required}
bin_dir=${2:?compiled binary directory is required}
out_dir=${3:?output directory is required}
package_version=${version#v}
mkdir -p "$out_dir"
out_dir=$(cd "$out_dir" && pwd)
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0}

for arch in amd64 arm64; do
    name="vk-turn-proxy-linux-${version}-${arch}"
    work=$(mktemp -d)
    portable="$work/$name"
    package="$work/package"
    mkdir -p "$portable" "$package/DEBIAN" "$package/usr/lib/vk-turn-proxy-desktop" \
        "$package/usr/bin" "$package/usr/share/applications" \
        "$package/usr/share/icons/hicolor/256x256/apps" "$package/usr/share/doc/vk-turn-proxy-desktop" \
        "$portable/docs/images" "$package/usr/share/doc/vk-turn-proxy-desktop/docs/images"

    install -m 644 tools/vk-turn-proxy-desktop/frontend/fonts/LICENSE.txt "$portable/FONT-LICENSE.txt"
    for module in github.com/wailsapp/wails/v2 github.com/energye/systray github.com/godbus/dbus/v5; do
        cat "$(go list -m -f '{{.Dir}}' "$module")/LICENSE" >> "$portable/DESKTOP-LICENSES.txt"
    done
    install -m 755 "${bin_dir}/${arch}/vk-turn-proxy-console" "$portable/vk-turn-proxy-console"
    install -m 755 "${bin_dir}/${arch}/vk-turn-proxy-desktop" "$portable/vk-turn-proxy-desktop"
    install -m 644 assets/logo-256.png "$portable/vk-turn-proxy-desktop.png"
    install -m 644 docs/images/main-window-*.png "$portable/docs/images/"
    install -m 644 docs/images/main-window-*.png "$package/usr/share/doc/vk-turn-proxy-desktop/docs/images/"
    for file in README.md README.ru.md LICENSE NOTICE.md; do
        install -m 644 "$file" "$portable/$file"
    done

    install -m 644 "$portable/vk-turn-proxy-desktop.png" \
        "$package/usr/lib/vk-turn-proxy-desktop/"
    install -m 755 "$portable/vk-turn-proxy-console" "$portable/vk-turn-proxy-desktop" "$package/usr/lib/vk-turn-proxy-desktop/"
    install -m 755 installer/linux/vk-turn-proxy-desktop "$package/usr/bin/vk-turn-proxy-desktop"
    ln -s ../lib/vk-turn-proxy-desktop/vk-turn-proxy-console "$package/usr/bin/vk-turn-proxy-console"
    install -m 644 installer/linux/vk-turn-proxy-desktop.desktop "$package/usr/share/applications/io.github.artemk1337.vkturnproxy.desktop"
    install -m 644 "$portable/vk-turn-proxy-desktop.png" "$package/usr/share/icons/hicolor/256x256/apps/"
    for file in README.md README.ru.md LICENSE NOTICE.md; do
        install -m 644 "$file" "$package/usr/share/doc/vk-turn-proxy-desktop/$file"
    done

    install -m 644 "$portable/DESKTOP-LICENSES.txt" "$portable/FONT-LICENSE.txt" "$package/usr/share/doc/vk-turn-proxy-desktop/"

    size=$(du -sk "$package/usr" | cut -f1)
    cat > "$package/DEBIAN/control" <<EOF
Package: vk-turn-proxy-desktop
Version: ${package_version}
Section: net
Priority: optional
Architecture: ${arch}
Maintainer: artemk1337 <51025000+artemk1337@users.noreply.github.com>
Installed-Size: ${size}
Depends: libgtk-3-0, libwebkit2gtk-4.1-0, iproute2, pkexec | policykit-1, polkitd | policykit-1, ca-certificates
Recommends: python3 (>= 3.9), python3-gi, gir1.2-gtk-3.0, gir1.2-webkit2-4.1
Homepage: https://github.com/artemk1337/vk-turn-proxy-desktop
Description: WireGuard client over VK TURN relays
 Shared Wails desktop interface and terminal setup wizard.
EOF
    dpkg-deb --root-owner-group -Zxz --build "$package" "$out_dir/${name}.deb"
    tar --sort=name --mtime="@${SOURCE_DATE_EPOCH}" --owner=0 --group=0 --numeric-owner \
        -C "$work" -cf - "$name" | gzip -n > "$out_dir/${name}.tar.gz"
    rm -rf "$work"
done
cd "$out_dir"
sha256sum *.deb *.tar.gz > SHA256SUMS-linux.txt
