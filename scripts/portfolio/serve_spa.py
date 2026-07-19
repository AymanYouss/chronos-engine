#!/usr/bin/env python3
"""Minimal static server with SPA history fallback, for screenshotting the UI."""
import http.server
import os
import sys

ROOT = sys.argv[2] if len(sys.argv) > 2 else "web/dist"
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 4180


class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **kw):
        super().__init__(*a, directory=ROOT, **kw)

    def do_GET(self):
        path = self.path.split("?")[0]
        full = os.path.join(ROOT, path.lstrip("/"))
        if path != "/" and not os.path.exists(full):
            self.path = "/index.html"  # SPA fallback
        return super().do_GET()

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    http.server.HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
