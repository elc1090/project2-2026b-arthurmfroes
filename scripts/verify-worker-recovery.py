#!/usr/bin/env python3
"""Worker recovery proof on acervo-app-test; default mode performs no I/O.

--execute requires exclusive test infrastructure. It creates its own account and
96 MiB operation, reserves that operation while uploading, and holds an uncommitted
intent on its last part receipt. Positive S3 ListParts evidence and a live readPart waiter on the fixture intent
are required. The same waiter must remain visible after pausing the worker;
CreateMultipartUpload or a dangling multipart after SQL timeout is insufficient.
--recover restores this run's fixture SQL session/reservation and paused backend.
"""
import argparse
import datetime
import fcntl
import hashlib
import hmac
import http.client
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import tempfile
import stat
import threading
import time
import urllib.parse
import uuid
import xml.etree.ElementTree as ET

PART_SIZE = 32 << 20
PART_COUNT = 3
LOCK = '/tmp/acervo-distributed-failures.lock'
SQL_CONTAINER = 'acervo-infra-runtime-cockroach-1-1'
DISK_FLOOR = 6 * (1 << 30)
DISK_GROWTH = 3 * (1 << 29)
DISK_INITIAL = DISK_FLOOR + DISK_GROWTH


def disk_free():
    stats = os.statvfs('/var/lib/docker')
    return stats.f_bavail * stats.f_frsize


def disk_exceeded(initial, current):
    return current < DISK_FLOOR or initial - current >= DISK_GROWTH


def may_restore(budget, current):
    return not budget.get('triggered') or current >= DISK_FLOOR


