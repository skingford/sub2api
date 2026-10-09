"""Run an existing isolated lab with /work on tmpfs, then export all evidence.

Mount an empty host output at /output and, if needed, cases.json at /seed/cases.json.
Use docker --tmpfs /work:rw,size=256m; avoid packet-flush latency on host bind mounts.
"""
from pathlib import Path
import runpy
import shutil
import sys

seed = Path('/seed/cases.json')
if seed.exists():
    shutil.copyfile(seed, '/work/cases.json')
entry = sys.argv[1]
sys.argv = sys.argv[1:]
try:
    runpy.run_path(entry, run_name='__main__')
finally:
    shutil.copytree('/work', '/output', dirs_exist_ok=True)
