"""Create a local-only probe by replacing the pinned ELF's JavaScript entrypoint.

The native executable prefix is unchanged. Never distribute the resulting binary
or use it as an unmodified CLI sample. Run it only in the network-none lab.
"""
import argparse
import hashlib
import json
import struct
from pathlib import Path

PINS = {
    'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3': '2.1.292',
    '4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358': '2.1.295',
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path, help='A new, empty output directory')
    args = parser.parse_args()
    source = args.binary.read_bytes()
    digest = hashlib.sha256(source).hexdigest()
    assert digest in PINS, 'Unsupported binary'
    assert source[:4] == b'\x7fELF'
    data = bytearray(source)
    meta = data.rfind(b'\n---- Bun! ----\n') - 32
    size, table, table_size, entry, _, _, _ = struct.unpack_from('<QIIIIII', data, meta)
    base = meta - size
    assert table_size % 52 == 0 and entry < table_size // 52
    record = base + table + 52 * entry
    values = struct.unpack_from('<12I4B', data, record)
    assert data[base + values[0]:base + values[0] + values[1]] == b'/$bunfs/root/cli'
    assert data[base + values[2]:base + values[2] + 4] != bytes.fromhex('28b52ffd'), 'Compressed entrypoint unsupported'
    here = Path(__file__).resolve().parent
    code = (here / 'cch_runtime_probe.js').read_bytes()
    assert len(code) <= values[3]
    data[base + values[2]:base + values[2] + values[3]] = code + b' ' * (values[3] - len(code))
    struct.pack_into('<II', data, record + 24, 0, 0)  # Disable only entrypoint bytecode.
    assert source[:base] == data[:base]
    args.output.mkdir(parents=True, exist_ok=False)
    binary = args.output / 'claude-runtime-probe'
    binary.write_bytes(data)
    binary.chmod(0o755)
    fixture = here.parent.parent / 'backend/internal/pkg/claude/testdata/cch-2.1.292.json'
    vectors = json.loads(fixture.read_text())['vectors']
    (args.output / 'cases.json').write_text(json.dumps(vectors, ensure_ascii=False, indent=2) + '\n')
    report = {'cli_version': PINS[digest], 'official_sha256': digest, 'instrumented_sha256': hashlib.sha256(data).hexdigest(),
              'entry_source_offset': base + values[2], 'entry_source_length': values[3],
              'bytecode_record_offset': record + 24, 'payload_offset': base,
              'native_prefix_unchanged': True, 'native_prefix_sha256': hashlib.sha256(source[:base]).hexdigest(),
              'probe_script_sha256': hashlib.sha256(code).hexdigest()}
    (args.output / 'runtime-probe-provenance.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    main()