def atomic_private(path, data):
    path = Path(path)
    temp = path.with_name(path.name+'.tmp-'+uuid.uuid4().hex)
    fd = os.open(temp, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(data, stream, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


def identifier(value):
    return str(uuid.UUID(value))


def manifest(run_id, payload):
    digest = hashlib.sha256()
    for _ in range(PART_COUNT):
        digest.update(payload)
    return {'idempotency_key': str(uuid.uuid4()), 'name': 'worker-proof-'+run_id,
            'size': len(payload)*PART_COUNT, 'sha256': digest.hexdigest(),
            'parts': [{'index': i, 'offset': i*len(payload), 'size': len(payload),
                       'sha256': hashlib.sha256(payload).hexdigest()} for i in range(PART_COUNT)]}


def positive_copy(snapshot, uploaded_bytes, contention=None):
    # An orphan multipart is not evidence of a still-running copy. Require the
    # identified backend's readPart query to be waiting on our fixture intent.
    return bool(snapshot['status'] == 'pending' and int(snapshot['files']) == 0
                and snapshot.get('holder') and int(snapshot['generation']) > 0 and uploaded_bytes > 0
                and contention and contention.get('holder') == snapshot['holder']
                and contention.get('generation') == str(snapshot['generation'])
                and all(contention.get(key) for key in ('blocking_txn', 'waiting_txn', 'query_id', 'lock_key', 'fixture_session')))


def same_wait(before, after):
    keys = ('holder', 'generation', 'blocking_txn', 'waiting_txn', 'query_id', 'lock_key', 'fixture_session')
    return bool(before and after and all(before.get(key) and before.get(key) == after.get(key) for key in keys))


def contention_evidence(operation, fixture, state, proof):
    """Current locks/queries, never historical contention_events.

    Verified against Cockroach v23.2.0 pkg/sql/crdb_internal.go. These virtual
    tables perform RPC fanout: errors or missing rows make this proof inconclusive.
    No application receipt row is read by the observer itself. The unique fixture
    session writes only this operation; its transaction plus the raw lock_key
    identifies the barrier. Pretty keys encode UUID bytes, not UUID strings.
    """
    operation = identifier(operation)
    if identifier(fixture['operation']) != operation:
        raise RuntimeError('Fixture belongs to a different operation')
    worker = next((index for index, node in proof.node_ids.items() if node == state.get('holder')), None)
    if worker is None:
        return None
    address = proof.containers[f'app-node-{worker}']['NetworkSettings']['Networks']['acervo-infra-runtime_default']['IPAddress']
    address = str(ipaddress.IPv4Address(address))
    marker = fixture['application_name']
    if not marker.startswith('workerproof_') or not marker[len('workerproof_'):].isalnum():
        raise RuntimeError('Invalid fixture marker')
    rows = sql(f"""WITH locks AS MATERIALIZED (
      SELECT lock_key,lock_key_pretty,txn_id,granted,contended
      FROM crdb_internal.cluster_locks
      WHERE database_name='acervo_app_test' AND table_name='upload_part_copies'
    )
    SELECT h.txn_id::STRING AS blocking_txn,w.txn_id::STRING AS waiting_txn,
           encode(h.lock_key,'hex') AS lock_key,q.query_id,s.session_id AS fixture_session,
           q.client_address,clock_timestamp()::STRING AS observed_at
    FROM locks h JOIN locks w ON h.lock_key=w.lock_key
    JOIN crdb_internal.cluster_sessions s ON s.kv_txn=h.txn_id::STRING
    JOIN crdb_internal.cluster_queries q ON q.txn_id=w.txn_id
    WHERE h.granted AND h.contended AND NOT w.granted
      AND s.application_name='{marker}' AND s.status IN ('ACTIVE','IDLE')
      AND q.database='acervo_app_test' AND q.client_address LIKE '{address}:%'
      AND q.query LIKE '%upload_part_copies%' AND q.query LIKE '%part_index%'
      AND q.query NOT LIKE '%crdb_internal%'
    """)
    if len(rows) != 1:
        return None
    return {**rows[0], 'holder': state['holder'], 'generation': str(state['generation'])}


def sql_args():
    return ['docker', 'exec', '-i', SQL_CONTAINER, 'cockroach', 'sql', '--insecure',
            '--host=localhost:26257', '--database=acervo_app_test', '--format=json']


def sql(statement):
    result = subprocess.run(sql_args()+['--execute='+statement], capture_output=True,
                            text=True, timeout=12)
    if result.returncode:
        raise RuntimeError('Fixture SQL failed; no success inferred from timeout/error')
    # Every observer call ends with exactly one SELECT; mutation calls ignore rows.
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        return []


class Barrier:
    def __init__(self, fixture):
        self.fixture = fixture
        self.stop = threading.Event()
        self.write_lock = threading.Lock()
        self.lines = queue.Queue()
        command = sql_args()
        command[-1] = '--format=tsv'
        self.process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.DEVNULL, text=True, bufsize=1)
        threading.Thread(target=self.read, daemon=True).start()
        operation = identifier(fixture['operation'])
        self.send("SET application_name='"+fixture['application_name']+"'; SET statement_timeout='30s'; "
                  "SET transaction_timeout='120s'; SET idle_in_transaction_session_timeout='5s'; "
                  "BEGIN PRIORITY HIGH; UPDATE upload_part_copies SET verified_at=clock_timestamp() "
                  f"WHERE operation_id='{operation}' AND part_index=2; SELECT 'BARRIER_READY';\n")
        deadline = time.monotonic()+10
        while time.monotonic() < deadline:
            try:
                if self.lines.get(timeout=.2).strip() == 'BARRIER_READY':
                    self.thread = threading.Thread(target=self.keepalive, daemon=True)
                    self.thread.start()
                    return
            except queue.Empty:
                pass
        self.close()
        raise RuntimeError('SQL receipt barrier not established')

    def read(self):
        for line in self.process.stdout:
            self.lines.put(line)

    def send(self, text):
        with self.write_lock:
            self.process.stdin.write(text)
            self.process.stdin.flush()

    def keepalive(self):
        while not self.stop.wait(1):
            try:
                self.send("SELECT 'BARRIER_ALIVE';\n")
            except (BrokenPipeError, OSError):
                return

    def close(self):
        self.stop.set()
        try:
            self.send('ROLLBACK;\n')
            self.process.stdin.close()
            self.process.wait(timeout=6)
        except (BrokenPipeError, OSError, subprocess.TimeoutExpired):
            self.process.terminate()
        # A crash/failed rollback is also covered by the persisted session marker
        # and the fixture's five-second idle timeout, never a global setting.


def s3_get(index, bucket, key, query, access, secret):
    host = f'127.0.0.1:{27900+index}'
    path = '/'+urllib.parse.quote(bucket, safe='')
    if key:
        path += '/'+urllib.parse.quote(key, safe='/')
    query_string = urllib.parse.urlencode(sorted(query.items()), quote_via=urllib.parse.quote)
    now = datetime.datetime.now(datetime.timezone.utc)
    timestamp, date = now.strftime('%Y%m%dT%H%M%SZ'), now.strftime('%Y%m%d')
    empty = hashlib.sha256(b'').hexdigest()
    canonical_headers = f'host:{host}\nx-amz-content-sha256:{empty}\nx-amz-date:{timestamp}\n'
    signed_headers = 'host;x-amz-content-sha256;x-amz-date'
    canonical = '\n'.join(['GET', path, query_string, canonical_headers, signed_headers, empty])
    scope = date+'/us-east-1/s3/aws4_request'
    to_sign = 'AWS4-HMAC-SHA256\n'+timestamp+'\n'+scope+'\n'+hashlib.sha256(canonical.encode()).hexdigest()
    signing = ('AWS4'+secret).encode()
    for part in [date, 'us-east-1', 's3', 'aws4_request']:
        signing = hmac.new(signing, part.encode(), hashlib.sha256).digest()
    signature = hmac.new(signing, to_sign.encode(), hashlib.sha256).hexdigest()
    headers = {'x-amz-date': timestamp, 'x-amz-content-sha256': empty,
               'Authorization': f'AWS4-HMAC-SHA256 Credential={access}/{scope}, SignedHeaders={signed_headers}, Signature={signature}'}
    connection = http.client.HTTPConnection('127.0.0.1', 27900+index, timeout=3)
    try:
        connection.request('GET', path+'?'+query_string, headers=headers)
        response = connection.getresponse()
        body = response.read()
        if response.status != 200:
            raise RuntimeError('S3 observer failed: '+str(response.status))
        return ET.fromstring(body)
    finally:
        connection.close()


def final_object_key(operation, sha256):
    if len(sha256) != 64 or any(c not in '0123456789abcdef' for c in sha256):
        raise ValueError('Invalid manifest SHA-256')
    return 'objects/'+identifier(operation)+'/'+sha256


def multipart_evidence(operation, config, sha256):
    env = config['services']['app-node-1']['environment']
    args = (env['S3_ACCESS_KEY'], env['S3_SECRET_KEY'])
    # MinIO interprets a nonempty prefix as an exact object name, not a directory.
    expected_key = final_object_key(operation, sha256)
    namespace = {'s': 'http://s3.amazonaws.com/doc/2006-03-01/'}
    for index in [1, 2, 3]:
        root = s3_get(index, env['S3_BUCKET'], '', {'uploads': '', 'prefix': expected_key}, *args)
        for upload in root.findall('s:Upload', namespace):
            key, upload_id = upload.findtext('s:Key', namespaces=namespace), upload.findtext('s:UploadId', namespaces=namespace)
            if key != expected_key:
                continue
            parts = s3_get(index, env['S3_BUCKET'], key, {'uploadId': upload_id}, *args)
            sizes = [int(part.findtext('s:Size', default='0', namespaces=namespace)) for part in parts.findall('s:Part', namespace)]
            if sum(sizes) > 0:
                return {'site': index, 'key': key, 'upload_id': upload_id, 'completed_parts': len(sizes), 'uploaded_bytes': sum(sizes)}
    return None


def snapshot(operation):
    operation = identifier(operation)
    rows = sql(f"SELECT u.status,u.phase,u.lease_holder::STRING AS holder,u.lease_generation AS generation,u.lease_expires_at::STRING AS expires_at,"
               f"(SELECT count(*) FROM files WHERE operation_id=u.id) AS files,"
               f"(SELECT min(id::STRING) FROM files WHERE operation_id=u.id) AS file_id "
               f"FROM upload_operations u WHERE u.id='{operation}'")
    if len(rows) != 1:
        raise RuntimeError('Operation observer returned no unique fixture')
    return rows[0]


def recover_fixture(path):
    if not path.exists():
        return
    fixture = json.loads(path.read_text())
    marker = fixture['application_name']
    if not marker.startswith('workerproof_') or not marker[len('workerproof_'):].isalnum():
        raise RuntimeError('Refusing unexpected SQL session marker')
    sql("CANCEL SESSIONS SELECT session_id FROM crdb_internal.cluster_sessions WHERE application_name='"+marker+"'")
    if fixture.get('operation') and fixture.get('reservation_generation'):
        operation, holder = identifier(fixture['operation']), identifier(fixture['reservation_holder'])
        generation = int(fixture['reservation_generation'])
        sql(f"UPDATE upload_operations SET lease_expires_at=clock_timestamp()-INTERVAL '1 second' "
            f"WHERE id='{operation}' AND status='pending' AND lease_holder='{holder}' AND lease_generation={generation}")
    fixture['restored'] = True
    atomic_private(path, fixture)


def run(args, proof):
    fixture_path = Path(args.journal+'.fixture.json')
    if fixture_path.exists() and not json.loads(fixture_path.read_text()).get('restored'):
        raise RuntimeError('Unrestored fixture; use --recover')
    proof.preflight(privileged=True)
    disk_initial = disk_free()
    if disk_initial < DISK_INITIAL:
        raise RuntimeError('Isolated fixture requires at least 7.5 GiB free before upload')
    proof.account()
    payload = bytes(range(256))*(PART_SIZE//256)
    data = manifest(proof.run_id, payload)
    code, raw, _ = proof.http(2, '/api/uploads', 'POST', data, proof.user_cookie, timeout=20)
    if code != 201:
        raise RuntimeError('Fixture operation creation failed')
    operation = identifier(json.loads(raw)['id'])
    fixture = {'application_name': 'workerproof_'+proof.run_id, 'operation': operation, 'restored': False, 'user_cookie': proof.user_cookie}
    atomic_private(fixture_path, fixture)
    holder = identifier(proof.node_ids[1])
    # Persist an unmistakable fixture generation BEFORE reserving the operation.
    # Real workers subsequently increment it normally; recovery matches it exactly.
    fixture.update(reservation_generation=uuid.uuid4().int % (1 << 60) + (1 << 60), reservation_holder=holder)
    atomic_private(fixture_path, fixture)
    barrier = None
    budget_hit, stop_watch = threading.Event(), threading.Event()
    def watch_disk():
        while not stop_watch.wait(1):
            free = disk_free()
            if disk_exceeded(disk_initial, free):
                atomic_private(Path(args.journal+'.budget.json'), {'initial_free': disk_initial, 'free': free, 'operation': operation, 'triggered': True})
                budget_hit.set()
                os.kill(os.getpid(), signal.SIGINT)
                return
    watcher = threading.Thread(target=watch_disk, daemon=True)
    watcher.start()
    preserve_paused = False
    assertions_passed = False
    try:
        reservation = sql(f"UPDATE upload_operations SET lease_holder='{holder}',lease_generation={fixture['reservation_generation']},lease_expires_at=clock_timestamp()+INTERVAL '5 minutes' "
                          f"WHERE id='{operation}' AND status='pending' AND lease_generation<{fixture['reservation_generation']} RETURNING lease_generation")
        if len(reservation)!=1:
            raise RuntimeError('Could not reserve this fixture operation')
        for part in range(PART_COUNT):
            code, _, _ = proof.http(2, f'/api/uploads/{operation}/parts/{part}', 'PUT', payload, proof.user_cookie, timeout=45)
            if code != 204:
                raise RuntimeError('Fixture part upload failed: '+str(code))
        rows = sql(f"SELECT count(*) AS copies FROM upload_part_copies WHERE operation_id='{operation}' AND part_index=2")
        if int(rows[0]['copies']) < 1:
            raise RuntimeError('Last part receipt missing')
        barrier = Barrier(fixture)
        sql(f"UPDATE upload_operations SET lease_expires_at=clock_timestamp()-INTERVAL '1 second' WHERE id='{operation}' AND lease_generation={fixture['reservation_generation']}")
        config = json.loads(Path(args.app_compose).read_text())
        observed = None
        evidence_samples = 0
        contention_samples = 0
        deadline = time.monotonic()+25
        while time.monotonic() < deadline:
            state = snapshot(operation)
            if state['status'] != 'pending':
                raise RuntimeError('Worker completed before crash barrier; proof invalid')
            evidence = multipart_evidence(operation, config, data['sha256'])
            contention = contention_evidence(operation, fixture, state, proof) if evidence else None
            if evidence:
                evidence_samples += 1
                proof.event('multipart_bytes_observed', operation=operation, multipart=evidence, contention=contention)
            if contention:
                contention_samples += 1
            if evidence and positive_copy(state, evidence['uploaded_bytes'], contention) and int(state['generation']) > fixture['reservation_generation']:
                observed = (state, evidence, contention)
                break
            time.sleep(.1)
        if not observed:
            proof.event('copy_window_inconclusive', operation=operation, multipart_samples=evidence_samples, contention_samples=contention_samples)
            raise RuntimeError('No complete multipart plus live-waiter evidence; timeout is not proof')
        before, evidence, waiting = observed
        worker = next(index for index, node_id in proof.node_ids.items() if node_id == before['holder'])
        proof.event('copy_wait_observed_before_pause', operation=operation, sql=before, multipart=evidence, contention=waiting)
        proof.mutate_container('pause', f'app-node-{worker}')
        frozen = snapshot(operation)
        if frozen['status'] != 'pending' or frozen['holder'] != before['holder'] or frozen['generation'] != before['generation'] or int(frozen['files']) != 0:
            raise RuntimeError('Worker identity/state changed before pause; proof invalid')
        still_waiting = contention_evidence(operation, fixture, frozen, proof)
        if not positive_copy(frozen, evidence['uploaded_bytes'], still_waiting) or not same_wait(waiting, still_waiting):
            proof.event('ambiguous_copy_window', operation=operation, reason='readPart waiter vanished or changed across pause; multipart alone cannot prove task 8.3')
            raise RuntimeError('Copy contention not confirmed after pause; task 8.3 remains unproved')
        proof.event('copy_interrupted_with_live_waiter', operation=operation, multipart=evidence, contention=still_waiting)
        crash_at = time.monotonic()
        barrier.close()
        barrier = None
        sql("CANCEL SESSIONS SELECT session_id FROM crdb_internal.cluster_sessions WHERE application_name='"+fixture['application_name']+"'")
        def completed():
            current = snapshot(operation)
            if current['status'] != 'available':
                return False
            if current['holder'] == before['holder'] or int(current['generation']) <= int(before['generation']) or int(current['files']) != 1:
                raise RuntimeError('No distinct fenced successor/publication')
            return current
        after = proof.wait(completed, 'survivor publication', seconds=150)
        proof.event('successor_published', operation=operation, sql=after, elapsed_seconds=time.monotonic()-crash_at)
        survivor = next(index for index, node_id in proof.node_ids.items() if node_id == after['holder'])
        connection = http.client.HTTPConnection('127.0.0.1', 28100+survivor, timeout=20)
        try:
            connection.request('GET', '/api/files/'+after['file_id']+'/download', headers={'Cookie': proof.user_cookie})
            response = connection.getresponse()
            if response.status != 200:
                raise RuntimeError('Successor download failed')
            digest, size = hashlib.sha256(), 0
            while chunk := response.read(1 << 20):
                digest.update(chunk)
                size += len(chunk)
            if digest.hexdigest() != data['sha256'] or size != data['size']:
                raise RuntimeError('Recovered file bytes differ')
        finally:
            connection.close()
        proof.restore()  # Resume the old worker and test that its stale claim cannot republish.
        proof.wait(proof.ready, 'all three nodes restored', seconds=150)
        time.sleep(12)  # Includes the old worker renewal interval and several worker polls.
        final = snapshot(operation)
        if final['status'] != 'available' or int(final['files']) != 1 or final['file_id'] != after['file_id'] or final['generation'] != after['generation']:
            raise RuntimeError('Publication changed after stale worker resumed')
        proof.event('single_publication_after_old_worker_return', operation=operation, file_id=final['file_id'], size=size, sha256=data['sha256'])
        assertions_passed = True
    finally:
        stop_watch.set()
        watcher.join(timeout=2)
        try:
            if barrier:
                barrier.close()
            recover_fixture(fixture_path)
        finally:
            if budget_hit.is_set() or not assertions_passed:
                stopped = False
                cleanup_reason = 'disk_budget' if budget_hit.is_set() else 'proof_failed_or_inconclusive'
                try:
                    stopped = snapshot(operation)['status'] in ('available', 'cancelled', 'failed')
                except Exception:
                    pass
                for index in (1, 2, 3):
                    if stopped:
                        break
                    try:
                        code, raw, _ = proof.http(index, '/api/uploads/'+operation+'/cancel', 'POST', {}, proof.user_cookie, timeout=5)
                        if code == 200 and json.loads(raw).get('status') in ('available', 'cancelled', 'failed'):
                            stopped = True
                            break
                    except Exception:
                        pass
                proof.event('fixture_write_containment', operation=operation, reason=cleanup_reason, terminal_confirmed=stopped)
                if not stopped:
                    preserve_paused = True
                    for index in (1, 2, 3):
                        service = f'app-node-{index}'
                        if any(a.get('kind') == 'pause' and a.get('service') == service and not a.get('restored') for a in proof.actions):
                            continue
                        proof.mutate_container('pause', service)
                    proof.event('fixture_backends_left_paused', reason=cleanup_reason, detail='Own operation not confirmed terminal; coordinator must authorize restore')
            if budget_hit.is_set() and not may_restore({'triggered': True}, disk_free()):
                preserve_paused = True
                proof.event('disk_budget_restore_refused', reason='Less than 6 GiB free; recorded pauses preserved')
            if not preserve_paused:
                proof.restore()
    if assertions_passed and not preserve_paused:
        proof.report['complete'] = True
        proof.event('worker_recovery_complete')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    parser.add_argument('--journal', default='/tmp/acervo-worker-recovery-journal.json')
    parser.add_argument('--report', default='/tmp/acervo-worker-recovery-report.json')
    parser.add_argument('--harness', type=Path, default=Path(__file__).with_name('verify-distributed-failures.py'), help='Use the current root failure harness when running from a worktree')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    parser.add_argument('--self-test', action='store_true', help='Run pure journal/manifest/evidence checks without infrastructure')
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return
    if not args.execute and not args.recover:
        print('Prepared only: exclusive fixture receipt intent -> S3 multipart bytes plus live readPart contention -> pause identified worker -> confirm same waiter -> rollback -> successor generation -> one publication. No HTTP/SQL/Docker executed.')
        return
    args.phase, args.start_node, args.start_mode = 'worker', 1, 'backend'
    with open(LOCK, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        spec = importlib.util.spec_from_file_location('failure_harness', args.harness.resolve())
        harness = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(harness)
        proof = harness.Proof(args)
        journal = Path(args.journal)
        if args.recover:
            if journal.exists():
                state = json.loads(journal.read_text())
                proof.run_id, proof.actions = state['run_id'], state['actions']
            recover_fixture(Path(args.journal+'.fixture.json'))
            budget_path = Path(args.journal+'.budget.json')
            budget = json.loads(budget_path.read_text()) if budget_path.exists() else {}
            if not may_restore(budget, disk_free()):
                raise RuntimeError('SQL fixture recovered; backend restore refused below 6 GiB after budget interruption')
            proof.restore()
            return
        if journal.exists() and any(not action.get('restored') for action in json.loads(journal.read_text())['actions']):
            raise RuntimeError('Pending container journal; use --recover')
        for sig in (signal.SIGTERM, signal.SIGINT):
            signal.signal(sig, lambda _sig, _frame: (_ for _ in ()).throw(KeyboardInterrupt()))
        run(args, proof)


def self_test():
    operation = '00000000-0000-0000-0000-000000000001'
    exact = final_object_key(operation, 'a'*64)
    assert exact == 'objects/'+operation+'/'+'a'*64
    calls = []
    original_s3_get = globals()['s3_get']
    def fake_s3(index, bucket, key, query, access, secret):
        calls.append((key, query))
        if 'uploads' in query:
            assert query['prefix'] == exact
            return ET.fromstring('<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">'
                '<Upload><Key>'+exact+'/other</Key><UploadId>wrong</UploadId></Upload>'
                '<Upload><Key>'+exact+'</Key><UploadId>correct</UploadId></Upload></ListMultipartUploadsResult>')
        assert key == exact and query['uploadId'] == 'correct'
        return ET.fromstring('<ListPartsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Part><Size>32</Size></Part></ListPartsResult>')
    globals()['s3_get'] = fake_s3
    try:
        config = {'services': {'app-node-1': {'environment': {'S3_BUCKET': 'fixture', 'S3_ACCESS_KEY': '', 'S3_SECRET_KEY': ''}}}}
        evidence = multipart_evidence(operation, config, 'a'*64)
        assert evidence['key'] == exact and evidence['uploaded_bytes'] == 32
        assert len(calls) == 2
    finally:
        globals()['s3_get'] = original_s3_get
    assert DISK_INITIAL == 7.5 * (1 << 30)
    assert not disk_exceeded(DISK_INITIAL, DISK_INITIAL)
    assert disk_exceeded(DISK_INITIAL, DISK_FLOOR)
    assert disk_exceeded(DISK_FLOOR, DISK_FLOOR - 1)
    assert not may_restore({'triggered': True}, DISK_FLOOR - 1)
    assert may_restore({'triggered': True}, DISK_FLOOR)
    assert may_restore({}, DISK_FLOOR - 1)
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory)/'journal.json'
        atomic_private(path, {'restored': False, 'generation': 123})
        assert stat.S_IMODE(path.stat().st_mode) == 0o600
        assert json.loads(path.read_text())['generation'] == 123
        atomic_private(path, {'restored': True})
        assert json.loads(path.read_text()) == {'restored': True}
    data = manifest('fixture', b'abc')
    assert data['size'] == 9
    assert data['sha256'] == hashlib.sha256(b'abcabcabc').hexdigest()
    assert [part['offset'] for part in data['parts']] == [0, 3, 6]
    pending = {'status': 'pending', 'files': '0', 'holder': 'worker', 'generation': '3'}
    wait = {'holder': 'worker', 'generation': '3', 'blocking_txn': 'fixture', 'waiting_txn': 'reader',
            'query_id': 'readPart', 'lock_key': 'last-part-key', 'fixture_session': 'session'}
    assert positive_copy(pending, 32, wait)
    assert not positive_copy(pending, 32)  # Dangling multipart without contention.
    assert not positive_copy(pending, 32, {**wait, 'waiting_txn': None})
    assert not positive_copy(pending, 32, {**wait, 'holder': 'different-worker'})
    assert not positive_copy(pending, 32, {**wait, 'generation': '2'})
    assert not positive_copy(pending, 0, wait)
    assert not positive_copy({**pending, 'status': 'available'}, 32, wait)
    assert not positive_copy({**pending, 'files': '1'}, 32, wait)
    assert same_wait(wait, dict(wait))
    assert not same_wait(wait, None)  # Statement timeout between observation and pause.
    assert not same_wait(wait, {**wait, 'waiting_txn': 'new-attempt'})
    assert not same_wait(wait, {**wait, 'query_id': 'different-query'})
    print('PASS: exact multipart key selection; capacity floor/growth/recovery guards; private journal/manifest; copy requires live fixture contention; missing/changed waiter, wrong worker/generation and dangling multipart all rejected; no infrastructure contacted')


if __name__ == '__main__':
    main()
