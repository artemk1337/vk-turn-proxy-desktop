# macOS packages

Follow the root [AGENTS.md](../../AGENTS.md).

- `package.sh <version> <output-directory>` builds Intel and Apple Silicon application ZIPs on macOS with the Apple SDK. The bundle targets macOS 12 or later.
- Keep the GUI and console together in `VK TURN Proxy.app/Contents/MacOS`. The GUI locates its console beside its own executable.
- The bundle identifier is `io.github.artemk1337.vkturnproxy`. Keep the Info.plist release version aligned with the console version and archive names. Build with `-buildvcs=false` to match the metadata policy of Docker packages.
- Generate `AppIcon.icns` from the canonical `assets/logo.png` with the standard 1x/2x iconset sizes. Include it in `Contents/Resources` and set `CFBundleIconFile` before signing.
- Include application licenses inside `Contents/Resources`, and README files with their English and Russian screenshot assets in the portable package.
- Clear extended attributes on the generated bundle before signing to remove resource forks and Finder metadata. Sign both executables and the completed bundle with an ad-hoc signature. Developer ID signing and notarization require a separate configured identity.
- Packaging performs compilation, signing, ZIP creation and checksum generation. Keep verification workflows separate from this build-only entry point.
