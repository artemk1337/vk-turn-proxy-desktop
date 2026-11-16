#!/bin/sh
set -eu

version=${1:?release version is required}
out_dir=${2:?output directory is required}
package_version=${version#v}
case "$package_version" in
    ''|*[!0-9A-Za-z.+-]*) echo "Invalid release version" >&2; exit 1 ;;
esac
if [ "$(uname -s)" != Darwin ]; then
    echo "macOS packaging requires macOS and the Apple SDK" >&2
    exit 1
fi

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
mkdir -p "$out_dir"
out_dir=$(CDPATH= cd -- "$out_dir" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
cd "$repo_root"

export MACOSX_DEPLOYMENT_TARGET=12.0
export SDKROOT=$(xcrun --sdk macosx --show-sdk-path)

iconset="$work/AppIcon.iconset"
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" assets/logo.png --out "$iconset/icon_${size}x${size}.png" >/dev/null
    retina=$((size * 2))
    sips -z "$retina" "$retina" assets/logo.png --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$work/AppIcon.icns"

for arch in amd64 arm64; do
    case "$arch" in
        amd64) clang_arch=x86_64 ;;
        arm64) clang_arch=arm64 ;;
    esac
    name="vk-turn-proxy-macos-${version}-${arch}"
    package="$work/$name"
    app="$package/VK TURN Proxy.app"
    binaries="$app/Contents/MacOS"
    resources="$app/Contents/Resources"
    mkdir -p "$binaries" "$resources" "$package/docs/images" "$resources/docs/images"

    GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 CC="clang -arch $clang_arch" \
      CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=12.0" \
      CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=12.0 -framework UniformTypeIdentifiers" \
      go build -buildvcs=false -tags desktop,production -trimpath -ldflags '-s -w' \
        -o "$binaries/vk-turn-proxy-desktop" ./tools/vk-turn-proxy-desktop
    GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -buildvcs=false -trimpath \
      -ldflags "-s -w -X main.version=${version}" \
      -o "$binaries/vk-turn-proxy-console" ./tools/vk-turn-proxy-console

    cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>io.github.artemk1337.vkturnproxy</string>
  <key>CFBundleName</key><string>VK TURN Proxy</string>
  <key>CFBundleDisplayName</key><string>VK TURN Proxy</string>
  <key>CFBundleExecutable</key><string>vk-turn-proxy-desktop</string>
  <key>CFBundleIconFile</key><string>AppIcon.icns</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${package_version}</string>
  <key>CFBundleVersion</key><string>${package_version}</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF

    install -m 644 "$work/AppIcon.icns" "$resources/AppIcon.icns"
    for file in README.md README.en.md README.ru.md LICENSE NOTICE.md; do
        install -m 644 "$file" "$package/$file"
        install -m 644 "$file" "$resources/$file"
    done
    install -m 644 docs/*.md "$package/docs/"
    install -m 644 docs/*.md "$resources/docs/"
    for language in en ru; do
        image="docs/images/main-window-${language}.png"
        install -m 644 "$image" "$package/$image"
        install -m 644 "$image" "$resources/$image"
        image="docs/images/interface-demo-${language}.gif"
        install -m 644 "$image" "$package/$image"
        install -m 644 "$image" "$resources/$image"
    done
    install -m 644 tools/vk-turn-proxy-desktop/frontend/fonts/LICENSE.txt "$resources/FONT-LICENSE.txt"
    for module in github.com/wailsapp/wails/v2 github.com/energye/systray github.com/godbus/dbus/v5; do
        cat "$(go list -m -f '{{.Dir}}' "$module")/LICENSE" >> "$resources/DESKTOP-LICENSES.txt"
    done

    xattr -cr "$app"
    codesign --force --sign - --timestamp=none "$binaries/vk-turn-proxy-console"
    codesign --force --sign - --timestamp=none "$binaries/vk-turn-proxy-desktop"
    codesign --force --sign - --timestamp=none "$app"
    ditto -c -k --sequesterRsrc --keepParent "$package" "$out_dir/$name.zip"
done

cd "$out_dir"
shasum -a 256 "vk-turn-proxy-macos-${version}-amd64.zip" \
    "vk-turn-proxy-macos-${version}-arm64.zip" > SHA256SUMS-macos.txt
