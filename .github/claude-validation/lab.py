import errno


import fcntl


import gzip




import ipaddress


import json








import selectors




import socket




import struct


import subprocess


import threading


import time


from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


from pathlib import Path


ROOT = Path('/work')
DUMMY_KEY = 'local-container-key-not-a-real-credential'
MARKER = 'LOCAL_CLI_VALIDATION_20261008'
LOCK = threading.Lock()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def verify_isolation():
    interfaces = sorted(p.name for p in Path('/sys/class/net').iterdir())
    details = []
    with socket.socket() as probe:
        for name in interfaces:
            path = Path('/sys/class/net') / name
            flags = int((path / 'flags').read_text(), 16)
            try:
                address = socket.inet_ntoa(fcntl.ioctl(probe.fileno(), 0x8915,
                                          struct.pack('256s', name.encode()))[20:24])
            except OSError:
                address = None
            details.append({'name': name, 'flags': hex(flags),
                            'state': (path / 'operstate').read_text().strip(), 'ipv4': address})
            # Older Docker kernels create these two unconfigured, DOWN tunnel
            # devices even in a network=none namespace. They are not egress links.
            if name != 'lo' and (name not in ('tunl0', 'ip6tnl0') or flags & 1 or address):
                raise RuntimeError(f'Refusing to launch CLI: possible egress interface {name}')
    if 'lo' not in interfaces:
        raise RuntimeError('Refusing to launch CLI: no loopback interface')
    routes = Path('/proc/net/route').read_text()
    if len(routes.splitlines()) > 1:
        raise RuntimeError('Refusing to launch CLI: an IPv4 route exists')
    addresses = sorted({x[4][0] for x in socket.getaddrinfo('api.anthropic.com', 443)})
    if not addresses or not all(ipaddress.ip_address(a).is_loopback for a in addresses):
        raise RuntimeError(f'Refusing to launch CLI: target is not loopback: {addresses}')
    # RFC 5737 documentation address, never an Anthropic endpoint.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.settimeout(1)
        result = probe.connect_ex(('198.51.100.1', 443))
    if result not in (errno.ENETUNREACH, errno.EHOSTUNREACH):
        raise RuntimeError(f'Refusing to launch CLI: unexpected external probe result {result}')
    return {'interfaces': interfaces, 'interface_details': details, 'ipv4_routes': routes,
            'ipv6_addresses': Path('/proc/net/if_inet6').read_text(),
            'api_anthropic_com_resolves_to': addresses,
            'external_documentation_address_probe': errno.errorcode[result],
            'real_credentials_mounted': False}


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):
        pass

    def respond(self, data, status=200):
        payload = json.dumps(data).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def start_capture(folder):
    log = (folder / 'tcpdump.log').open('w')
    proc = subprocess.Popen(['tcpdump', '-B', '16384', '--immediate-mode', '-Z', 'root', '-i', 'lo', '-s', '0', '-U', '-w',
                             str(folder / 'capture.pcap'), 'tcp port 443'],
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    selector = selectors.DefaultSelector()
    selector.register(proc.stderr, selectors.EVENT_READ)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError('tcpdump exited before becoming ready')
        if selector.select(timeout=0.2):
            line = proc.stderr.readline()
            log.write(line)
            log.flush()
            if 'listening on lo' in line:
                selector.close()
                return proc, log
    proc.kill()
    raise RuntimeError('tcpdump readiness timed out')


def decode_capture(folder, keyfile, name):
    args = ['tshark', '-r', str(folder / 'capture.pcap')]
    if keyfile is not None:
        args += ['-o', 'tls.keylog_file:' + str(keyfile)]
    proc = subprocess.run(args + ['-Y', 'http.request', '-T', 'json'],
                          capture_output=True, text=True, timeout=20)
    (folder / (name + '.json')).write_text(proc.stdout)
    (folder / (name + '.stderr')).write_text(proc.stderr)
    if proc.returncode:
        raise RuntimeError(f'tshark failed in {name}: {proc.stderr[-500:]}')
    packets = json.loads(proc.stdout)
    uris = []
    decoded_bodies = []
    for packet in packets:
        layers = packet.get('_source', {}).get('layers', {})
        http = layers.get('http', {})
        for key, value in http.items():
            if isinstance(value, dict) and 'http.request.uri' in value:
                uris.append(value['http.request.uri'])
        value = http.get('http.file_data')
        if isinstance(value, str):
            try:
                payload = bytes.fromhex(value.replace(':', ''))
                if payload.startswith(b'\x1f\x8b'):
                    payload = gzip.decompress(payload)
                decoded_bodies.append(payload.decode(errors='replace'))
            except ValueError:
                decoded_bodies.append(value)
    marker = MARKER in proc.stdout or any(MARKER in b for b in decoded_bodies)
    return {'http_requests': len(packets), 'uris': uris, 'marker_visible': marker}
