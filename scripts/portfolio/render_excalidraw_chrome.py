#!/usr/bin/env python3
"""Render an .excalidraw file to a self-running HTML page that exports it to SVG.

The skill's bundled Playwright Chromium has no outbound network here, so we emit
an HTML page that imports @excalidraw/excalidraw from a CDN and renders the SVG
inline; the system Chrome (which does have network) then screenshots it.
"""
import json
import sys
from pathlib import Path

src = Path(sys.argv[1])
data = json.loads(src.read_text())
out = src.with_suffix(".render.html")

html = """<!doctype html><html><head><meta charset="utf-8"/>
<style>*{margin:0;padding:0;box-sizing:border-box}html,body{background:#fff}
#root{display:inline-block}#root svg{display:block}</style></head>
<body><div id="root"></div>
<script type="module">
const data = %s;
(async () => {
  try {
    const mod = await import("https://esm.sh/@excalidraw/excalidraw@0.17.6?bundle");
    const exportToSvg = mod.exportToSvg || (mod.default && mod.default.exportToSvg);
    if (!exportToSvg) { throw new Error("exports=" + Object.keys(mod).join(",")); }
    const svg = await exportToSvg({
      elements: data.elements || [],
      appState: { ...(data.appState||{}), exportBackground: true, exportWithDarkMode: false },
      files: data.files || {},
    });
    const vb = (svg.getAttribute("viewBox") || "0 0 1430 790").split(" ").map(Number);
    svg.setAttribute("width", vb[2]);
    svg.setAttribute("height", vb[3]);
    svg.style.width = vb[2] + "px";
    svg.style.height = vb[3] + "px";
    const root = document.getElementById("root");
    root.innerHTML = "";
    root.appendChild(svg);
    document.title = "READY";
  } catch (e) {
    document.title = "ERR";
    document.getElementById("root").textContent = "ERROR: " + (e && e.message ? e.message : e);
  }
})();
</script></body></html>""" % json.dumps(data)

out.write_text(html)
print(out)
