#!/usr/bin/env python3
"""Regenerate the desktop icons from the dashboard's red star.

  assets/tray-template.png   macOS menu-bar template (black + alpha)
  assets/appicon.png         1024px app icon (window/dock fallback)
  build/darwin/icons.icns    the .app bundle icon

Needs Pillow; icns uses macOS `iconutil`. Run from desktop/:
  python3 scripts/make-icons.py
"""
import os, shutil, subprocess, sys, tempfile
from PIL import Image, ImageDraw

here = os.path.dirname(os.path.abspath(__file__))
root = os.path.dirname(here)
src = Image.open(os.path.join(root, "frontend", "antares.png")).convert("RGBA")
star = src.crop(src.getbbox())

# Tray: the silhouette in black, fitted to 44x44 (22pt @2x) with 2px air.
alpha = star.getchannel("A")
mask = Image.new("RGBA", star.size, (0, 0, 0, 0))
mask.putalpha(alpha)
side = 44
fit = mask.copy()
fit.thumbnail((side - 4, side - 4), Image.LANCZOS)
tray = Image.new("RGBA", (side, side), (0, 0, 0, 0))
tray.paste(fit, ((side - fit.width) // 2, (side - fit.height) // 2), fit)
tray.save(os.path.join(root, "assets", "tray-template.png"))

# App icon: the macOS grid (824px rounded square on 1024, radius ~185), the
# dashboard's near-black, the red star at ~80% of the square.
S, box, radius = 1024, 824, 185
icon = Image.new("RGBA", (S, S), (0, 0, 0, 0))
plate = Image.new("RGBA", (box, box), (0, 0, 0, 0))
ImageDraw.Draw(plate).rounded_rectangle((0, 0, box - 1, box - 1), radius=radius, fill=(13, 14, 16, 255), outline=(38, 41, 47, 255), width=4)
icon.paste(plate, ((S - box) // 2, (S - box) // 2), plate)
art = star.copy()
art.thumbnail((int(box * 0.8), int(box * 0.8)), Image.LANCZOS)
icon.paste(art, ((S - art.width) // 2, (S - art.height) // 2), art)
icon.save(os.path.join(root, "assets", "appicon.png"))

if sys.platform == "darwin" and shutil.which("iconutil"):
    with tempfile.TemporaryDirectory() as tmp:
        iconset = os.path.join(tmp, "icons.iconset")
        os.mkdir(iconset)
        for size in (16, 32, 128, 256, 512):
            for scale in (1, 2):
                px = size * scale
                name = f"icon_{size}x{size}{'@2x' if scale == 2 else ''}.png"
                icon.resize((px, px), Image.LANCZOS).save(os.path.join(iconset, name))
        subprocess.run(["iconutil", "-c", "icns", iconset, "-o", os.path.join(root, "build", "darwin", "icons.icns")], check=True)
print("icons written")
