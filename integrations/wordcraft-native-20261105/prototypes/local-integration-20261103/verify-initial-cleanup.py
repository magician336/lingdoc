"""Read-only ownership audit for the single failed sandbox model probe."""
import json
import os
from pathlib import Path
from datetime import datetime, timezone

target = Path(__file__).resolve().parent / 'artifacts/workflow-model/2026-11-03T10-05-55-165Z/runtime'
marker = str(target)
matches = []
denied = []
for process in Path('/proc').iterdir():
    if not process.name.isdigit() or int(process.name) == os.getpid():
        continue
    try:
        cmd = (process / 'cmdline').read_bytes().replace(b'\0', b' ').decode('utf-8', errors='replace')
        # Read only selected ownership fields; never serialize the environment.
        fields = (process / 'environ').read_bytes().split(b'\0')
        owner = {}
        for field in fields:
            key, _, value = field.partition(b'=')
            if key in (b'TMPDIR', b'LINGDOC_READER_PID_FILE'):
                decoded = value.decode('utf-8', errors='replace')
                if decoded.startswith(marker + '/'):
                    owner[key.decode()] = decoded
        if marker in cmd or owner:
            stat = (process / 'stat').read_text()
            start_ticks = stat[stat.rfind(')') + 2:].split()[19]
            matches.append({'pid': int(process.name), 'command': cmd, 'start_ticks': start_ticks, 'ownership_fields': owner})
    except FileNotFoundError:
        pass
    except PermissionError:
        denied.append(int(process.name))
result = {'checked_at': datetime.now(timezone.utc).isoformat(), 'target': marker, 'matching_processes': matches, 'permission_denied_pids': denied, 'killed_processes': [], 'read_only': True}
output = target.parent / 'cleanup-wsl.json'
output.write_text(json.dumps(result, indent=2), encoding='utf-8')
print(json.dumps(result))
