#!/usr/bin/env python3
"""Draws the example looks extension's pictures (schema/extension/examples/example-looks, docs/extensions/extensions.md "Avatars").

Two original looks, drawn here so nothing third-party is in the repo:
- aurora: a glowing jellyfish, avatar PNGs and SVGs for light and dark, and all five dot states as APNGs.
- pebble: a round grey pebble, one PNG and an SVG only: the hub gives it an idle dot from its avatar.

    python3 scripts/assets/make_example_looks.py [--out schema/extension/examples/example-looks]
"""
import argparse
import math
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

SS = 4  # supersampling


def hexrgb(h, a=255):
    h = h.lstrip("#")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16), a)


AURORA = {
    "light": {"bg": "#E8ECFF", "body": "#5B6CFF", "body2": "#38C6B0", "eye": "#101433", "glow": "#FFFFFF"},
    "dark": {"bg": "#161A3A", "body": "#8C98FF", "body2": "#4FE0C8", "eye": "#0B0E24", "glow": "#DDE3FF"},
}
PEBBLE = {
    "light": {"bg": "#F1EEE8", "body": "#8E8A84", "eye": "#2A2724"},
    "dark": {"bg": "#2A2826", "body": "#B9B4AC", "eye": "#1A1816"},
}


def jelly(side, scheme, *, t=0.0, state="idle", blink=1.0):
    """One frame of aurora at side px: t in [0, 1) is the loop's phase, blink 1 open … 0 shut."""
    c = AURORA[scheme]
    n = side * SS
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    gray = state == "offline"
    d.ellipse((0, 0, n - 1, n - 1), fill=hexrgb("#E4E4E8" if gray and scheme == "light" else ("#26262C" if gray else c["bg"])))
    bob = 0.0
    if state == "working":
        bob = math.sin(t * 2 * math.pi * 2) * 0.03
    elif state == "idle":
        bob = math.sin(t * 2 * math.pi) * 0.015
    elif state == "needs_you":
        bob = abs(math.sin(t * 2 * math.pi * 2)) * -0.04
    cx, cy = 0.5 * n, (0.46 + bob) * n
    body = hexrgb("#9A9AA2" if gray else c["body"])
    body2 = hexrgb("#7C7C84" if gray else c["body2"])
    # tentacles: four waves under the bell
    for i in range(4):
        x0 = cx + (i - 1.5) * 0.12 * n
        pts = []
        for k in range(12):
            y = cy + 0.08 * n + k * 0.022 * n
            sway = math.sin(t * 2 * math.pi + k * 0.6 + i) * (0.0 if gray else 0.018) * n
            pts.append((x0 + sway, y))
        d.line(pts, fill=body2, width=int(0.035 * n), joint="curve")
    # the bell
    d.pieslice((cx - 0.3 * n, cy - 0.28 * n, cx + 0.3 * n, cy + 0.3 * n), 180, 360, fill=body)
    d.rectangle((cx - 0.3 * n, cy, cx + 0.3 * n, cy + 0.06 * n), fill=body)
    d.ellipse((cx - 0.3 * n, cy + 0.02 * n, cx + 0.3 * n, cy + 0.1 * n), fill=body)
    if not gray:
        d.ellipse((cx - 0.2 * n, cy - 0.2 * n, cx - 0.08 * n, cy - 0.12 * n), fill=hexrgb(c["glow"], 150))
    # eyes
    look_up = 0.03 * n if state == "thinking" else 0.0
    ey = cy - 0.03 * n - look_up
    for dx in (-0.1, 0.1):
        ex = cx + dx * n
        h = 0.05 * n * (0.12 if gray else max(blink, 0.12))
        d.ellipse((ex - 0.035 * n, ey - h, ex + 0.035 * n, ey + h), fill=hexrgb(c["eye"]))
    img = img.resize((side, side), Image.LANCZOS)
    o = ImageDraw.Draw(img)
    s = side
    if state == "thinking":  # three dots in a bubble, lit in turn
        o.ellipse((0.66 * s, 0.08 * s, 0.94 * s, 0.3 * s), fill=hexrgb("#FFFFFF", 235))
        for i in range(3):
            lit = int(t * 3) % 3 == i
            x = 0.72 * s + i * 0.08 * s
            o.ellipse((x - 0.025 * s, 0.17 * s, x + 0.025 * s, 0.21 * s), fill=hexrgb(c["body"] if lit else "#C9CCE0"))
    if state == "working":  # an arc running round the edge
        a = t * 360
        o.arc((2, 2, s - 3, s - 3), a, a + 80, fill=hexrgb(c["body2"]), width=max(2, s // 28))
    if state == "needs_you":  # a breathing red ring
        alpha = int(150 + 100 * math.sin(t * 2 * math.pi))
        o.ellipse((1, 1, s - 2, s - 2), outline=hexrgb("#E5484D", alpha), width=max(2, s // 22))
    if state == "offline":  # a z
        o.text((0.74 * s, 0.1 * s), "z", fill=hexrgb("#8A8A92"))
    return img


def pebble(side, scheme):
    c = PEBBLE[scheme]
    n = side * SS
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.ellipse((0, 0, n - 1, n - 1), fill=hexrgb(c["bg"]))
    d.ellipse((0.18 * n, 0.26 * n, 0.82 * n, 0.8 * n), fill=hexrgb(c["body"]))
    for dx in (-0.1, 0.1):
        d.ellipse((0.5 * n + dx * n - 0.03 * n, 0.48 * n, 0.5 * n + dx * n + 0.03 * n, 0.56 * n), fill=hexrgb(c["eye"]))
    return img.resize((side, side), Image.LANCZOS)


def jelly_svg(scheme):
    c = AURORA[scheme]
    tent = "".join(
        f'<path d="M{50 + (i - 1.5) * 12} 54 q3 6 0 12 t0 12" fill="none" stroke="{c["body2"]}" stroke-width="3.5" stroke-linecap="round"/>'
        for i in range(4))
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">'
            f'<title>极光</title><circle cx="50" cy="50" r="50" fill="{c["bg"]}"/>{tent}'
            f'<path d="M20 46 A30 29 0 0 1 80 46 L80 52 Q50 60 20 52 Z" fill="{c["body"]}"/>'
            f'<ellipse cx="36" cy="30" rx="6" ry="4" fill="{c["glow"]}" fill-opacity="0.6"/>'
            f'<ellipse cx="40" cy="43" rx="3.5" ry="5" fill="{c["eye"]}"/><ellipse cx="60" cy="43" rx="3.5" ry="5" fill="{c["eye"]}"/>'
            f'</svg>\n')


def pebble_svg():
    c = PEBBLE["light"]
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><title>石子</title>'
            f'<circle cx="50" cy="50" r="50" fill="{c["bg"]}"/><ellipse cx="50" cy="53" rx="32" ry="27" fill="{c["body"]}"/>'
            f'<ellipse cx="40" cy="52" rx="3" ry="4" fill="{c["eye"]}"/><ellipse cx="60" cy="52" rx="3" ry="4" fill="{c["eye"]}"/></svg>\n')


def apng(path, frames, fps):
    frames[0].save(path, save_all=True, append_images=frames[1:], duration=int(1000 / fps), loop=0, format="PNG", optimize=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="schema/extension/examples/example-looks")
    out = Path(ap.parse_args().out)
    a = out / "looks" / "aurora"
    p = out / "looks" / "pebble"
    a.mkdir(parents=True, exist_ok=True)
    p.mkdir(parents=True, exist_ok=True)
    for side in (128, 256):
        jelly(side, "light").save(a / f"avatar-{side}.png", optimize=True)
        jelly(side, "dark").save(a / f"avatar-dark-{side}.png", optimize=True)
    (a / "avatar.svg").write_text(jelly_svg("light"))
    (a / "avatar-dark.svg").write_text(jelly_svg("dark"))
    frames = 12
    for state, fps in (("idle", 6), ("thinking", 6), ("working", 10), ("needs_you", 6), ("offline", 4)):
        imgs = []
        for i in range(frames):
            t = i / frames
            blink = 0.15 if state == "idle" and i == frames - 1 else 1.0
            imgs.append(jelly(144, "light", t=t, state=state, blink=blink))
        apng(a / f"{state}.png", imgs, fps)
    pebble(256, "light").save(p / "avatar-256.png", optimize=True)
    (p / "avatar.svg").write_text(pebble_svg())


if __name__ == "__main__":
    main()
