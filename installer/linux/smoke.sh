#!/bin/sh
# Run in a disposable Debian/Ubuntu environment as root.
set -eu

package=${1:?path to a native-architecture .deb is required}
capture_dir=${2:-/tmp/vk-turn-proxy-linux-ui}
user=vktest
if ! id "$user" >/dev/null 2>&1; then
    useradd --create-home --shell /bin/sh "$user"
fi
home_dir=$(getent passwd "$user" | cut -d: -f6)
install -d -m 755 -o "$user" -g "$user" "$capture_dir"
dpkg -i "$package"
/usr/bin/vk-turn-proxy-console -version
python3 -m py_compile /usr/lib/vk-turn-proxy-desktop/*.py
# Root's compile cache is test output, not an installed package file.
rm -rf /usr/lib/vk-turn-proxy-desktop/__pycache__
timeout --kill-after=5s 60s runuser -u "$user" -- env NO_AT_BRIDGE=1 VKTURN_REQUIRE_WEBKIT_TEST=1 dbus-run-session -- xvfb-run -a \
    /usr/bin/vk-turn-proxy-desktop --smoke-test --capture-ui="$capture_dir"
install -d -m 700 -o "$user" -g "$user" "$home_dir/.config/vk-turn-proxy-desktop"
printf '%s\n' 'preserve user settings' > "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
chown "$user:$user" "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
dpkg --remove vk-turn-proxy-desktop
test -f "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
test ! -e /usr/bin/vk-turn-proxy-desktop
test ! -e /usr/bin/vk-turn-proxy-console
test ! -e /usr/lib/vk-turn-proxy-desktop/main.py
printf '%s\n' 'Linux package, GUI startup and uninstall checks passed'
