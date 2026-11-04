import collections
import datetime
import json
import os
import pathlib
import re
import subprocess
import sys
import time

artifact = pathlib.Path('/artifacts/pr755-66')
source = pathlib.Path('/ci-src')
plan = json.loads((artifact / 'plan.json').read_text(encoding='utf-8'))
group = sys.argv[1]
head = subprocess.check_output(['git', '-c', 'safe.directory=/ci-src', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
if head != plan['code_sha']:
    raise SystemExit('Source head differs from the fixed acceptance target')
if group == 'postgres-fault':
    previous = json.loads((artifact / 'postgres-result.json').read_text(encoding='utf-8'))
    pending = {(row['package'], row['test']) for row in previous['targets'] if row['status'] == 'SKIP'}
    targets = [row for row in plan['targets'] if (row['package'], row['test']) in pending]
else:
    targets = [row for row in plan['targets'] if row['group'] == group]
if not targets:
    raise SystemExit('Unknown/empty group')
if group == 'permission' and os.geteuid() == 0:
    raise SystemExit('Permission regression must run as an unprivileged user')
env = dict(os.environ, GOMODCACHE='/opt/local-go-mod', GOCACHE='/opt/local-go-build', GOPROXY='off')
if group == 'permission':
    env['GOCACHE'] = '/tmp/pr755-nobody-go-cache'
packages = collections.defaultdict(list)
for target in targets:
    packages[target['package']].append(target['test'])
commands = []
actions, messages = {}, {}
results = []
started = datetime.datetime.now(datetime.timezone.utc).isoformat()
log_path = artifact / (group + '.jsonl')

def publish():
    data = {'code_sha': head, 'group': group, 'started_at': started,
            'recorded_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'uid': os.geteuid(), 'commands': commands, 'targets': results,
            'counts': dict(collections.Counter(row['status'] for row in results))}
    data['result'] = 'PASS' if len(results) == len(targets) and all(row['status'] == 'PASS' for row in results) and all(row['exit_code'] == 0 for row in commands) else 'INCOMPLETE_OR_FAIL'
    (artifact / (group + '-result.json')).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    return data

with log_path.open('w', encoding='utf-8') as log:
    for package, tests in sorted(packages.items()):
        roots = sorted({test.split('/')[0] for test in tests})
        pattern = '^(' + '|'.join(re.escape(name) for name in roots) + ')$'
        if group == 'permission':
            cmd = ['go', 'tool', 'test2json', '-t', '-p', package, str(artifact / 'permission.test'), '-test.v', '-test.run', pattern]
        else:
            cmd = ['go', 'test', '-race', '-json', '-count=1', '-p=1', '-timeout=15m', '-run', pattern, package]
        print(group, package, 'targets=' + str(len(tests)), flush=True)
        began = time.monotonic()
        proc = subprocess.Popen(cmd, cwd=source, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding='utf-8', errors='replace')
        for line in proc.stdout:
            log.write(line)
            log.flush()
            try:
                item = json.loads(line)
            except json.JSONDecodeError:
                continue
            test = item.get('Test')
            if not test:
                continue
            key = (item.get('Package', package), test)
            if item.get('Action') == 'output':
                output = item.get('Output', '').strip()
                if not output.startswith(('=== RUN', '--- SKIP', '--- PASS', '--- FAIL')):
                    messages[key] = output
            if item.get('Action') in ('pass', 'fail', 'skip'):
                actions[key] = item['Action']
        exit_code = proc.wait()
        commands.append({'package': package, 'command': cmd, 'exit_code': exit_code, 'elapsed_seconds': round(time.monotonic() - began, 3)})
        for test in tests:
            key = (package, test)
            status = {'pass': 'PASS', 'skip': 'SKIP', 'fail': 'FAIL'}.get(actions.get(key), 'NOT_RUN')
            results.append({'package': package, 'test': test, 'status': status, 'last_diagnostic': messages.get(key, '')})
        publish()

data = publish()
print(json.dumps({'group': group, 'result': data['result'], 'counts': data['counts']}, ensure_ascii=False), flush=True)
raise SystemExit(0 if data['result'] == 'PASS' else 1)
