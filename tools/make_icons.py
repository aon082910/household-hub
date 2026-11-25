"""Draws the Household Hub icon (same design as internal/web/ui/icon.svg) as PNG and ICO files.

Run: python -I tools/make_icons.py   (from the project folder)
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
UI = os.path.join(ROOT, "internal", "web", "ui")


def draw(size: int) -> Image.Image:
    s = size / 64.0
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle([0, 0, size - 1, size - 1], radius=14 * s, fill="#1f2a44")
    d.polygon([(32 * s, 11 * s), (9 * s, 30 * s), (15 * s, 30 * s), (15 * s, 51 * s), (49 * s, 51 * s), (49 * s, 30 * s), (55 * s, 30 * s)], fill="#4f7cff")
    d.rounded_rectangle([21 * s, 31 * s, 43 * s, 47 * s], radius=2 * s, fill="#ffffff")
    d.rounded_rectangle([21 * s, 31 * s, 43 * s, 36 * s], radius=2 * s, fill="#ff8a3d")
    for x in (24, 30, 36):
        d.rectangle([x * s, 39 * s, (x + 4) * s, 42 * s], fill="#1f2a44")
    try:
        font = ImageFont.truetype("segoeuib.ttf", max(8, int(11 * s)))
    except OSError:
        font = ImageFont.load_default()
    d.text((32 * s, 21.5 * s), "$", fill="#ffffff", font=font, anchor="mm")
    return img


def main() -> int:
    big = draw(512)
    big.save(os.path.join(UI, "icon-512.png"))
    draw(192).save(os.path.join(UI, "icon-192.png"))
    draw(256).save(os.path.join(ROOT, "icon.png"))
    draw(256).save(os.path.join(ROOT, "icon.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
    print("icons written")
    return 0


if __name__ == "__main__":
    sys.exit(main())
