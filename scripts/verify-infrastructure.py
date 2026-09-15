#!/usr/bin/env python3
"""Check Compose and bootstrap control flow; this does not run real services."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def check(condition, message):
    if not condition:
        raise AssertionError(message)


config = json.loads(subprocess.check_output(
    ['docker', 'compose', '--file', str(ROOT / 'docker-compose.dev.yml'),
     'config', '--format', 'json'], text=True))
services = config['services']
volumes = []
for n in range(1, 4):
    backend = services[f'backend-node-{n}']
    env = backend['environment']
    check(f'@cockroach-{n}:26257/' in env['DATABASE_URL'], 'SQL must use local node')
    check(env['S3_ENDPOINT'] == f'http://minio-{n}:9000', 'S3 must use local node')
    check(not backend.get('depends_on'), 'Backend startup cannot require global bootstrap')
    for component in ['cockroach', 'minio']:
        mounts = services[f'{component}-{n}']['volumes']
        volumes.extend(m['source'] for m in mounts if m['type'] == 'volume')
check(len(volumes) == len(set(volumes)) == 6, 'Expected six independent volumes')
print('PASS: Compose local endpoints, six volumes, independent backend startup')

with tempfile.TemporaryDirectory(prefix='acervo-bootstrap-') as temp:
    work = Path(temp)
    # Simulate the documented mc table and command failures, not storage behavior.
    mc = work / 'mc'
    mc.write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
args = sys.argv[1:]
if args[0] == '--no-color': args = args[1:]
case = os.environ['CASE']
state = Path(os.environ['TEST_DIR']) / 'configured'
with (Path(os.environ['TEST_DIR']) / 'calls').open('a') as f:
    f.write(' '.join(args) + '\\n')
if args[:2] == ['alias', 'set']:
    sys.exit(1 if case == 'offline' and args[2] == 'minio-3' else 0)
if args[:3] == ['admin', 'replicate', 'add']:
    state.touch()
    sys.exit(0)
if args[:3] == ['admin', 'replicate', 'info']:
    if case == 'cold' and not state.exists() or case == 'mixed' and args[3] == 'minio-3':
        print('SiteReplication is not enabled')
    elif case == 'unknown':
        print('new unknown format')
    else:
        print('SiteReplication enabled for:\\nDeployment ID | Site Name | Endpoint | Sync')
        peers = [1, 2] if case == 'missing' else [1, 2, 3]
        if case == 'extra': peers.append(4)
        if case == 'duplicate': peers = [1, 2, 2]
        for n in peers:
            print(f'id-{n} | minio-{n} | http://minio-{n}:9000 |')
    sys.exit(0)
if args[0] == 'stat' and case == 'bucket-missing': sys.exit(1)
''')
    mc.chmod(0o755)
    env = dict(os.environ, PATH=f'{work}:{os.environ["PATH"]}', TEST_DIR=str(work))
    for case in ['cold', 'warm', 'missing', 'extra', 'duplicate', 'mixed', 'unknown', 'offline', 'bucket-missing']:
        for name in ['calls', 'configured']:
            (work / name).unlink(missing_ok=True)
        result = subprocess.run(['sh', str(ROOT / 'minio/init-dev.sh')],
                                env=dict(env, CASE=case), capture_output=True, text=True)
        check((result.returncode == 0) == (case in ['cold', 'warm']), f'MinIO {case}: {result.stderr}')
        calls = (work / 'calls').read_text()
        check(('replicate add' in calls) == (case == 'cold'), f'Unexpected reconfiguration: {case}')
        print(f'PASS: simulated MinIO {case}')

    cockroach = work / 'cockroach'
    cockroach.write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
args = sys.argv[1:]
case = os.environ['CASE']
state = Path(os.environ['TEST_DIR']) / 'initialized'
with (Path(os.environ['TEST_DIR']) / 'sql-calls').open('a') as f:
    f.write(' '.join(args) + '\\n')
if args[0] == 'init':
    if case == 'unavailable': sys.exit(1)
    state.touch()
    sys.exit(0)
if case == 'warm': sys.exit(0)
if case == 'degraded': sys.exit(0 if 'cockroach-2:' in ' '.join(args) else 1)
sys.exit(0 if case == 'cold' and state.exists() else 1)
''')
    cockroach.chmod(0o755)
    for case in ['cold', 'warm', 'degraded', 'unavailable']:
        for name in ['sql-calls', 'initialized']:
            (work / name).unlink(missing_ok=True)
        result = subprocess.run(['sh', str(ROOT / 'cockroach/init-dev.sh')],
                                env=dict(env, CASE=case), capture_output=True, text=True)
        check((result.returncode == 0) == (case != 'unavailable'), f'SQL {case}: {result.stderr}')
        calls = (work / 'sql-calls').read_text().splitlines()
        check(any(c.startswith('init ') for c in calls) == (case in ['cold', 'unavailable']),
              f'Unexpected SQL initialization: {case}')
        print(f'PASS: simulated CockroachDB {case}')
print('Only configuration and simulated CLI behavior checked; runtime integration is not evaluated here.')
