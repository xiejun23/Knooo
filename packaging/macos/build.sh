#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
export MACOSX_DEPLOYMENT_TARGET=13.0
version=$(cat VERSION)
mkdir -p build
stage=$(mktemp -d "$PWD/build/mac.XXXXXX")
trap 'rm -rf "$stage"' EXIT
app="$stage/image/Knooo.app"
resources="$app/Contents/Resources"
icons="$stage/Knooo.iconset"
mkdir -p "$app/Contents/MacOS" "$resources" "$icons"

for arch in arm64 amd64; do
  CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" CC=clang go build -trimpath -buildvcs=false \
    -o "$stage/knooo-$arch" ./cmd/knooo
done
exe="$app/Contents/MacOS/Knooo"
lipo -create "$stage/knooo-arm64" "$stage/knooo-amd64" -output "$exe"
lipo "$exe" -verify_arch arm64 x86_64
sed "s/>VERSION</>${version%%[-+]*}</g" packaging/macos/Info.plist > "$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"
for size in 16 32 128 256 512; do
  for scale in 1 2; do
    suffix=""
    if [[ "$scale" == 2 ]]; then suffix="@2x"; fi
    sips -z "$((size * scale))" "$((size * scale))" ui/src/assets/eagle.png \
      --out "$icons/icon_${size}x${size}${suffix}.png" >/dev/null
  done
done
iconutil -c icns "$icons" -o "$resources/Knooo.icns"
codesign --force --sign - "$app"
codesign --verify --strict --all-architectures "$app"

ln -s /Applications "$stage/image/Applications"
hdiutil create -volname Knooo -fs HFS+ -format UDZO -srcfolder "$stage/image" "$stage/Knooo.dmg"
hdiutil verify "$stage/Knooo.dmg"
mv -f "$stage/Knooo.dmg" build/Knooo-macOS-universal.dmg
