#!/usr/bin/env python3
"""Draws the Mixtape OnionOS theme (theme/Mixtape) in Mixtape's palette and fonts.

Every image is drawn here from scratch, so the theme is Mixtape's own work
(MIT, like the app; fonts under the SIL OFL). Re-run after changing anything:

    python3 theme/generate.py

Needs Python 3 with Pillow. File names and sizes follow OnionOS's theme layout
(the Blueprint template); files left out (the key-test diagrams) fall back to
OnionOS's own.
"""

import json
import math
import os
import shutil
import sys

from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
OUT = os.path.join(HERE, "Mixtape")
SS = 4  # supersampling for smooth lines

# Mixtape's palette (gfx.go)
BG = (0x07, 0x08, 0x0A)
PANEL = (0x10, 0x12, 0x15)
PANEL2 = (0x17, 0x1A, 0x1E)
GRID = (0x22, 0x26, 0x2C)
AMBER = (0xFF, 0xB0, 0x00)
AMBER_DIM = (0x8A, 0x5E, 0x00)
AMBER_DK = (0x3A, 0x28, 0x00)
CYAN = (0x2E, 0xE6, 0xD6)
CYAN_DIM = (0x12, 0x5E, 0x58)
MAGENTA = (0xFF, 0x2E, 0x88)
RED = (0xFF, 0x3B, 0x30)
GREEN = (0x7D, 0xFF, 0x6A)
PAPER = (0xE8, 0xE2, 0xD0)
PAPER_DIM = (0x8C, 0x88, 0x7C)

FONT_TITLE = os.path.join(REPO, "assets", "title.ttf")  # Orbitron
FONT_BODY = os.path.join(REPO, "assets", "body.ttf")  # Share Tech Mono
FONT_LCD = os.path.join(REPO, "assets", "lcd.ttf")  # VT323


def hexc(c):
    return "#%02X%02X%02X" % c


def font(path, size):
    return ImageFont.truetype(path, size)


def rgba(c, a=255):
    return (c[0], c[1], c[2], a)


