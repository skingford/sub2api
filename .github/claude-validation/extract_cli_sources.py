"""Read-only extraction of JS records from two measured Linux x64 CLI binaries.

This recovers embedded distribution code, not the original TypeScript. Never
execute the extracted modules or commit their contents. Zstd records need
Python 3.14's compression.zstd or the optional zstandard package.
"""
import argparse
import hashlib
import json
from pathlib import Path
import struct

PINS = {
    '2.1.292': 'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3',
    '2.1.295': '4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358',
}


def decompress(data):
    try:
        from compression import zstd
        return zstd.decompress(data)
    except ImportError:
        try:
            import zstandard
        except ImportError as exc:
            raise RuntimeError('Zstd source records require Python 3.14 or zstandard') from exc
        return zstandard.ZstdDecompressor().decompress(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path, help='New directory; existing evidence is never overwritten')
    args = parser.parse_args()
    binary = args.binary.read_bytes()
    digest = hashlib.sha256(binary).hexdigest()
    version = next((v for v, pin in PINS.items() if pin == digest), None)
    if version is None or binary[:4] != b'\x7fELF':
        raise ValueError('Unsupported binary: no extraction performed')
    marker = binary.rfind(b'\n---- Bun! ----\n')
    if marker < 32:
        raise ValueError('Missing Bun metadata')
    meta = marker - 32
    size, table, table_size, entry, _, _, _ = struct.unpack_from('<QIIIIII', binary, meta)
    base = meta - size
    if base < 0 or table_size % 52 or entry >= table_size // 52 or table + table_size > size:
        raise ValueError('Invalid Bun record table')

    def read_region(offset, length):
        if offset + length > size:
            raise ValueError('Source record exceeds Bun payload')
        return binary[base + offset:base + offset + length]

    records, sources = [], {}
    for at in range(base + table, base + table + table_size, 52):
        value = struct.unpack_from('<12I4B', binary, at)
        name = read_region(value[0], value[1]).decode('utf-8')
        if not (name.endswith('.js') or name.endswith('/cli')):
            continue
        packed = read_region(value[2], value[3])
        compressed = packed.startswith(bytes.fromhex('28b52ffd'))
        source = decompress(packed) if compressed else packed
        source.decode('utf-8')
        local = Path(name).name
        if local in sources:
            raise ValueError('Duplicate source basename: ' + local)
        sources[local] = source
        records.append({'name': name, 'local_file': local, 'offset': base + value[2],
                        'length': value[3], 'sha256': hashlib.sha256(packed).hexdigest(),
                        'source_encoding': 'zstd' if compressed else 'plain',
                        'decoded_size': len(source), 'decoded_sha256': hashlib.sha256(source).hexdigest()})
    args.output.mkdir(parents=True, exist_ok=False, mode=0o700)
    for name, source in sources.items():
        (args.output / name).write_bytes(source)
    manifest = {'version': version, 'binary_sha256': digest,
                'method': 'read-only Bun JS record extraction; no original TypeScript reconstruction',
                'modules': records}
    (args.output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps({'version': version, 'modules': len(records),
                      'zstd_records': sum(r['source_encoding'] == 'zstd' for r in records)}))


if __name__ == '__main__':
    main()
