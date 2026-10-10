"""Check that captured opaque thinking survives a production Chat round trip."""
import json
from pathlib import Path
import sys


def objects(value):
    if isinstance(value, dict):
        yield value
        for child in value.values(): yield from objects(child)
    elif isinstance(value, list):
        for child in value: yield from objects(child)


rows = json.loads(Path(sys.argv[1]).read_text())
assert rows
failures = []
for row in rows:
    actual = list(objects(row['next_anthropic_request']))
    missing = [block for block in row['source_opaque']
               if not any(all(value.get(k) == v for k, v in block.items()) for value in actual)]
    if missing:
        failures.append({'name': row['name'], 'model': row['model'], 'blocks': len(missing)})
result = {'cases': len(rows), 'opaque_round_trip_failures': len(failures),
          'affected_models': sorted(set(x['model'] for x in failures)),
          'conversion_errors': sum(bool(x.get('error')) for x in rows),
          'component_round_trip_not_live_provider': True, 'failures': failures}
Path(sys.argv[2]).write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps({k: v for k, v in result.items() if k != 'failures'}))
sys.exit(bool(failures))
