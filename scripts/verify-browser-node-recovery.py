#!/usr/bin/env python3
"""Prepare browser re-selection proof on existing isolated stack; default is offline."""
import argparse
import datetime
import fcntl
import hashlib
import hmac
import http.client
import importlib.util
import json
import os
from pathlib import Path
import signal
import stat
import time
import urllib.parse


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def expected_key(operation):
    parts = operation.get('parts', [])
    if operation.get('status') != 'pending' or operation.get('size') != 65 << 20:
        raise ValueError('Expected own pending 65 MiB fixture')
    if [(p['index'], p['size'], p['offset']) for p in parts] != [(0, 32 << 20, 0), (1, 32 << 20, 32 << 20), (2, 1 << 20, 64 << 20)]:
        raise ValueError('Unexpected fixture manifest')
    digest = parts[1]['sha256']
    if len(digest) != 64 or any(c not in '0123456789abcdef' for c in digest):
        raise ValueError('Invalid part hash')
    return 'parts/'+operation['id']+'/1/'+digest


def availability(operation, values):
    parts = operation.get('parts', [])
    return operation.get('status') == 'pending' and len(parts) == 3 and all(
        part['index'] == index and part.get('availability') == value
        and part.get('available') == (value == 'available')
        for index, (part, value) in enumerate(zip(parts, values)))


