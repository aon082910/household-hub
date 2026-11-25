"""Draws a made-up receipt (no real store or amounts) for the receipt-reader test.

Run: python tools/make_test_receipt.py
"""
import os

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "internal", "core", "testdata", "receipt.png")

LINES = [
    "CORNER MARKET",
    "123 MAIN ST",
    "11/20/2026  14:32",
    "",
    "MILK 2%              3.49",
    "BREAD                2.99",
    "EGGS DOZEN           4.19",
    "SUBTOTAL            10.67",
    "TAX                  0.75",
    "TOTAL               11.42",
    "VISA ****0000       11.42",
]


def main():
    try:
        font = ImageFont.truetype("consola.ttf", 28)
    except OSError:
        font = ImageFont.load_default()
    img = Image.new("L", (560, 60 + 42 * len(LINES)), 255)
    d = ImageDraw.Draw(img)
    for i, line in enumerate(LINES):
        d.text((30, 30 + 42 * i), line, fill=0, font=font)
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    img.save(OUT)
    print("wrote", OUT)


if __name__ == "__main__":
    main()
