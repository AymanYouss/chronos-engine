#!/usr/bin/env python3
"""Generate a styled HTML chart of measured replay-latency benchmarks.

The numbers come from `go test -bench BenchmarkReplay` on an Apple M1 Max and
are rendered as a dark, portfolio-quality SVG line chart. The HTML is then
screenshotted by headless Chrome into docs/portfolio/replay-benchmark.png.
"""
import os

# (events, latency_us, throughput_label)
DATA = [
    (21, 1.24, "805k/s"),
    (101, 5.35, "187k/s"),
    (201, 10.43, "96k/s"),
    (1001, 60.82, "16.4k/s"),
    (2001, 131.71, "7.6k/s"),
]

X0, X1 = 120, 1080
Y0, Y1 = 90, 470
YMAX = 140.0


def xs(i):
    return X0 + (X1 - X0) * i / (len(DATA) - 1)


def ys(v):
    return Y1 - (v / YMAX) * (Y1 - Y0)


pts = [(xs(i), ys(v)) for i, (_, v, _) in enumerate(DATA)]
line = " ".join(f"{x:.1f},{y:.1f}" for x, y in pts)
area = f"{X0},{Y1} " + line + f" {X1},{Y1}"

grid = []
for g in range(0, 141, 35):
    y = ys(g)
    grid.append(
        f'<line x1="{X0}" y1="{y:.1f}" x2="{X1}" y2="{y:.1f}" class="grid"/>'
        f'<text x="{X0-14:.0f}" y="{y+4:.1f}" class="ylab">{g}</text>'
    )

point_markers = []
for i, (ev, v, tp) in enumerate(DATA):
    x, y = pts[i]
    point_markers.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="5.5" class="pt"/>')
    dy = -16 if i < len(DATA) - 1 else -16
    point_markers.append(
        f'<text x="{x:.1f}" y="{y+dy:.1f}" class="val">{v:.1f} µs</text>'
    )
    point_markers.append(f'<text x="{x:.1f}" y="{Y1+26:.0f}" class="xlab">{ev}</text>')

svg = f"""
<svg viewBox="0 0 1200 560" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="area" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#5b7cfa" stop-opacity="0.35"/>
      <stop offset="100%" stop-color="#5b7cfa" stop-opacity="0.02"/>
    </linearGradient>
  </defs>
  {''.join(grid)}
  <text x="{(X0+X1)/2:.0f}" y="522" class="axis-title">Workflow history size (events)</text>
  <text transform="translate(40,{(Y0+Y1)/2:.0f}) rotate(-90)" class="axis-title">Replay latency (µs)</text>
  <polygon points="{area}" fill="url(#area)"/>
  <polyline points="{line}" class="line"/>
  {''.join(point_markers)}
</svg>
"""

html = f"""<!doctype html><html><head><meta charset="utf-8"/>
<style>
  @font-face {{ font-family:'Inter'; src: local('Inter'); }}
  * {{ margin:0; box-sizing:border-box; }}
  body {{ background:#0a0d13; font-family:'Inter',-apple-system,'Segoe UI',sans-serif; }}
  .card {{ width:1200px; padding:40px 44px 30px; background:
      radial-gradient(1200px 400px at 80% -10%, rgba(91,124,250,0.12), transparent),
      #10141c; }}
  .eyebrow {{ color:#5b7cfa; font-size:13px; font-weight:600; letter-spacing:.08em; text-transform:uppercase; }}
  h1 {{ color:#e7eaf0; font-size:30px; font-weight:600; letter-spacing:-0.02em; margin-top:8px; }}
  .sub {{ color:#97a0b2; font-size:15px; margin-top:8px; }}
  .grid {{ stroke:#232a38; stroke-width:1; }}
  .ylab {{ fill:#5f6a7d; font-size:15px; text-anchor:end; font-family:'JetBrains Mono',monospace; }}
  .xlab {{ fill:#97a0b2; font-size:16px; text-anchor:middle; font-family:'JetBrains Mono',monospace; }}
  .axis-title {{ fill:#5f6a7d; font-size:15px; text-anchor:middle; }}
  .line {{ fill:none; stroke:#5b7cfa; stroke-width:3; stroke-linejoin:round; stroke-linecap:round;
           filter: drop-shadow(0 4px 10px rgba(91,124,250,0.4)); }}
  .pt {{ fill:#0a0d13; stroke:#5b7cfa; stroke-width:3; }}
  .val {{ fill:#e7eaf0; font-size:15px; font-weight:600; text-anchor:middle; font-family:'JetBrains Mono',monospace; }}
  .foot {{ display:flex; gap:26px; margin-top:16px; color:#5f6a7d; font-size:13px; }}
  .foot b {{ color:#3fb950; font-weight:600; }}
</style></head>
<body><div class="card">
  <div class="eyebrow">Benchmark · deterministic replay</div>
  <h1>Resuming a workflow costs microseconds, not milliseconds</h1>
  <div class="sub">Time to deterministically replay a completed workflow history — pure CPU, zero I/O. Apple M1 Max, single core.</div>
  {svg}
  <div class="foot">
    <div><b>~61 µs</b> to replay a 1,000-event history</div>
    <div><b>16,000+</b> replays/sec per core</div>
    <div><b>0</b> external calls during replay</div>
  </div>
</div></body></html>
"""

out = os.path.join(os.path.dirname(__file__), "..", "..", "docs", "portfolio", "_benchmark.html")
out = os.path.abspath(out)
with open(out, "w") as f:
    f.write(html)
print(out)