def delete_version(index, bucket, key, version, access, secret):
    # VersionId is mandatory: never issue an unversioned delete or bucket action.
    if not version or version == 'null':
        raise ValueError('Refusing unversioned deletion')
    host = f'127.0.0.1:{27900+index}'
    path = '/'+urllib.parse.quote(bucket, safe='')+'/'+urllib.parse.quote(key, safe='/')
    query = urllib.parse.urlencode({'versionId': version}, quote_via=urllib.parse.quote)
    now = datetime.datetime.now(datetime.timezone.utc)
    timestamp, date = now.strftime('%Y%m%dT%H%M%SZ'), now.strftime('%Y%m%d')
    empty = hashlib.sha256(b'').hexdigest()
    headers_text = f'host:{host}\nx-amz-content-sha256:{empty}\nx-amz-date:{timestamp}\n'
    signed = 'host;x-amz-content-sha256;x-amz-date'
    canonical = '\n'.join(['DELETE', path, query, headers_text, signed, empty])
    scope = date+'/us-east-1/s3/aws4_request'
    data = 'AWS4-HMAC-SHA256\n'+timestamp+'\n'+scope+'\n'+hashlib.sha256(canonical.encode()).hexdigest()
    signing = ('AWS4'+secret).encode()
    for step in (date, 'us-east-1', 's3', 'aws4_request'):
        signing = hmac.new(signing, step.encode(), hashlib.sha256).digest()
    signature = hmac.new(signing, data.encode(), hashlib.sha256).hexdigest()
    conn = http.client.HTTPConnection('127.0.0.1', 27900+index, timeout=5)
    try:
        conn.request('DELETE', path+'?'+query, headers={'x-amz-date': timestamp, 'x-amz-content-sha256': empty,
            'Authorization': f'AWS4-HMAC-SHA256 Credential={access}/{scope}, SignedHeaders={signed}, Signature={signature}'})
        response = conn.getresponse()
        response.read()
        if response.status != 204:
            raise RuntimeError('Exact version deletion failed: '+str(response.status))
    finally:
        conn.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--operation')
    parser.add_argument('--cookie-file', type=Path)
    parser.add_argument('--harness-dir', type=Path, default=Path(__file__).parent)
    parser.add_argument('--journal', default='/tmp/acervo-browser-node-journal.json')
    parser.add_argument('--report', default='/tmp/acervo-browser-node-report.json')
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    mode.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.self_test:
        sample = {'id': 'test-op', 'size': 65 << 20, 'status': 'pending', 'parts': [
            {'index': i, 'size': size, 'offset': i*(32 << 20), 'sha256': 'a'*64,
             'availability': 'available' if i < 2 else 'missing', 'available': i < 2}
            for i, size in enumerate((32 << 20, 32 << 20, 1 << 20))]}
        assert expected_key(sample) == 'parts/test-op/1/'+'a'*64
        assert availability(sample, ['available', 'available', 'missing'])
        assert not availability(sample, ['available', 'missing', 'missing'])
        sample['parts'][1]['availability'] = 'unknown'
        assert not availability(sample, ['available', 'missing', 'missing'])
        print('PASS: exact part selection and authoritative availability; no infrastructure contacted')
        return
    if not args.execute and not args.recover:
        print('Prepared only: stop actual worker, delete only fixture part-1 versions, require API available/missing/missing. No infrastructure contacted.')
        return
    worker = module('worker_proof', args.harness_dir/'verify-worker-recovery.py')
    harness = module('failure_proof', args.harness_dir/'verify-distributed-failures.py')
    args.phase, args.start_node, args.start_mode = 'browser', 1, 'backend'
    with open(worker.LOCK, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        proof = harness.Proof(args)
        journal = Path(args.journal)
        if args.recover:
            state = json.loads(journal.read_text())
            proof.run_id, proof.actions = state['run_id'], state['actions']
            if worker.disk_free() < worker.DISK_FLOOR:
                raise RuntimeError('Refusing backend restart below 6 GiB; coordinator must resolve capacity')
            proof.restore()
            proof.wait(proof.ready, 'three restored nodes', seconds=90)
            return
        if journal.exists() and any(not a.get('restored') for a in json.loads(journal.read_text())['actions']):
            raise RuntimeError('Previous backend action pending; recover first')
        if not args.cookie_file or stat.S_IMODE(args.cookie_file.stat().st_mode) != 0o600:
            raise RuntimeError('Private mode0600 cookie file required')
        operation = worker.identifier(args.operation)
        proof.user_cookie = args.cookie_file.read_text().strip()
        initial = worker.disk_free()
        if initial < worker.DISK_INITIAL:
            raise RuntimeError('At least 7.5 GiB required before fixture')
        proof.preflight(privileged=True)
        def get_operation():
            for index in (1, 2, 3):
                try:
                    code, raw, _ = proof.http(index, '/api/uploads/'+operation, cookie=proof.user_cookie, timeout=5)
                    if code == 200:
                        value = json.loads(raw)
                        if value['id'] != operation:
                            raise RuntimeError('Operation mismatch')
                        return value
                except (OSError, TimeoutError):
                    pass
            return None
        value = get_operation()
        if value is None:
            raise RuntimeError('No authoritative operation response')
        key = expected_key(value)
        if not availability(value, ['available', 'available', 'missing']):
            raise RuntimeError('First session must receive0/1 and never send2')
        rows = worker.sql(f"SELECT count(*) AS copies FROM upload_part_copies WHERE operation_id='{operation}' AND part_index=2")
        if rows[0]['copies'] != '0':
            raise RuntimeError('Part2 has a receipt; not the intended fixture')
        envs = json.loads(Path(args.app_compose).read_text())['services']
        deletion_path = Path(args.journal+'.versions.json')
        deletion = {'operation': operation, 'key': key, 'deletions': []}
        success = False
        for sig in (signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        try:
            def active_holder():
                found = worker.sql(f"SELECT lease_holder::STRING AS holder,lease_generation AS generation FROM upload_operations WHERE id='{operation}' AND status='pending' AND lease_holder IS NOT NULL AND lease_expires_at>clock_timestamp()")
                return found[0] if len(found) == 1 else False
            owner = proof.wait(active_holder, 'live coordinator claim', seconds=30)
            index = next(i for i, node in proof.node_ids.items() if node == owner['holder'])
            proof.event('worker_claim_observed', operation=operation, **owner)
            proof.mutate_container('stop', f'app-node-{index}')
            proof.event('worker_process_stopped', operation=operation, service=f'app-node-{index}')
            namespace = {'s': 'http://s3.amazonaws.com/doc/2006-03-01/'}
            deadline = time.monotonic()+60
            while time.monotonic() < deadline:
                if worker.disk_exceeded(initial, worker.disk_free()):
                    raise RuntimeError('Fixture disk budget exceeded')
                for site in (1, 2, 3):
                    env = envs[f'app-node-{site}']['environment']
                    xml = worker.s3_get(site, env['S3_BUCKET'], '', {'versions': '', 'prefix': key}, env['S3_ACCESS_KEY'], env['S3_SECRET_KEY'])
                    if xml.findtext('s:IsTruncated', default='false', namespaces=namespace) != 'false':
                        raise RuntimeError('Refusing truncated version inventory')
                    for item in xml.findall('s:Version', namespace):
                        if item.findtext('s:Key', namespaces=namespace) != key:
                            continue
                        version = item.findtext('s:VersionId', namespaces=namespace)
                        entry = {'site': site, 'key': key, 'version_id': version, 'deleted': False}
                        deletion['deletions'].append(entry)
                        worker.atomic_private(deletion_path, deletion)
                        delete_version(site, env['S3_BUCKET'], key, version, env['S3_ACCESS_KEY'], env['S3_SECRET_KEY'])
                        entry['deleted'] = True
                        worker.atomic_private(deletion_path, deletion)
                value = get_operation()
                if value is not None and availability(value, ['available', 'missing', 'missing']):
                    if not any(entry['deleted'] for entry in deletion['deletions']):
                        raise RuntimeError('No actual fixture version deletion confirmed')
                    proof.event('browser_resume_prepared', operation=operation, stopped_service=f'app-node-{index}', parts=value['parts'])
                    success = True
                    break
                time.sleep(1)
            if not success:
                raise RuntimeError('Authoritative missing state not reached')
        finally:
            if not success:
                terminal = False
                for i in (1, 2, 3):
                    try:
                        code, raw, _ = proof.http(i, '/api/uploads/'+operation+'/cancel', 'POST', {}, proof.user_cookie, timeout=5)
                        if code == 200 and json.loads(raw).get('status') in ('cancelled', 'available', 'failed'):
                            terminal = True
                            break
                    except Exception:
                        pass
                proof.event('fixture_failure_containment', terminal_confirmed=terminal, operation=operation)
                if terminal and worker.disk_free() >= worker.DISK_FLOOR:
                    proof.restore()
                else:
                    for i in (1, 2, 3):
                        service = f'app-node-{i}'
                        if any(a.get('service') == service and not a.get('restored') for a in proof.actions):
                            continue
                        proof.mutate_container('pause', service)
                    proof.event('coordinator_required', reason='Backends stopped/paused; no automatic restart')
        print('Prepared for second browser session; node intentionally stopped. Run --recover after browser proof.')


if __name__ == '__main__':
    main()
