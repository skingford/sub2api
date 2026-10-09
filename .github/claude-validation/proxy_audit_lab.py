"""Offline CONNECT/SOCKS5 relay with one owner for each SSL socket.

The existing threaded lab relays read and write the same SSLSocket concurrently.
This comparison helper services TLS reads/writes in a single nonblocking loop.
It can only connect to the loopback model simulator, never an external host.
"""
import select
import socket
import socketserver
import ssl
import threading
import time


def start_proxy(cert, key, kind):
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(cert), str(key))

    def exact(conn, size):
        data = b''
        while len(data) < size:
            chunk = conn.recv(size - len(data))
            if not chunk:
                raise EOFError('closed during local proxy negotiation')
            data += chunk
        return data

    class Proxy(socketserver.BaseRequestHandler):
        def handle(self):
            client = self.request
            client.settimeout(10)
            peer = None
            if kind == 'https':
                client = context.wrap_socket(client, server_side=True)
            try:
                if kind == 'socks5':
                    version, count = exact(client, 2)
                    assert version == 5 and 0 in exact(client, count)
                    client.sendall(b'\x05\x00')
                    version, command, _, address = exact(client, 4)
                    assert version == 5 and command == 1 and address == 3
                    host = exact(client, exact(client, 1)[0]).decode()
                    port = int.from_bytes(exact(client, 2), 'big')
                    assert (host, port) == ('api.anthropic.com', 443)
                    reply = b'\x05\x00\x00\x01\x7f\x00\x00\x01\x01\xbb'
                else:
                    headers = b''
                    while not headers.endswith(b'\r\n\r\n'):
                        headers += exact(client, 1)
                        assert len(headers) < 8192
                    assert headers.startswith(b'CONNECT api.anthropic.com:443 ')
                    reply = b'HTTP/1.1 200 Connection established\r\n\r\n'
                peer = socket.create_connection(('127.0.0.1', 443), timeout=10)
                client.sendall(reply)
                client.setblocking(False)
                peer.setblocking(False)
                other = {client: peer, peer: client}
                outgoing = {client: bytearray(), peer: bytearray()}
                live = {client, peer}
                deadline = time.monotonic() + 25
                while live and time.monotonic() < deadline:
                    readable, writable, _ = select.select(list(live), [s for s in other if outgoing[s]], [], .1)
                    if client in live and isinstance(client, ssl.SSLSocket) and client.pending() and client not in readable:
                        readable.append(client)
                    for source in readable:
                        try:
                            part = source.recv(65536)
                            if part:
                                outgoing[other[source]].extend(part)
                            else:
                                live.discard(source)
                        except (BlockingIOError, ssl.SSLWantReadError, ssl.SSLWantWriteError):
                            pass
                    for target in writable:
                        try:
                            sent = target.send(outgoing[target])
                            del outgoing[target][:sent]
                        except (BlockingIOError, ssl.SSLWantReadError, ssl.SSLWantWriteError):
                            pass
                    if len(live) < 2 and not any(outgoing.values()):
                        break
            except (OSError, EOFError):
                pass
            finally:
                if peer is not None:
                    peer.close()
                client.close()

    server = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Proxy)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f'{kind}://127.0.0.1:{server.server_address[1]}'
