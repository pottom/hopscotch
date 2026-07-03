#!/usr/bin/env python3
"""dummy-responder.py — local HTTP target for the docs screenshot fixture.

Every fake hostname in scripts/demo/screenshots.sh's synthetic config
(*.hopscotch-demo.internal) resolves to 127.0.0.1 via a temporary /etc/hosts
block; this is what they all actually reach once hopscotch (directly, or via
a fake tunnel's SSH forward) connects to them. Returns a moderately-sized
body so BytesIn/BytesOut and the TUI/web-UI traffic graphs show real,
non-trivial motion instead of a few-byte blip.

Usage: dummy-responder.py <port>
"""
import http.server
import sys

PORT = int(sys.argv[1])
BODY = ("hopscotch-demo fixture response\n" * 400).encode()  # ~12KB


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(BODY)))
        self.end_headers()
        self.wfile.write(BODY)

    def log_message(self, *args):
        pass  # keep the demo's own terminal output quiet


if __name__ == "__main__":
    http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
