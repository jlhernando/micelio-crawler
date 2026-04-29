#!/bin/bash
# Generate AppIcon.icns from the Micelio logo using macOS native tools.
# Requires: sips + iconutil (built into macOS), ImageMagick (for padding).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$SCRIPT_DIR/../../dashboard/public/icon-512.png"
ICONSET="$SCRIPT_DIR/AppIcon.iconset"
ICNS="$SCRIPT_DIR/AppIcon.icns"

if [ ! -f "$SRC" ]; then
    echo "Error: Source icon not found at $SRC"
    exit 1
fi

mkdir -p "$ICONSET"

# Pad to square (512x512) with matching background color, then upscale to 1024
magick "$SRC" -gravity center -background "#2a2a4a" -extent 512x512 "$SCRIPT_DIR/_tmp_512.png"
magick "$SCRIPT_DIR/_tmp_512.png" -resize 1024x1024 "$SCRIPT_DIR/_tmp_1024.png"

# Generate all required icon sizes
gen() { sips -z "$2" "$2" "$SCRIPT_DIR/_tmp_512.png" --out "$ICONSET/$1" >/dev/null 2>&1; }

gen "icon_16x16.png"      16
gen "icon_16x16@2x.png"   32
gen "icon_32x32.png"      32
gen "icon_32x32@2x.png"   64
gen "icon_128x128.png"    128
gen "icon_128x128@2x.png" 256
gen "icon_256x256.png"    256
gen "icon_256x256@2x.png" 512
gen "icon_512x512.png"    512
cp "$SCRIPT_DIR/_tmp_1024.png" "$ICONSET/icon_512x512@2x.png"

# Convert iconset to icns
iconutil -c icns "$ICONSET" -o "$ICNS"

# Cleanup temp files
rm -f "$SCRIPT_DIR/_tmp_512.png" "$SCRIPT_DIR/_tmp_1024.png"
rm "$ICONSET"/icon_*.png
rmdir "$ICONSET"

echo "Created $ICNS"
