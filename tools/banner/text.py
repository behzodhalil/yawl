import json
import sys
from pathlib import Path

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont

FONT_DIR = Path(sys.argv[1] if len(sys.argv) > 1 else "IoskeleyMono")
OUT = Path(__file__).with_name("text.json")
fonts = {w: TTFont(FONT_DIR / f"IoskeleyMono-{w}.ttf") for w in ["Regular", "Medium", "SemiBold"]}


def text(s, weight, size, x, y, spacing=0.0, center=False):
    font = fonts[weight]
    glyphs, cmap, widths = font.getGlyphSet(), font.getBestCmap(), font["hmtx"]
    k = size / font["head"].unitsPerEm
    if center:
        x -= (sum(widths[cmap[ord(c)]][0] * k + spacing for c in s) - spacing) / 2
    pen = SVGPathPen(glyphs, ntos=lambda v: ("%.1f" % v).rstrip("0").rstrip("."))
    for c in s:
        g = cmap[ord(c)]
        glyphs[g].draw(TransformPen(pen, (k, 0, 0, -k, x, y)))
        x += widths[g][0] * k + spacing
    return pen.getCommands()


OUT.write_text(json.dumps({
    "Y": text("Y", "SemiBold", 22, 87, 97, center=True),
    "brand": text("Yawl", "SemiBold", 22, 114, 96),
    "eyebrow": text("Open source PaaS", "Regular", 17, 72, 226),
    "h1": text("Deploy to your", "Medium", 62, 69, 316, -1.8) + text("own servers.", "Medium", 62, 69, 388, -1.8),
    "sub": text("Push to Git. Yawl builds, deploys", "Regular", 21, 72, 462) + text("and rolls back on failure.", "Regular", 21, 72, 492),
}))
print(f"wrote {OUT}")
