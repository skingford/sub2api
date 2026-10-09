"""CONNECT-only egress for an internal Docker network; does not terminate TLS or log request headers."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
import select
import socket

HOST = os.environ['ALLOWED_HOST'].lower()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_CONNECT(self):
        if self.path.lower() != HOST + ':443':
            self.send_error(403, 'Destination not authorized for this test')
            return
        try:
            remote = socket.create_connection((HOST, 443), timeout=15)
        except OSError:
            self.send_error(502, 'Authorized destination unavailable')
            return
        self.send_response(200, 'Connection established')
        self.end_headers()
        self.close_connection = True
        try:
            while True:
                ready, _, _ = select.select([self.connection, remote], [], [], 90)
                if not ready:
                    break
                for source in ready:
                    chunk = source.recv(65536)
                    if not chunk:
                        return
                    (remote if source is self.connection else self.connection).sendall(chunk)
        finally:
            remote.close()


ThreadingHTTPServer(('0.0.0.0', 8080), Handler).serve_forever()
