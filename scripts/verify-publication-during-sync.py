#!/usr/bin/env python3
"""Offline plan by default. Real publication overlapping node3 readmission.

Uses an existing published large file only to lengthen real synchronization.
Creates exactly one tiny two-part upload; requires exclusive test-stack access.
A missed syncing/publication window is INCONCLUSIVE, never silently retried.
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
import time
import uuid


def harness():
    spec = importlib.util.spec_from_file_location('failure_harness', Path(__file__).with_name('verify-distributed-failures.py'))
    h = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(h)
    return h


def sql(h, proof, query):
    raw = h.command('docker', 'exec', proof.containers['cockroach-1']['Id'], 'cockroach', 'sql', '--insecure', '--database=acervo_app_test', '--format=csv', '--execute', query)
    return list(csv.DictReader(io.StringIO(raw)))


def snapshot(h, proof, operation):
    rows = sql(h, proof, f"""SELECT clock_timestamp() AS observed_at,n.state,n.transitioned_at,
 n.storage_generation::STRING AS storage_generation,n.synced_publication_generation,
 g.publication_generation,u.status,f.id::STRING AS file_id,f.published_at
 FROM cluster_nodes n CROSS JOIN cluster_configuration g
 CROSS JOIN upload_operations u LEFT JOIN files f ON f.operation_id=u.id
 WHERE n.node_id='app-node-3' AND u.id='{operation}'""")
    if len(rows) != 1:
        raise RuntimeError('Missing test node/operation')
    proof.event('sync_publication_snapshot', **rows[0])
    return rows[0]


def execute(h, proof, args):
    proof.preflight(privileged=True)
    # Existing fixture remains untouched; refuse to create a large one implicitly.
    large = sql(h, proof, "SELECT f.id::STRING,size_bytes FROM files f JOIN upload_operations u ON u.id=f.operation_id WHERE u.status='available' AND size_bytes>=100663296")
    if not large:
        raise RuntimeError('Existing published >=96MiB fixture required; no upload created')
    proof.account()
    proof.select_target(3)
    payload = ('publication-overlap-' + proof.run_id).encode()
    cut = len(payload) // 2
    pieces = [payload[:cut], payload[cut:]]
    digest = hashlib.sha256(payload).hexdigest()
    manifest = {'idempotency_key': str(uuid.uuid4()), 'name': 'sync-publication-' + proof.run_id,
                'size': len(payload), 'sha256': digest, 'parts': [
                    {'index': i, 'offset': 0 if i == 0 else cut, 'size': len(part), 'sha256': hashlib.sha256(part).hexdigest()} for i, part in enumerate(pieces)]}
    operation = None
    try:
        status, body, _ = proof.http(1, '/api/uploads', 'POST', manifest, proof.user_cookie)
        assert status == 201, status
        operation = str(uuid.UUID(json.loads(body)['id']))
        base = '/api/uploads/' + operation
        assert proof.http(1, base + '/parts/0', 'PUT', pieces[0], proof.user_cookie)[0] == 204
        status, body, _ = proof.http(1, base, cookie=proof.user_cookie)
        op = json.loads(body)
        assert status == 200 and op['status'] == 'pending' and not op['parts'][1]['available']
        proof.event('pending_last_part_withheld', operation_id=operation)
        proof.fault('storage')
        proof.wait(lambda: proof.excluded(proof.target_id), 'exclude node3')
        proof.restore()  # Restore storage simulation only, allowing natural StartSync.
        deadline = time.monotonic() + 90
        while True:
            observed = snapshot(h, proof, operation)
            if observed['state'] == 'syncing':
                break
            if observed['state'] == 'ready' or time.monotonic() >= deadline:
                raise RuntimeError('INCONCLUSIVE: syncing window missed; no automatic repetition')
            time.sleep(.1)
        sync_started = observed['transitioned_at']
        before_generation = int(observed['publication_generation'])
        proof.event('last_part_start', sync_started=sync_started, publication_generation=before_generation)
        status, _, _ = proof.http(1, base + '/parts/1', 'PUT', pieces[1], proof.user_cookie, timeout=20)
        proof.event('last_part_response', status=status)
        assert status == 204, 'Last-part result ambiguous or failed; inspect operation before retry'
        after_put = snapshot(h, proof, operation)
        if after_put['state'] != 'syncing' or after_put['transitioned_at'] != sync_started:
            raise RuntimeError('INCONCLUSIVE: PUT was not bracketed by same syncing interval')
        deadline = time.monotonic() + 150
        while True:
            final = snapshot(h, proof, operation)
            if final['state'] == 'ready' and final['status'] == 'available':
                break
            if time.monotonic() >= deadline:
                raise AssertionError('Publication/readmission deadline exceeded')
            time.sleep(.5)
        # SQL timestamps prove publication fell inside the same observed recovery.
        evidence = sql(h, proof, f"""SELECT f.id::STRING AS file_id,f.published_at,n.transitioned_at AS admitted_at,
 f.published_at>='{sync_started}'::TIMESTAMPTZ AND f.published_at<n.transitioned_at AS overlap,
 n.synced_publication_generation,g.publication_generation
 FROM files f CROSS JOIN cluster_nodes n CROSS JOIN cluster_configuration g
 WHERE f.operation_id='{operation}' AND n.node_id='app-node-3'""")[0]
        proof.event('publication_overlap_evidence', **evidence)
        assert evidence['overlap'] in ('t', 'true'), 'INCONCLUSIVE: publication outside syncing interval'
        assert int(evidence['synced_publication_generation']) > before_generation
        copies = sql(h, proof, f"""SELECT n.node_id,c.storage_generation::STRING,n.storage_generation::STRING AS current_generation,
 c.object_key,c.s3_version_id,c.size_bytes,encode(c.sha256,'hex') AS sha256,c.verified_at,
 c.verified_at>='{sync_started}'::TIMESTAMPTZ AS fresh_after_sync
 FROM object_copies c JOIN cluster_nodes n ON n.id=c.node_id
 WHERE c.operation_id='{operation}' AND c.storage_generation=n.storage_generation ORDER BY n.node_id""")
        proof.event('current_copies_after_readmission', copies=copies)
        assert len(copies) == 3 and {r['node_id'] for r in copies} == {f'app-node-{i}' for i in (1, 2, 3)}
        assert all(int(r['size_bytes']) == len(payload) and r['sha256'] == digest for r in copies)
        assert next(r for r in copies if r['node_id'] == 'app-node-3')['fresh_after_sync'] in ('t', 'true')
        for index in (1, 2, 3):
            status, body, _ = proof.http(index, '/api/files/' + evidence['file_id'] + '/download', cookie=proof.user_cookie)
            assert status == 200 and body == payload
        proof.event('downloads_match', nodes=[1, 2, 3], sha256=digest)
    finally:
        proof.restore()
        proof.wait(proof.ready, 'restore all nodes', seconds=150)
        proof.event('restored', operation_id=operation)
    proof.report['complete'] = True
    proof.event('publication_during_sync_pass')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    parser.add_argument('--report', default='/tmp/acervo-publication-sync-report.json')
    parser.add_argument('--journal', default='/tmp/acervo-publication-sync-journal.json')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    args = parser.parse_args()
    if not args.execute and not args.recover:
        print('OFFLINE PLAN: withhold tiny final part; exclude/restore node3; PUT while syncing; prove publication timestamp overlap and fresh copies. No services contacted.')
        return
    args.phase, args.start_node, args.start_mode = 'publication-sync', 3, 'storage'
    with open('/tmp/acervo-distributed-failures.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        h = harness()
        proof = h.Proof(args)
        path = Path(args.journal)
        if args.recover:
            data = json.loads(path.read_text())
            proof.run_id, proof.actions = data['run_id'], data['actions']
            proof.restore()
            proof.wait(proof.ready, 'recovered stack', seconds=150)
            return
        if path.exists() and any(not a.get('restored') for a in json.loads(path.read_text())['actions']):
            raise RuntimeError('Pending restoration: --recover first')
        def interrupted(*_):
            raise KeyboardInterrupt('Interrupted; restoring')
        signal.signal(signal.SIGTERM, interrupted)
        execute(h, proof, args)


if __name__ == '__main__':
    main()
