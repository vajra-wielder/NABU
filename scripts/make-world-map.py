#!/usr/bin/env python3
"""Regenerate internal/web/assets/world.svg from world-atlas's land-110m.json
(Natural Earth, public domain), equirectangular, 720x360.

    npm pack world-atlas@2.0.2 && tar xzf world-atlas-2.0.2.tgz
    python3 scripts/make-world-map.py package/land-110m.json internal/web/assets/world.svg
"""
import json
import sys

t = json.load(open(sys.argv[1]))
sx, sy = t["transform"]["scale"]
tx, ty = t["transform"]["translate"]
arcs = []
for a in t["arcs"]:
    x = y = 0
    pts = []
    for dx, dy in a:
        x += dx
        y += dy
        pts.append((x * sx + tx, y * sy + ty))
    arcs.append(pts)


def ring(r):
    pts = []
    for i in r:
        a = arcs[i] if i >= 0 else arcs[~i][::-1]
        pts.extend(a if not pts else a[1:])
    return pts


W, H = 720, 360
d = []
for g in t["objects"]["land"]["geometries"]:
    for poly in (g["arcs"] if g["type"] == "MultiPolygon" else [g["arcs"]]):
        for r in poly:
            pts = [((lon + 180) / 360 * W, (90 - lat) / 180 * H) for lon, lat in ring(r)]
            if len(pts) >= 3:
                d.append("M" + " ".join(f"{x:.1f} {y:.1f}" for x, y in pts) + "Z")
open(sys.argv[2], "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}"><path fill="#d9c09b" stroke="#b89a70" stroke-width=".4" d="{"".join(d)}"/></svg>')
