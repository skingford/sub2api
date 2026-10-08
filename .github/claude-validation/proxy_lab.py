"""Local-only CONNECT and SOCKS5 servers for the isolated transport lab."""
import socket
import socketserver
import ssl
import threading


def start_proxy(cert, key, kind):
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(cert), str(key))
    def read_exact(conn, size):
        out = b''
        while len(out) < size:
            part = conn.recv(size - len(out))
            if not part:
                raise EOFError('Proxy peer closed during negotiation')
            out += part
        return out
    class Proxy(socketserver.BaseRequestHandler):
        def handle(self):
            conn = self.request
            conn.settimeout(10)
            if kind == 'https':
                conn = context.wrap_socket(conn, server_side=True)
            try:
                if kind == 'socks5':
                    version, methods = read_exact(conn, 2)
                    assert version == 5 and 0 in read_exact(conn, methods)
                    conn.sendall(b'\x05\x00')
                    version, command, _, address_type = read_exact(conn, 4)
                    assert version == 5 and command == 1 and address_type == 3
                    hostname = read_exact(conn, read_exact(conn, 1)[0]).decode()
                    port = int.from_bytes(read_exact(conn, 2), 'big')
                    assert hostname == 'api.anthropic.com' and port == 443
                    reply = b'\x05\x00\x00\x01\x7f\x00\x00\x01\x01\xbb'
                else:
                    data = b''
                    while not data.endswith(b'\r\n\r\n'):
                        data += read_exact(conn, 1)
                        assert len(data) < 8192
                    assert data.startswith(b'CONNECT api.anthropic.com:443 ')
                    reply = b'HTTP/1.1 200 Connection established\r\n\r\n'
                # Never resolve or connect to the requested hostname.
                peer = socket.create_connection(('127.0.0.1', 443), timeout=10)
                conn.sendall(reply)
                def copy(source, target):
                    try:
                        while True:
                            data = source.recv(65536)
                            if not data:
                                break
                            target.sendall(data)
                    except (OSError, EOFError):
                        pass
                    finally:
                        try:
                            target.shutdown(socket.SHUT_WR)
                        except OSError:
                            pass
                back = threading.Thread(target=copy, args=(peer, conn), daemon=True)
                back.start()
                copy(conn, peer)
                back.join(timeout=2)
                peer.close()
            finally:
                conn.close()
    server = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Proxy)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f'{kind}://127.0.0.1:{server.server_address[1]}'
