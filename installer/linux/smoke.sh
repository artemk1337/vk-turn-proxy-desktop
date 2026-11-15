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
install -d -m 755 "$capture_dir"
runtime_dir=$(mktemp -d /tmp/vkturn-runtime.XXXXXX)
trap 'rm -rf "$runtime_dir"' EXIT
chown "$user:$user" "$runtime_dir"
chmod 700 "$runtime_dir"
dpkg -i "$package"
/usr/bin/vk-turn-proxy-console -version
timeout --kill-after=5s 60s runuser -u "$user" -- env NO_AT_BRIDGE=1 XDG_RUNTIME_DIR="$runtime_dir" xvfb-run -a dbus-run-session -- \
    /usr/bin/vk-turn-proxy-desktop --smoke-test
install -d -m 700 -o "$user" -g "$user" "$home_dir/.config/vk-turn-proxy-desktop"
printf '%s\n' 'preserve user settings' > "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
chown "$user:$user" "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
dpkg --remove vk-turn-proxy-desktop
test -f "$home_dir/.config/vk-turn-proxy-desktop/package-smoke.txt"
test ! -e /usr/bin/vk-turn-proxy-desktop
test ! -e /usr/bin/vk-turn-proxy-console
test ! -e /usr/lib/vk-turn-proxy-desktop/vk-turn-proxy-desktop
printf '%s\n' 'Linux package, GUI startup and uninstall checks passed' > "$capture_dir/package-smoke.txt"
cat "$capture_dir/package-smoke.txt"
