"""Package this integration slice without runtime data, dependencies or links."""
import hashlib
import json
import stat
import zipfile
from datetime import datetime, timezone
from pathlib import Path

root = Path(__file__).resolve().parent
files = []


def collect(target):
    info = target.lstat()
    if info.st_file_attributes & stat.FILE_ATTRIBUTE_REPARSE_POINT:
        raise RuntimeError(f'Refusing reparse point: {target}')
    if target.is_dir():
        for child in sorted(target.iterdir()):
            collect(child)
    elif target.is_file():
        files.append(target)
    else:
        raise RuntimeError(f'Not a regular file: {target}')


for name in [
    'START-HERE.md', 'README.md', '试用与验收.md', 'CHAIN-API.md',
    'Kimi-链路接线回执.md', 'package.json', 'server.mjs', 'go-sources.mjs',
    'chain-store.mjs', 'chain-store.test.mjs', 'native-adapter.js',
    'native-adapter.d.ts', 'native-adapter.test.mjs', 'seed-document.py',
    'browser-client.mjs', 'verify-chain-browser.mjs', 'verify-reopen-browser.mjs',
    'verify-final-ui.mjs', 'verify-gateway.mjs', 'verify-real.mjs',
    'Start-LocalTrial.ps1', 'Stop-LocalTrial.ps1', 'verify-chain-handoff.mjs',
    'package-handoff.py',
]:
    collect(root / name)
for name in [
    '.gitignore', 'components.json', 'eslint.config.js', 'index.html',
    'LD-V1前端实施与接口映射.md', 'LOCAL-INTEGRATION.md', 'package-lock.json',
    'package.json', 'postcss.config.js', 'README.md', 'tailwind.config.js',
    'tsconfig.app.json', 'tsconfig.json', 'tsconfig.node.json', 'vite.config.ts',
    'src', 'public', 'handoff', 'dist',
]:
    collect(root / 'frontend' / name)
evidence = [
    'artifacts/final-unit-tests.txt',
    'artifacts/final-gateway-verification.json',
    'artifacts/chain-2026-11-03T05-34-27.166Z/verification.json',
    'artifacts/reopen-2026-11-03T05-38-17.579Z/verification.json',
    'artifacts/final-2026-11-03T05-44-09.465Z/verification.json',
    'artifacts/final-2026-11-03T05-48-57.958Z/verification.json',
]
for name in evidence:
    collect(root / name)
manifest = {
    'baselineId': 'LD-V1-20261103-R1',
    'componentVersion': 'review-chain-0.2.0',
    'packagedAt': datetime.now(timezone.utc).isoformat(),
    'sourceZipSha256': '2908ce602a3e68b16376c27cab1861de41c3acc86d765678b671919939d9db8c',
    'baseCodeCommit': '5269e58f561bcb973d63a7623f2d076e8d943c8e',
    'codeCommitted': False,
    'newFrontendConnectedLocally': True,
    'localTrialOrigin': 'http://127.0.0.1:7210',
    'cloudKimiAcceptance': 'pending',
    'fullSyncComplete': False,
    'productionDeployed': False,
    'verification': {
        'unitPassed': 24, 'gatewayPassed': 10,
        'mainChain': {'passed': False, 'businessChecksPassed': 14, 'remainingFailure': 'reload harness race; retained original result'},
        'restartChecksPassed': 11, 'finalOpinionChecksPassed': 5,
        'cleanTrialReadonlyChecksPassed': 3,
        'typescriptBuild': 'passed', 'eslint': 'passed',
        'humanTrial': 'pending', 'fullCI': 'not_run', 'evidence': evidence,
    },
    'runtimeRequirements': 'Existing LingDoc repository, Windows Node/Python, WSL LingDoc-Beta-R29-Ubuntu2404, validated /tmp/lingdoc-local-source-ai and native assets; not a standalone product installer',
    'files': [{'path': p.relative_to(root).as_posix(), 'sha256': hashlib.sha256(p.read_bytes()).hexdigest()} for p in sorted(files)],
}
manifest_path = root / 'CHAIN-INTEGRATION.json'
manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
archive = root / 'LingDoc-R1-真实链路接线-0.2.0.zip'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
    for p in [*files, manifest_path]:
        output.write(p, p.relative_to(root).as_posix())
with zipfile.ZipFile(archive) as output:
    assert output.testzip() is None
    assert len(output.namelist()) == len(files) + 1
    for item in manifest['files']:
        assert hashlib.sha256(output.read(item['path'])).hexdigest() == item['sha256']
print(json.dumps({'archive': str(archive), 'bytes': archive.stat().st_size, 'files': len(files), 'sha256': hashlib.sha256(archive.read_bytes()).hexdigest(), 'archiveVerified': True}, ensure_ascii=False))