def mix(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


def save(img, rel):
    path = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    if img.mode == "RGBA" and rel.startswith("skin/extra/") and img.size == (640, 480):
        img = img.convert("RGB")
    img.save(path, optimize=True)


class Big:
    """A supersampled RGBA canvas; .done() returns it at its real size."""

    def __init__(self, w, h, fill=(0, 0, 0, 0)):
        self.w, self.h = w, h
        self.im = Image.new("RGBA", (w * SS, h * SS), fill)
        self.d = ImageDraw.Draw(self.im)

    def s(self, *v):
        return [x * SS for x in v]

    def rect(self, x, y, w, h, fill=None, outline=None, width=1):
        self.d.rectangle(self.s(x, y, x + w - 1 / SS, y + h - 1 / SS), fill=fill, outline=outline, width=int(width * SS))

    def line(self, pts, col, width=2):
        self.d.line([v * SS for p in pts for v in p], fill=col, width=int(width * SS), joint="curve")

    def circle(self, cx, cy, r, fill=None, outline=None, width=2):
        self.d.ellipse(self.s(cx - r, cy - r, cx + r, cy + r), fill=fill, outline=outline, width=int(width * SS))

    def arc(self, cx, cy, r, a0, a1, col, width=2):
        self.d.arc(self.s(cx - r, cy - r, cx + r, cy + r), a0, a1, fill=col, width=int(width * SS))

    def poly(self, pts, fill=None, outline=None, width=2):
        p = [(x * SS, y * SS) for x, y in pts]
        if fill:
            self.d.polygon(p, fill=fill)
        if outline:
            self.d.line(p + [p[0]], fill=outline, width=int(width * SS), joint="curve")

    def rrect(self, x, y, w, h, r, fill=None, outline=None, width=2):
        self.d.rounded_rectangle(self.s(x, y, x + w, y + h), r * SS, fill=fill, outline=outline, width=int(width * SS))

    def text(self, x, y, s, path, size, col, anchor="la"):
        self.d.text((x * SS, y * SS), s, font=font(path, size * SS), fill=col, anchor=anchor)

    def done(self):
        return self.im.resize((self.w, self.h), Image.LANCZOS)


def glow_text(img, xy, s, path, size, col, anchor="la", glow=0.55, radius=3):
    """Text with a soft phosphor glow, like Mixtape's TextGlow."""
    f = font(path, size)
    layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    ImageDraw.Draw(layer).text(xy, s, font=f, fill=rgba(col), anchor=anchor)
    halo = layer.filter(ImageFilter.GaussianBlur(radius))
    a = halo.getchannel("A").point(lambda v: int(v * glow))
    halo.putalpha(a)
    img.alpha_composite(halo)
    img.alpha_composite(layer)


def scanlines(img, strength=0.12):
    """Darken every other row, like Mixtape's CRT scanlines."""
    over = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(over)
    for y in range(1, img.size[1], 2):
        d.line([(0, y), (img.size[0], y)], fill=(0, 0, 0, int(255 * strength)))
    img.alpha_composite(over)


def stripes(d, x, y, w, t=2):
    for i, c in enumerate((AMBER, MAGENTA, CYAN)):
        d.rectangle([x, y + i * t, x + w - 1, y + (i + 1) * t - 1], fill=rgba(c))


def corners(d, x, y, w, h, l, col, t=1):
    """Corner brackets, like Mixtape's Corners."""
    c = rgba(col)
    x2, y2 = x + w - 1, y + h - 1
    for (px, py, dx, dy) in ((x, y, 1, 1), (x2, y, -1, 1), (x, y2, 1, -1), (x2, y2, -1, -1)):
        ex, ey = px + dx * (l - 1), py + dy * (l - 1)
        tx, ty = px + dx * (t - 1), py + dy * (t - 1)
        d.rectangle([min(px, ex), min(py, ty), max(px, ex), max(py, ty)], fill=c)
        d.rectangle([min(px, tx), min(py, ey), max(px, tx), max(py, ey)], fill=c)


# ---------------------------------------------------------------- glyphs
# Line-art icons drawn into a Big canvas inside the box (x, y, s, s).


def g_star(b, x, y, s, col, w):
    cx, cy, R, r = x + s / 2, y + s / 2 + s * 0.04, s * 0.5, s * 0.21
    pts = []
    for i in range(10):
        a = -math.pi / 2 + i * math.pi / 5
        rad = R if i % 2 == 0 else r
        pts.append((cx + rad * math.cos(a), cy + rad * math.sin(a)))
    b.poly(pts, outline=col, width=w)


def g_cassette(b, x, y, s, col, w):
    h = s * 0.66
    top = y + (s - h) / 2
    b.rrect(x, top, s, h, s * 0.05, outline=col, width=w)
    b.rect(x + s * 0.12, top + h * 0.14, s * 0.76, h * 0.42, outline=col, width=w)
    for cx in (x + s * 0.33, x + s * 0.67):
        b.circle(cx, top + h * 0.35, s * 0.08, outline=col, width=w)
    b.poly([(x + s * 0.22, top + h), (x + s * 0.3, top + h * 0.74), (x + s * 0.7, top + h * 0.74), (x + s * 0.78, top + h)], outline=col, width=w)


def g_apps(b, x, y, s, col, w):
    g = s * 0.12
    c = (s - g) / 2
    for i in range(2):
        for j in range(2):
            b.rrect(x + i * (c + g), y + j * (c + g), c, c, s * 0.06, outline=col, width=w)


def g_gear(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    pts = []
    teeth = 8
    for i in range(teeth * 4):
        a = i * 2 * math.pi / (teeth * 4)
        rad = s * (0.5 if (i % 4) in (1, 2) else 0.38)
        pts.append((cx + rad * math.cos(a), cy + rad * math.sin(a)))
    b.poly(pts, outline=col, width=w)
    b.circle(cx, cy, s * 0.15, outline=col, width=w)


def g_clock(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    b.circle(cx, cy, s * 0.48, outline=col, width=w)
    b.line([(cx, cy - s * 0.3), (cx, cy), (cx + s * 0.22, cy + s * 0.1)], col, w)


def g_invader(b, x, y, s, col, w):
    # a pixel space invader (RetroArch)
    rows = ["00100000100", "00010001000", "00111111100", "01101110110", "11111111111", "10111111101", "10100000101", "00011011000"]
    px = s / 11
    oy = y + (s - px * 8) / 2
    for j, row in enumerate(rows):
        for i, ch in enumerate(row):
            if ch == "1":
                b.rect(x + i * px, oy + j * px, px + 0.2, px + 0.2, fill=col)


def g_gamepad(b, x, y, s, col, w):
    h = s * 0.58
    top = y + (s - h) / 2
    b.rrect(x, top, s, h, h * 0.45, outline=col, width=w)
    cx, cy = x + s * 0.28, top + h / 2
    a = s * 0.11
    b.line([(cx - a, cy), (cx + a, cy)], col, w)
    b.line([(cx, cy - a), (cx, cy + a)], col, w)
    for dx, dy in ((0.68, -0.08), (0.78, 0.06)):
        b.circle(x + s * dx, cy + s * dy, s * 0.045, outline=col, width=w)


def g_power(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2 + s * 0.04
    b.arc(cx, cy, s * 0.42, -60, 240, col, w)
    b.line([(cx, y), (cx, cy)], col, w)


def g_sun(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    b.circle(cx, cy, s * 0.2, outline=col, width=w)
    for i in range(8):
        a = i * math.pi / 4
        b.line([(cx + s * 0.32 * math.cos(a), cy + s * 0.32 * math.sin(a)), (cx + s * 0.47 * math.cos(a), cy + s * 0.47 * math.sin(a))], col, w)


def g_info(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    b.circle(cx, cy, s * 0.46, outline=col, width=w)
    b.line([(cx, cy - s * 0.04), (cx, cy + s * 0.25)], col, w)
    b.circle(cx, cy - s * 0.2, w * 0.7, fill=col)


def g_reset(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    b.arc(cx, cy, s * 0.4, -200, 120, col, w)
    a = math.radians(-200)
    tx, ty = cx + s * 0.4 * math.cos(a), cy + s * 0.4 * math.sin(a)
    b.poly([(tx - s * 0.14, ty - s * 0.02), (tx + s * 0.1, ty - s * 0.12), (tx + s * 0.06, ty + s * 0.14)], fill=col)


def g_globe(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    r = s * 0.46
    b.circle(cx, cy, r, outline=col, width=w)
    b.d.ellipse([(cx - r * 0.45) * SS, (cy - r) * SS, (cx + r * 0.45) * SS, (cy + r) * SS], outline=col, width=int(w * SS))
    b.line([(cx - r, cy), (cx + r, cy)], col, w)
    b.line([(cx, cy - r), (cx, cy + r)], col, w)


def g_wifi(b, x, y, s, col, w, bars=3, dim=None):
    cx, cy = x + s / 2, y + s * 0.85
    for i in range(3):
        r = s * (0.25 + i * 0.22)
        b.arc(cx, cy, r, 225, 315, col if i < bars else (dim or col), w)
    b.circle(cx, cy, s * 0.07, fill=col if bars > 0 else (dim or col))


def g_lock(b, x, y, s, col, w):
    b.rrect(x + s * 0.18, y + s * 0.45, s * 0.64, s * 0.5, s * 0.06, outline=col, width=w)
    b.arc(x + s / 2, y + s * 0.45, s * 0.2, 180, 360, col, w)
    b.line([(x + s * 0.3, y + s * 0.45), (x + s * 0.3, y + s * 0.42)], col, w)


def g_check(b, x, y, s, col, w):
    b.circle(x + s / 2, y + s / 2, s * 0.46, outline=col, width=w)
    b.line([(x + s * 0.28, y + s * 0.52), (x + s * 0.44, y + s * 0.68), (x + s * 0.72, y + s * 0.36)], col, w)


def g_folder(b, x, y, s, col, w):
    b.poly([(x, y + s * 0.2), (x + s * 0.38, y + s * 0.2), (x + s * 0.46, y + s * 0.3), (x + s, y + s * 0.3), (x + s, y + s * 0.82), (x, y + s * 0.82)], outline=col, width=w)


def g_sd(b, x, y, s, col, w):
    b.poly([(x + s * 0.2, y), (x + s * 0.7, y), (x + s * 0.85, y + s * 0.18), (x + s * 0.85, y + s), (x + s * 0.2, y + s)], outline=col, width=w)
    for i in range(3):
        b.line([(x + s * (0.36 + i * 0.14), y + s * 0.12), (x + s * (0.36 + i * 0.14), y + s * 0.3)], col, w)


def g_speaker(b, x, y, s, col, w):
    b.poly([(x + s * 0.05, y + s * 0.36), (x + s * 0.25, y + s * 0.36), (x + s * 0.5, y + s * 0.14), (x + s * 0.5, y + s * 0.86), (x + s * 0.25, y + s * 0.64), (x + s * 0.05, y + s * 0.64)], outline=col, width=w)
    b.arc(x + s * 0.5, y + s / 2, s * 0.2, -50, 50, col, w)
    b.arc(x + s * 0.5, y + s / 2, s * 0.36, -50, 50, col, w)


def g_headphone(b, x, y, s, col, w):
    b.arc(x + s / 2, y + s * 0.55, s * 0.4, 180, 360, col, w)
    for cx in (x + s * 0.14, x + s * 0.72):
        b.rrect(cx, y + s * 0.52, s * 0.14, s * 0.36, s * 0.04, fill=col)


def g_wrench(b, x, y, s, col, w):
    b.line([(x + s * 0.22, y + s * 0.78), (x + s * 0.62, y + s * 0.38)], col, w * 1.8)
    b.arc(x + s * 0.7, y + s * 0.3, s * 0.2, 100, 350, col, w * 1.4)


def g_palette(b, x, y, s, col, w):
    cx, cy = x + s / 2, y + s / 2
    b.circle(cx, cy, s * 0.46, outline=col, width=w)
    for i, c in enumerate((AMBER, MAGENTA, CYAN, GREEN)):
        a = math.radians(200 + i * 45)
        b.circle(cx + s * 0.26 * math.cos(a), cy + s * 0.26 * math.sin(a), s * 0.08, fill=rgba(c))


def g_arrow(b, x, y, s, col, w, left=True):
    if left:
        b.line([(x + s * 0.65, y + s * 0.15), (x + s * 0.3, y + s / 2), (x + s * 0.65, y + s * 0.85)], col, w)
    else:
        b.line([(x + s * 0.35, y + s * 0.15), (x + s * 0.7, y + s / 2), (x + s * 0.35, y + s * 0.85)], col, w)


def icon(fn, size, col, w=None, pad=0.12, **kw):
    b = Big(size[0], size[1])
    s = min(size) * (1 - 2 * pad)
    x, y = (size[0] - s) / 2, (size[1] - s) / 2
    fn(b, x, y, s, rgba(col), w or max(1.6, min(size) / 22), **kw)
    return b.done()


# ---------------------------------------------------------------- pieces


def background():
    im = Image.new("RGBA", (640, 480), rgba(BG))
    d = ImageDraw.Draw(im)
    # header band (matches bg-title) and footer band (tips-bar-bg)
    d.rectangle([0, 0, 639, 53], fill=rgba(PANEL))
    stripes(d, 0, 54, 640)
    d.rectangle([0, 420, 639, 479], fill=rgba(PANEL))
    d.line([(0, 420), (639, 420)], fill=rgba(GRID))
    scanlines(im)
    return im


def title_bar():
    im = Image.new("RGBA", (640, 60), rgba(PANEL))
    d = ImageDraw.Draw(im)
    stripes(d, 0, 54, 640)
    scanlines(im)
    return im


def tips_bar():
    im = Image.new("RGBA", (640, 60), rgba(PANEL))
    d = ImageDraw.Draw(im)
    d.line([(0, 0), (639, 0)], fill=rgba(GRID))
    scanlines(im)
    return im


def logo():
    im = Image.new("RGBA", (148, 42), (0, 0, 0, 0))
    glow_text(im, (4, 22), "MIXTAPE", FONT_TITLE, 23, AMBER, anchor="lm", radius=3)
    return im


def tile(glyph, focused):
    w, h = 156, 278
    b = Big(w, h)
    if focused:
        b.rect(4, 4, w - 8, h - 8, fill=rgba(PANEL2, 245))
        b.rect(4, 4, w - 8, h - 8, outline=rgba(AMBER), width=2)
        b.rect(8, h - 30, w - 16, 3, fill=rgba(AMBER))
        b.rect(8, h - 27, w - 16, 3, fill=rgba(MAGENTA))
        b.rect(8, h - 24, w - 16, 3, fill=rgba(CYAN))
    else:
        b.rect(4, 4, w - 8, h - 8, fill=rgba(PANEL, 200))
        b.rect(4, 4, w - 8, h - 8, outline=rgba(GRID), width=1)
    im = b.done()
    d = ImageDraw.Draw(im)
    corners(d, 0, 0, w, h, 14, CYAN if focused else AMBER_DIM, 2)
    col = AMBER if focused else PAPER_DIM
    g = icon(glyph, (84, 84), col, w=3.2, pad=0.04)
    if focused:
        halo = g.filter(ImageFilter.GaussianBlur(4))
        halo.putalpha(halo.getchannel("A").point(lambda v: int(v * 0.6)))
        im.alpha_composite(halo, (36, 62))
    im.alpha_composite(g, (36, 62))
    return im


def list_row(h):
    im = Image.new("RGBA", (640, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    d.rectangle([6, 1, 633, h - 2], fill=rgba(AMBER_DK, 235))
    d.rectangle([6, 1, 633, h - 2], outline=rgba(AMBER))
    d.rectangle([6, 1, 10, h - 2], fill=rgba(CYAN))
    return im


def grid_item(focused):
    w, h = 155, 170
    im = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    if focused:
        d.rectangle([2, 2, w - 3, h - 3], fill=rgba(AMBER_DK, 230), outline=rgba(AMBER), width=2)
        corners(d, 0, 0, w, h, 12, CYAN, 2)
    else:
        d.rectangle([2, 2, w - 3, h - 3], fill=rgba(PANEL, 200), outline=rgba(GRID))
    return im


def panel(w, h, accent=AMBER):
    im = Image.new("RGBA", (w, h), rgba(PANEL))
    d = ImageDraw.Draw(im)
    d.rectangle([0, 0, w - 1, h - 1], outline=rgba(GRID))
    d.rectangle([0, 0, w - 1, 2], fill=rgba(accent))
    corners(d, 0, 0, w, h, 12, accent, 2)
    scanlines(im, 0.08)
    return im


def button_bg():
    im = Image.new("RGBA", (232, 68), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    d.rectangle([4, 8, 227, 59], fill=rgba(AMBER_DK), outline=rgba(AMBER), width=2)
    d.rectangle([4, 8, 9, 59], fill=rgba(CYAN))
    return im


def key_button(label, col, size=54):
    b = Big(size, size)
    b.rect(5, 5, size - 10, size - 10, fill=rgba(col))
    b.text(size / 2, size / 2, label, FONT_BODY, size * 0.6, rgba(BG), anchor="mm")
    return b.done()


def chip(label, w, h, col):
    b = Big(w, h)
    b.rect(2, 8, w - 4, h - 16, outline=rgba(col), width=2)
    b.text(w / 2, h / 2, label, FONT_BODY, 17, rgba(col), anchor="mm")
    return b.done()


def menu_a():
    im = Image.new("RGBA", (118, 48), (0, 0, 0, 0))
    im.alpha_composite(chip("MENU", 65, 48, AMBER), (0, 0))
    d = ImageDraw.Draw(im)
    d.text((72, 24), "+", font=font(FONT_BODY, 20), fill=rgba(PAPER_DIM), anchor="mm")
    im.alpha_composite(key_button("A", AMBER, 40), (78, 4))
    return im


def battery(level, charging=False):
    b = Big(48, 48)
    col = RED if level <= 0.2 and not charging else (CYAN if charging else PAPER_DIM)
    b.rect(6, 16, 32, 16, outline=rgba(col), width=2)
    b.rect(38, 21, 4, 6, fill=rgba(col))
    if level > 0:
        b.rect(9, 19, 26 * level, 10, fill=rgba(mix(BG, col, 0.75)))
    if charging:
        b.poly([(24, 13), (17, 25), (23, 25), (20, 35), (29, 22), (23, 22)], fill=rgba(AMBER))
    return b.done()


def wifi_icon(bars):
    b = Big(48, 48)
    for i in range(4):
        bh = 8 + i * 7
        col = CYAN if i < bars else GRID
        b.rect(9 + i * 8, 38 - bh, 5, bh, fill=rgba(col))
    return b.done()


def digits():
    im = Image.new("RGBA", (90, 14), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    f = font(FONT_LCD, 17)
    for i in range(10):
        d.text((i * 9 + 4.5, 7), str(i), font=f, fill=rgba(AMBER), anchor="mm")
    return im


def dots(active):
    im = Image.new("RGBA", (24, 14), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    if active:
        d.rectangle([3, 4, 20, 9], fill=rgba(AMBER))
    else:
        d.rectangle([3, 4, 20, 9], outline=rgba(GRID))
    return im


def noise_card(w, h, label="NO SIGNAL"):
    import random

    rnd = random.Random(7)
    im = Image.new("RGBA", (w, h), rgba(BG))
    px = im.load()
    for y in range(8, h - 8):
        for x in range(8, w - 8):
            v = rnd.randint(10, 60)
            px[x, y] = (v, v, v, 255)
    d = ImageDraw.Draw(im)
    d.rectangle([0, 0, w - 1, h - 1], outline=rgba(GRID))
    corners(d, 0, 0, w, h, 14, CYAN, 2)
    f = font(FONT_LCD, 34)
    tw = d.textlength(label, font=f)
    d.rectangle([w / 2 - tw / 2 - 10, h / 2 - 22, w / 2 + tw / 2 + 10, h / 2 + 18], fill=rgba(BG))
    d.text((w / 2, h / 2 - 2), label, font=f, fill=rgba(AMBER), anchor="mm")
    scanlines(im, 0.2)
    return im


def reel(d, cx, cy, r, angle, col, dim, tape):
    d.ellipse([cx - tape, cy - tape, cx + tape, cy + tape], fill=rgba((0x2A, 0x1E, 0x10)))
    d.ellipse([cx - r, cy - r, cx + r, cy + r], outline=rgba(col), width=3)
    d.ellipse([cx - r * 0.35, cy - r * 0.35, cx + r * 0.35, cy + r * 0.35], outline=rgba(col), width=2)
    for i in range(3):
        a = angle + i * 2 * math.pi / 3
        d.line([(cx + r * 0.38 * math.cos(a), cy + r * 0.38 * math.sin(a)), (cx + r * 0.92 * math.cos(a), cy + r * 0.92 * math.sin(a))], fill=rgba(col), width=3)


def cassette(im, x, y, w, h, label, sub, angle=0.0, progress=0.3):
    d = ImageDraw.Draw(im)
    d.rectangle([x, y, x + w - 1, y + h - 1], fill=rgba(PANEL2), outline=rgba(AMBER_DIM), width=3)
    for px, py in ((x + 12, y + 12), (x + w - 13, y + 12), (x + 12, y + h - 13), (x + w - 13, y + h - 13)):
        d.ellipse([px - 4, py - 4, px + 4, py + 4], fill=rgba(GRID))
    lx, ly, lw, lh = x + 24, y + 18, w - 48, int(h * 0.44)
    d.rectangle([lx, ly, lx + lw - 1, ly + lh - 1], fill=rgba(PAPER))
    sy = ly + lh - 20
    for i, c in enumerate((AMBER, MAGENTA, CYAN)):
        d.rectangle([lx, sy + i * 6, lx + lw - 1, sy + i * 6 + 5], fill=rgba(c))
    d.text((lx + 12, ly + 8), label, font=font(FONT_BODY, int(h * 0.12)), fill=(0, 0, 0, 255))
    d.text((lx + 12, ly + 8 + int(h * 0.14)), sub, font=font(FONT_BODY, int(h * 0.075)), fill=(0x55, 0x50, 0x44, 255))
    wy = ly + lh + 12
    wh = h - (wy - y) - 24
    wx, ww = x + w // 2 - int(w * 0.3), int(w * 0.6)
    d.rectangle([wx, wy, wx + ww - 1, wy + wh - 1], fill=rgba(BG), outline=rgba(GRID))
    r = max(8, int(wh * 0.3))
    cy = wy + wh // 2
    maxt = wh // 2 - 3
    reel(d, wx + ww // 4, cy, r, angle, AMBER, AMBER_DIM, r + int((maxt - r) * (1 - progress)))
    reel(d, wx + ww * 3 // 4, cy, r, angle, AMBER, AMBER_DIM, r + int((maxt - r) * progress))
    d.rectangle([x + w // 4, y + h - 16, x + w * 3 // 4, y + h - 3], fill=rgba(PANEL), outline=rgba(GRID))


def vu(d, x, y, w, h, segs, v):
    gap = 3
    sw = (w - gap * (segs - 1)) / segs
    lit = round(v * segs)
    for i in range(segs):
        t = i / segs
        col = RED if t > 0.85 else (AMBER if t > 0.65 else GREEN)
        sx = x + i * (sw + gap)
        if i < lit:
            d.rectangle([sx, y, sx + sw - 1, y + h - 1], fill=rgba(col))
            d.rectangle([sx, y, sx + sw - 1, y + 1], fill=rgba(mix(col, PAPER, 0.5)))
        else:
            d.rectangle([sx, y, sx + sw - 1, y + h - 1], fill=rgba(mix(BG, col, 0.14)))


def screen(title, sub, label="SIDE A", tape="COMMUNITY PORTS VOL.1", angle=0.0, progress=0.3, meter=None, footer=None):
    im = Image.new("RGBA", (640, 480), rgba(BG))
    d = ImageDraw.Draw(im)
    stripes(d, 0, 112, 640)
    glow_text(im, (320, 146), title, FONT_TITLE, 34, AMBER, anchor="mm", radius=4)
    d.text((320, 178), sub, font=font(FONT_BODY, 16), fill=rgba(CYAN), anchor="mm")
    cassette(im, 190, 200, 260, 140, label, tape, angle, progress)
    if meter is not None:
        vu(d, 170, 366, 300, 16, 20, meter)
    if footer:
        d.text((320, 452), footer, font=font(FONT_BODY, 14), fill=rgba(PAPER_DIM), anchor="mm")
    scanlines(im)
    return im


def lum(level):
    im = Image.new("RGBA", (126, 30), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    vu(d, 2, 8, 122, 14, 10, level / 10)
    return im


def toggle(on):
    b = Big(92, 44)
    if on:
        b.rect(4, 8, 84, 28, fill=rgba(AMBER_DK), outline=rgba(AMBER), width=2)
        b.rect(48, 12, 36, 20, fill=rgba(AMBER))
        b.text(26, 22, "ON", FONT_BODY, 16, rgba(AMBER), anchor="mm")
    else:
        b.rect(4, 8, 84, 28, fill=rgba(PANEL), outline=rgba(GRID), width=2)
        b.rect(8, 12, 36, 20, fill=rgba(PAPER_DIM))
        b.text(66, 22, "OFF", FONT_BODY, 16, rgba(PAPER_DIM), anchor="mm")
    return b.done()


def gs_bar(top):
    im = Image.new("RGBA", (640, 40), rgba(PANEL, 235))
    d = ImageDraw.Draw(im)
    if top:
        stripes(d, 0, 34, 640)
    else:
        d.line([(0, 0), (639, 0)], fill=rgba(GRID))
    return im


README = """MIXTAPE - an OnionOS theme to match the Mixtape app
=====================================================
Made by Claude, Anthropic's AI model, for snyderman3000.
https://github.com/snyderman3000/mixtape

INSTALL
  Copy this "Mixtape" folder into the Themes folder on your SD card
  (Themes/Mixtape), then open Apps > Theme Switcher and pick Mixtape.

  It reskins OnionOS's menus, lists, dialogs, boot and power screens
  and the charging animation. It doesn't include system icons, so
  OnionOS keeps whichever icon pack you use.

Images: drawn by theme/generate.py in the Mixtape repo (MIT).
Fonts: Orbitron, Share Tech Mono, VT323 (SIL Open Font License,
see the licenses folder).
"""


# ---------------------------------------------------------------- the theme

TILES = {
    "favorite": g_star,
    "game": g_cassette,
    "app": g_apps,
    "setting": g_gear,
    "recent": g_clock,
    "retroarch": g_invader,
}


def build():
    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(os.path.join(OUT, "skin", "extra"))

    shutil.copy(FONT_TITLE, os.path.join(OUT, "Orbitron.ttf"))
    shutil.copy(FONT_BODY, os.path.join(OUT, "ShareTechMono.ttf"))
    shutil.copy(FONT_LCD, os.path.join(OUT, "VT323.ttf"))
    lic = os.path.join(REPO, "package", "Mixtape", "licenses")
    os.makedirs(os.path.join(OUT, "licenses"))
    for f in ("OFL-Orbitron.txt", "OFL-ShareTechMono.txt", "OFL-VT323.txt"):
        shutil.copy(os.path.join(lic, f), os.path.join(OUT, "licenses", f))
    with open(os.path.join(OUT, "README.txt"), "w") as f:
        f.write(README)

    cfg = {
        "name": "Mixtape",
        "author": "Claude for snyderman3000",
        "description": "Amber phosphor, scanlines and tape stripes, to match the Mixtape app",
        "hideLabels": {"icons": False, "hints": False},
        "batteryPercentage": {"visible": True, "font": "VT323.ttf", "size": 20, "color": hexc(AMBER),
                              "textAlign": "center", "fixed": False, "offsetX": 0, "offsetY": 0},
        "title": {"font": "Orbitron.ttf", "size": 26, "color": hexc(AMBER)},
        "hint": {"font": "ShareTechMono.ttf", "size": 26, "color": hexc(PAPER_DIM)},
        "currentpage": {"color": hexc(AMBER)},
        "total": {"color": hexc(PAPER_DIM)},
        "grid": {"font": "ShareTechMono.ttf", "grid1x4": 24, "grid3x4": 18, "color": hexc(PAPER_DIM),
                 "selectedcolor": hexc(AMBER)},
        "list": {"font": "ShareTechMono.ttf", "size": 24, "color": hexc(PAPER)},
    }
    with open(os.path.join(OUT, "config.json"), "w") as f:
        json.dump(cfg, f, indent=4)

    save(background(), "skin/background.png")
    save(title_bar().convert("RGB"), "skin/bg-title.png")
    save(tips_bar().convert("RGB"), "skin/tips-bar-bg.png")
    save(logo(), "skin/miyoo-topbar.png")
    for name, g in TILES.items():
        save(tile(g, False), f"skin/ic-{name}-n.png")
        save(tile(g, True), f"skin/ic-{name}-f.png")
    save(list_row(56), "skin/bg-list-s.png")
    save(list_row(90), "skin/bg-list-l.png")
    save(grid_item(True), "skin/bg-game-item-f.png")
    save(grid_item(False), "skin/bg-game-item-n.png")
    for i, h in enumerate((70, 130, 190, 250), 1):
        save(panel(320, h), f"skin/bg-pop-menu-{i}.png")
    save(panel(640, 300, MAGENTA).convert("RGB"), "skin/pop-bg.png")
    save(panel(214, 120, CYAN).convert("RGB"), "skin/bg-ra-list-item.png")
    save(button_bg(), "skin/bg-button-f.png")
    hl = Image.new("RGBA", (160, 60), (0, 0, 0, 0))
    ImageDraw.Draw(hl).rectangle([1, 1, 158, 58], fill=rgba(AMBER_DK, 220), outline=rgba(AMBER), width=2)
    save(hl, "skin/bg-keysetting-f.png")
    dl = Image.new("RGB", (640, 4), BG)
    ImageDraw.Draw(dl).line([(0, 1), (639, 1)], fill=GRID, width=1)
    for x in range(0, 640, 7):
        ImageDraw.Draw(dl).line([(x + 4, 1), (x + 6, 1)], fill=BG)
    save(dl, "skin/div-line-h.png")
    dv = Image.new("RGB", (4, 360), BG)
    ImageDraw.Draw(dv).line([(1, 0), (1, 359)], fill=GRID)
    save(dv, "skin/div-line-v-01.png")
    save(dots(True), "skin/dot-a.png")
    save(dots(False), "skin/dot-n.png")
    save(Image.new("RGBA", (162, 46), (0, 0, 0, 0)), "skin/Empty.png")
    save(Image.new("RGBA", (1, 60), (0, 0, 0, 0)), "skin/icon-game.png")
    save(digits(), "skin/list-num.png")
    nb = Image.new("RGBA", (44, 24), (0, 0, 0, 0))
    ImageDraw.Draw(nb).rectangle([0, 2, 43, 21], fill=rgba(PANEL2), outline=rgba(AMBER_DIM))
    save(nb, "skin/num-bg.png")
    pd = Image.new("RGB", (10, 10), BG)
    ImageDraw.Draw(pd).rectangle([2, 2, 7, 7], fill=AMBER)
    save(pd, "skin/progress-dot.png")
    save(key_button("A", AMBER), "skin/icon-A-54.png")
    save(key_button("B", PAPER_DIM), "skin/icon-B-54.png")
    save(chip("MENU", 65, 48, AMBER), "skin/ic-MENU.png")
    save(menu_a(), "skin/ic-MENU+A.png")
    for name, lv in (("power-0%-icon", 0.03), ("power-20%-icon", 0.2), ("power-50%-icon", 0.5), ("power-80%-icon", 0.8), ("power-full-icon", 1.0)):
        save(battery(lv), f"skin/{name}.png")
    save(battery(1.0, charging=True), "skin/ic-power-charge-100%.png")
    for i in range(1, 5):
        save(wifi_icon(i), f"skin/icon-wifi-signal-0{i}.png")
    G = PAPER
    save(icon(g_check, (48, 48), CYAN), "skin/icon-wifi-connected.png")
    save(icon(g_lock, (48, 48), G), "skin/icon-wifi-locked.png")
    save(icon(g_wifi, (46, 46), G), "skin/icon-setting-wifi.png")
    save(icon(g_power, (48, 48), G), "skin/icon-Shutdown.png")
    save(icon(g_sun, (48, 48), G), "skin/icon-brightness-48.png")
    save(icon(g_info, (48, 48), G), "skin/icon-device-info-48.png")
    save(icon(g_reset, (48, 49), G), "skin/icon-factory-reset-48.png")
    save(icon(g_gamepad, (48, 48), G), "skin/icon-key-setting-48.png")
    save(icon(g_globe, (48, 48), G), "skin/icon-language-48.png")
    save(icon(g_folder, (60, 60), AMBER), "skin/icon-folder.png")
    save(icon(g_sd, (19, 24), G, w=1.5, pad=0.05), "skin/icon-TF.png")
    save(icon(g_arrow, (24, 24), AMBER, w=2.4), "skin/icon-left-arrow-24.png")
    save(icon(g_arrow, (24, 24), AMBER, w=2.4, left=False), "skin/icon-right-arrow-24.png")
    save(icon(g_speaker, (48, 48), G), "skin/sound-icon.png")
    save(icon(g_headphone, (22, 23), G, w=1.6, pad=0.04), "skin/headphone-icon.png")
    save(icon(g_wrench, (50, 44), AMBER, w=3), "skin/fixit.png")
    save(icon(g_palette, (48, 48), G), "skin/color.png")
    save(icon(g_star, (34, 39), AMBER, w=2.2, pad=0.08), "skin/ic-favorite-mark.png")
    card = noise_card(250, 360)
    save(card, "skin/thumb-default.png")
    save(noise_card(250, 360, "TUNING"), "skin/preview-bg.png")

    # boot, power and charging screens
    save(screen("MIXTAPE", "COMMUNITY PORT DECK FOR ONION OS", footer="LOADING TAPE..."), "skin/extra/bootScreen.png")
    save(screen("SIDE B", "POWERING DOWN", label="SIDE B", tape="SEE YOU NEXT TIME", progress=1.0), "skin/extra/Screen_Off.png")
    save(screen("SAVING", "WRITING YOUR PROGRESS TO TAPE", label="REC", tape="SAVE STATE", progress=0.6,
                footer="DON'T TURN OFF THE POWER"), "skin/extra/Screen_Off_Save.png")
    save(screen("LOW BATTERY", "PLUG IN TO KEEP PLAYING", label="LOW BATTERY", tape="CHARGE ME", progress=0.95, meter=0.1),
         "skin/extra/lowBat.png")
    for i in range(24):
        save(screen("CHARGING", "TAPE REWINDING", label="CHARGING", tape="POWER", angle=i * math.pi / 12,
                    progress=i / 23, meter=(i + 1) / 24), f"skin/extra/chargingState{i}.png")
    with open(os.path.join(OUT, "skin", "extra", "chargingState.json"), "w") as f:
        json.dump({"frame_delay": 80}, f, indent=4)
    for i in range(11):
        save(lum(i), f"skin/extra/lum{i}.png")
    save(toggle(True), "skin/extra/toggle-on.png")
    save(toggle(False), "skin/extra/toggle-off.png")
    save(gs_bar(True), "skin/extra/gs-top-bar.png")
    save(gs_bar(False), "skin/extra/gs-bottom-bar.png")

    # Art for the Mixtape "system" that Mixtape can add to Games (see
    # launcher.go); Mixtape ships these in App/Mixtape/res.
    sysicon = system_icon()
    res = os.path.join(REPO, "package", "Mixtape", "res")
    os.makedirs(res, exist_ok=True)
    sysicon.save(os.path.join(res, "mixtape.png"), optimize=True)
    boxart().save(os.path.join(res, "boxart.png"), optimize=True)

    save(preview(cfg), "preview.png")
    print("theme written to", OUT)


def system_icon():
    """120x130 icon for the Mixtape entry in Games: a cassette in amber line art."""
    b = Big(120, 130)
    g_cassette(b, 12, 17, 96, rgba(AMBER), 3.2)
    im = b.done()
    halo = im.filter(ImageFilter.GaussianBlur(4))
    halo.putalpha(halo.getchannel("A").point(lambda v: int(v * 0.5)))
    out = Image.new("RGBA", im.size, (0, 0, 0, 0))
    out.alpha_composite(halo)
    out.alpha_composite(im)
    return out


def boxart():
    """Box art for the Mixtape entry in the Games list."""
    im = Image.new("RGBA", (250, 250), rgba(BG))
    cassette(im, 15, 60, 220, 128, "MIXTAPE", "COMMUNITY PORT DECK", progress=0.35)
    d = ImageDraw.Draw(im)
    stripes(d, 0, 26, 250, 3)
    glow_text(im, (125, 218), "PRESS A", FONT_BODY, 18, AMBER, anchor="mm", radius=2)
    scanlines(im)
    return im


def preview(cfg):
    """A mock of OnionOS's main menu with this theme, for the theme picker."""
    sk = lambda n: Image.open(os.path.join(OUT, "skin", n)).convert("RGBA")
    im = sk("background.png")
    im.alpha_composite(sk("miyoo-topbar.png"), (10, 6))
    im.alpha_composite(sk("icon-wifi-signal-04.png"), (500, 4))
    im.alpha_composite(sk("power-80%-icon.png"), (580, 4))
    d = ImageDraw.Draw(im)
    d.text((562, 28), "80%", font=font(FONT_LCD, 20), fill=rgba(AMBER), anchor="rm")
    labels = (("favorite", "Favorites"), ("game", "Games"), ("app", "Apps"), ("setting", "Settings"))
    for i, (name, label) in enumerate(labels):
        x = 8 + i * 156
        f = i == 1
        im.alpha_composite(sk(f"ic-{name}-{'f' if f else 'n'}.png"), (x, 92))
        d.text((x + 78, 300), label, font=font(FONT_BODY, 24), fill=rgba(AMBER if f else PAPER_DIM), anchor="mm")
    for i in range(4):
        im.alpha_composite(sk("dot-a.png" if i == 0 else "dot-n.png"), (272 + i * 24, 392))
    im.alpha_composite(sk("tips-bar-bg.png"), (0, 420))
    im.alpha_composite(sk("icon-A-54.png"), (14, 423))
    d.text((74, 450), "SELECT", font=font(FONT_BODY, 26), fill=rgba(PAPER_DIM), anchor="lm")
    im.alpha_composite(sk("icon-B-54.png"), (200, 423))
    d.text((260, 450), "BACK", font=font(FONT_BODY, 26), fill=rgba(PAPER_DIM), anchor="lm")
    return im.resize((480, 360), Image.LANCZOS)


if __name__ == "__main__":
    sys.exit(build())
