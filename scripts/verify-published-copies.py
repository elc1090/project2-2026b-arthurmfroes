#!/usr/bin/env python3
"""Offline by default. Prove current SQL receipts on isolated MinIO sites.

Run with /tmp/acervo-s3-proof-venv/bin/python; boto3 is test-only.
Imports the sibling failure harness; never uploads or deletes objects/volumes.
"""
import argparse
import csv
import fcntl
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import signal
import uuid


def load_harness():
    spec = importlib.util.spec_from_file_location('failure_harness', Path(__file__).with_name('verify-distributed-failures.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def add_minio(h, proof):
    config = json.loads(h.command('docker', 'compose', '-f', proof.args.infra_compose, 'config', '--format', 'json'))
    for index in (1, 2, 3):
        name = f'minio-{index}'
        cid = h.command('docker', 'compose', '-f', proof.args.infra_compose, 'ps', '--all', '-q', name).strip()
        data = h.inspect(cid)
        labels = data['Config']['Labels']
        if labels.get('com.docker.compose.project') != h.INFRA or labels.get('com.docker.compose.service') != name:
            raise RuntimeError('Foreign MinIO service')
        ports = config['services'][name]['ports']
        if not any(str(p.get('published')) == str(27900 + index) and p.get('host_ip') == '127.0.0.1' and p.get('target') == 9000 for p in ports):
            raise RuntimeError('Unexpected S3 test port')
        proof.containers[name] = data


def restore_minio(h, proof):
    # Never start applications here: all three sites must be restored first.
    for action in reversed(proof.actions):
        if action.get('restored') or not action['service'].startswith('minio-'):
            continue
        current = h.inspect(action['id'])
        labels = current['Config']['Labels']
        if current['Created'] != action['created'] or labels.get('com.docker.compose.project') != h.INFRA or labels.get('com.docker.compose.service') != action['service']:
            raise RuntimeError('Replaced/foreign MinIO during restoration')
        if not current['State']['Running']:
            h.command('docker', 'start', action['id'])
        action['restored'] = True
        proof.save_journal()


def s3_ready(client):
    try:
        client.list_buckets()
        return True
    except Exception:
        return False


def receipts(h, proof, file_id):
    sql = f"""SELECT c.node_id::STRING AS node_uuid,n.node_id,c.object_key,c.s3_version_id,
 c.size_bytes,encode(c.sha256,'hex') AS sha256,c.storage_generation::STRING AS receipt_generation,
 n.storage_generation::STRING AS current_generation
 FROM files f JOIN upload_operations u ON u.id=f.operation_id
 JOIN object_copies c ON c.operation_id=u.id JOIN cluster_nodes n ON n.id=c.node_id
 WHERE f.id='{file_id}' AND u.status='available'
 AND c.storage_generation=n.storage_generation ORDER BY n.node_id"""
    raw = h.command('docker', 'exec', proof.containers['cockroach-1']['Id'], 'cockroach', 'sql', '--insecure', '--database=acervo_app_test', '--format=csv', '--execute', sql)
    rows = list(csv.DictReader(io.StringIO(raw)))
    if len(rows) != 3 or {r['node_id'] for r in rows} != {'app-node-1', 'app-node-2', 'app-node-3'}:
        raise RuntimeError('Expected exactly three current-generation receipts for published file')
    if len({(r['size_bytes'], r['sha256']) for r in rows}) != 1:
        raise RuntimeError('Receipt content disagreement')
    proof.event('published_current_receipts', file_id=file_id, receipts=rows)
    return rows


def execute(h, proof, args):
    import boto3
    from botocore.config import Config
    file_id = str(uuid.UUID(args.file_id))
    proof.preflight(privileged=True)
    env = json.loads(Path(args.app_compose).read_text())['services']['app-node-1']['environment']
    clients = {i: boto3.client('s3', endpoint_url=f'http://127.0.0.1:{27900+i}',
                 aws_access_key_id=env['S3_ACCESS_KEY'], aws_secret_access_key=env['S3_SECRET_KEY'],
                 region_name='us-east-1', config=Config(connect_timeout=5, read_timeout=20, retries={'max_attempts': 0}, s3={'addressing_style': 'path'})) for i in (1, 2, 3)}
    try:
        # Journal apps first, then storage. Reverse restore order preserves this barrier.
        for i in (1, 2, 3):
            proof.mutate_container('stop', f'app-node-{i}')
        if any(h.inspect(proof.containers[f'app-node-{i}']['Id'])['State']['Running'] for i in (1, 2, 3)):
            raise RuntimeError('All app processes must be stopped before isolating storage')
        rows = receipts(h, proof, file_id)
        for index in (1, 2, 3):
            for peer in (1, 2, 3):
                if peer != index:
                    proof.mutate_container('stop', f'minio-{peer}')
            for peer in (1, 2, 3):
                running = h.inspect(proof.containers[f'minio-{peer}']['Id'])['State']['Running']
                if running != (peer == index):
                    raise RuntimeError('Site isolation not established')
            row = next(r for r in rows if r['node_id'] == f'app-node-{index}')
            result = clients[index].get_object(Bucket=env['S3_BUCKET'], Key=row['object_key'], VersionId=row['s3_version_id'])
            body = result['Body']
            digest, size = hashlib.sha256(), 0
            try:
                while True:
                    chunk = body.read(1024 * 1024)
                    if not chunk:
                        break
                    size += len(chunk)
                    digest.update(chunk)
            finally:
                body.close()
            assert result.get('VersionId') == row['s3_version_id'], 'Physical version differs'
            assert size == result['ContentLength'] == int(row['size_bytes']), 'Size differs'
            assert digest.hexdigest() == row['sha256'], 'SHA256 differs'
            proof.event('isolated_site_verified', site=index, version_id=row['s3_version_id'], bytes=size, sha256=digest.hexdigest(), peers_stopped=True)
            restore_minio(h, proof)
            for peer in (1, 2, 3):
                proof.wait(lambda peer=peer: s3_ready(clients[peer]), f'S3 site {peer} responding', seconds=60)
    finally:
        # If storage restoration fails, do NOT proceed to application restoration.
        restore_minio(h, proof)
        for peer in (1, 2, 3):
            proof.wait(lambda peer=peer: s3_ready(clients[peer]), f'restore S3 {peer}', seconds=60)
        proof.restore()
        proof.wait(proof.ready, 'restore applications', seconds=150)
        proof.event('restored', all_actions_restored=all(a.get('restored') for a in proof.actions))
    proof.report['complete'] = True
    proof.event('three_physical_copies_pass')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--file-id')
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    parser.add_argument('--report', default='/tmp/acervo-published-copies-report.json')
    parser.add_argument('--journal', default='/tmp/acervo-published-copies-journal.json')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    args = parser.parse_args()
    if not args.execute and not args.recover:
        print('OFFLINE PLAN: stop apps; query published receipts; isolate/read/hash each site; restore storage before apps. No services contacted.')
        return
    if args.execute and not args.file_id:
        parser.error('--execute requires --file-id')
    args.phase, args.start_node, args.start_mode = 'published-copies', 1, 'backend'
    with open('/tmp/acervo-distributed-failures.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        h = load_harness()
        proof = h.Proof(args)
        add_minio(h, proof)
        journal = Path(args.journal)
        if args.recover:
            data = json.loads(journal.read_text())
            proof.run_id, proof.actions = data['run_id'], data['actions']
            restore_minio(h, proof)
            proof.restore()
            proof.wait(proof.ready, 'recovered stack', seconds=150)
            return
        if journal.exists() and any(not a.get('restored') for a in json.loads(journal.read_text())['actions']):
            raise RuntimeError('Pending journal: --recover before another run')
        def interrupted(*_):
            raise KeyboardInterrupt('Interrupted; restoring test services')
        signal.signal(signal.SIGTERM, interrupted)
        execute(h, proof, args)


if __name__ == '__main__':
    main()
