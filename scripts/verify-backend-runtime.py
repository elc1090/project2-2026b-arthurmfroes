#!/usr/bin/env python3
"""Run three disposable backend processes against the isolated infrastructure.

Usage: python3 scripts/verify-backend-runtime.py /tmp/acervo-runtime-server
The binary must already be built. No Docker service is stopped or rebuilt.
"""
import json
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

binary = str(Path(sys.argv[1]).resolve())
schema = 'runtime_' + uuid.uuid4().hex
ports = [28011, 28012, 28013]
processes = [None] * 3
logs = []
token = 'runtime-test-control-token'


def sql(statement):
    return subprocess.run([
        'docker', 'exec', 'acervo-infra-runtime-cockroach-1-1',
        'cockroach', 'sql', '--insecure', '--host=localhost:26257',
        '--execute', statement,
    ], check=True, capture_output=True, text=True).stdout


def call(index, path, method='GET', body=None, cookie=None, internal=False):
    headers = {}
    if isinstance(body, bytes):
        headers['Content-Type'] = 'application/octet-stream'
    elif body is not None:
        body = json.dumps(body).encode()
        headers['Content-Type'] = 'application/json'
    if cookie:
        headers['Cookie'] = cookie
    if internal:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(f'http://127.0.0.1:{ports[index]}{path}', body, headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=15)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        return response.status, raw, response.headers


def wait_for(check, message, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if check():
                print('PASS:', message, flush=True)
                return
        except (OSError, urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.3)
    raise AssertionError(message + ' timed out')


def start(index):
    env = dict(os.environ, PORT=str(ports[index]), NODE_ID=f'runtime-node-{index+1}',
               NODE_BOOTSTRAP='true',
               BACKEND_ENDPOINT=f'http://127.0.0.1:{ports[index]}',
               DATABASE_URL=f'postgresql://root@127.0.0.1:{27657+index}/acervo_runtime_test?sslmode=disable&search_path={schema}',
               S3_ENDPOINT=f'http://127.0.0.1:{27901+index}', S3_BUCKET='drive-clone',
               S3_ACCESS_KEY='minioadmin', S3_SECRET_KEY='minioadmin', CONTROL_TOKEN=token,
               CONTROL_INTERVAL='1s', CONTROL_TIMEOUT='2s', MANAGER_LEASE_TTL='12s',
               FAILURE_THRESHOLD='2', SECURE_COOKIES='false', ADMIN_LOGIN='admin', ADMIN_PASSWORD='admin', ENABLE_DEV_FAULTS='false')
    log = tempfile.TemporaryFile()
    logs.append(log)
    processes[index] = subprocess.Popen([binary], env=env, stdout=log, stderr=log)


def stop(index):
    process = processes[index]
    if process and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise AssertionError('backend did not stop gracefully')


sql('CREATE DATABASE IF NOT EXISTS acervo_runtime_test; CREATE SCHEMA acervo_runtime_test.' + schema)
try:
    for index in range(3):
        start(index)
    wait_for(lambda: all(call(i, '/health/ready')[0] == 200 for i in range(3)),
             'three real processes admitted using local SQL and MinIO')
    assert call(0, '/internal/cluster')[0] == 401
    assert call(0, '/internal/node/identity')[0] == 401
    status, body, _ = call(0, '/internal/node/identity', internal=True)
    identity = json.loads(body)
    assert status == 200 and identity['node_id'] == 'runtime-node-1'
    assert identity['deployment_id'] and identity['sql_node_id'] > 0
    assert 'password' not in body.decode() and 'minioadmin' not in body.decode()
    assert call(0, '/api/register', 'POST', {'login': 'alice', 'password': 'secret'})[0] == 201
    status, body, headers = call(0, '/api/login', 'POST', {'login': 'alice', 'password': 'secret'})
    assert status == 200, (status, body)
    cookie = headers['Set-Cookie'].split(';')[0]
    status, body, _ = call(1, '/api/folders', 'POST', {'name': 'Cadeira', 'parent_id': None}, cookie)
    assert status == 201, (status, body)
    folder_id = json.loads(body)['id']
    assert folder_id in call(2, '/api/folders', cookie=cookie)[1].decode()
    print('PASS: login on node 1, folder creation on node 2, listing on node 3', flush=True)
    chunks = [b'first fragment', b'second fragment']
    content = b''.join(chunks)
    manifest = {'idempotency_key': str(uuid.uuid4()), 'folder_id': folder_id,
                'name': 'example.unknown', 'size': len(content),
                'sha256': hashlib.sha256(content).hexdigest(), 'parts': []}
    offset = 0
    for index, chunk in enumerate(chunks):
        manifest['parts'].append({'index': index, 'offset': offset, 'size': len(chunk),
                                  'sha256': hashlib.sha256(chunk).hexdigest()})
        offset += len(chunk)
    status, body, _ = call(0, '/api/uploads', 'POST', manifest, cookie)
    assert status == 201, (status, body)
    operation = json.loads(body)
    replay = call(1, '/api/uploads', 'POST', manifest, cookie)
    assert replay[0] == 201 and json.loads(replay[1])['id'] == operation['id']
    for index, chunk in enumerate(chunks[:1]):
        status, body, _ = call(index, f"/api/uploads/{operation['id']}/parts/{index}", 'PUT', chunk, cookie)
        assert status == 204, (status, body)
    status, body, _ = call(2, f"/api/uploads/{operation['id']}", cookie=cookie)
    confirmed = json.loads(body)
    assert status == 200 and confirmed['parts'][0]['available'] and not confirmed['parts'][1]['available'], (status, body)
    assert confirmed['status'] == 'pending'
    assert call(0, '/api/admin/cluster', cookie=cookie)[0] == 403
    for path, method in [('/api/admin/nodes', 'POST'),
                         ('/api/admin/nodes/00000000-0000-0000-0000-000000000001/retire', 'POST'),
                         ('/api/admin/nodes/00000000-0000-0000-0000-000000000001/fault', 'POST'),
                         ('/api/admin/node-operations', 'GET')]:
        assert call(0, path, method, {} if method == 'POST' else None, cookie)[0] == 403
    status, body, headers = call(1, '/api/login', 'POST', {'login': 'admin', 'password': 'admin'})
    assert status == 200, (status, body)
    admin_cookie = headers['Set-Cookie'].split(';')[0]
    assert call(1, '/api/admin/node-operations', cookie=admin_cookie)[0] == 200
    assert call(1, '/api/admin/nodes', 'POST', {'node_id': 'invalid'}, admin_cookie)[0] == 400
    assert call(1, '/api/admin/nodes/00000000-0000-0000-0000-000000000001/fault',
                'POST', {'mode': 'total'}, admin_cookie)[0] == 404
    assert call(1, '/internal/faults', 'POST', {'mode': 'none'}, internal=True)[0] == 404
    print('PASS: topology API requires admin; internal identity authenticated; simulation disabled outside development', flush=True)
    status, body, _ = call(2, '/api/admin/cluster', cookie=admin_cookie)
    assert status == 200 and operation['id'] in body.decode(), (status, body)
    assert 'example.unknown' not in body.decode() and 'Cadeira' not in body.decode()
    assert len(json.loads(body)['operations'][0]['sites']) == 3
    print('PASS: admin sees operational sites without private file names or paths', flush=True)

    assert call(1, '/api/folders?parent_id=' + folder_id, cookie=admin_cookie)[0] == 404
    assert call(1, '/api/folders', 'POST', {'name': 'Foreign child', 'parent_id': folder_id}, admin_cookie)[0] == 404
    assert call(1, '/api/uploads', 'POST', dict(manifest, idempotency_key=str(uuid.uuid4())), admin_cookie)[0] == 404
    assert call(1, f"/api/uploads/{operation['id']}/parts/1", 'PUT', chunks[1], admin_cookie)[0] == 404
    assert call(1, f"/api/uploads/{operation['id']}/cancel", 'POST', {}, admin_cookie)[0] == 404
    status, body, _ = call(1, '/api/uploads', cookie=admin_cookie)
    assert status == 200 and operation['id'] not in body.decode()
    assert call(0, '/api/logout', 'POST', cookie=cookie)[0] == 204
    status, body, headers = call(2, '/api/login', 'POST', {'login': 'alice', 'password': 'secret'})
    assert status == 200, (status, body)
    cookie = headers['Set-Cookie'].split(';')[0]
    status, body, _ = call(1, '/api/uploads', cookie=cookie)
    assert status == 200 and operation['id'] in body.decode()
    print('PASS: owner re-login recovers pending operation; foreign folders, parts, cancellation and transfer listing denied', flush=True)

    assert 'example.unknown' not in call(2, '/api/folders?parent_id=' + folder_id, cookie=cookie)[1].decode()
    status, body, _ = call(1, f"/api/uploads/{operation['id']}/parts/1", 'PUT', chunks[1], cookie)
    assert status == 204, (status, body)

    def published():
        status, body, _ = call(0, f"/api/uploads/{operation['id']}", cookie=cookie)
        return status == 200 and json.loads(body)['status'] == 'available'

    wait_for(published, 'worker publishes after all local receipts', timeout=40)
    final = json.loads(call(0, f"/api/uploads/{operation['id']}", cookie=cookie)[1])
    for index in range(3):
        status, body, _ = call(index, f"/api/files/{final['file_id']}/download", cookie=cookie)
        assert status == 200 and body == content, (status, body)
    assert call(1, f"/api/files/{final['file_id']}/download", cookie=admin_cookie)[0] == 404
    assert call(1, f"/api/uploads/{operation['id']}", cookie=admin_cookie)[0] == 404
    cancelled = dict(manifest, name='cancel.unknown', idempotency_key=str(uuid.uuid4()))
    status, body, _ = call(0, '/api/uploads', 'POST', cancelled, cookie)
    assert status == 201
    cancelled_id = json.loads(body)['id']
    status, body, _ = call(2, f"/api/uploads/{cancelled_id}/cancel", 'POST', cookie=cookie)
    assert status == 200 and json.loads(body)['status'] == 'cancelled'
    status, body, _ = call(2, f"/api/uploads/{operation['id']}/cancel", 'POST', cookie=cookie)
    assert status == 200 and json.loads(body)['status'] == 'available'
    print('PASS: three downloads match, admin cannot read private bytes, cancellation preserves published file', flush=True)

    stop(2)

    def excluded():
        status, body, _ = call(0, '/internal/cluster', internal=True)
        return status == 200 and len(json.loads(body)['configuration']['Members']) == 2

    wait_for(excluded, 'terminated backend automatically excluded within configured lease/probe budget', timeout=20)
    assert call(1, '/api/me', cookie=cookie)[0] == 200
    start(2)
    wait_for(lambda: call(2, '/health/ready')[0] == 200, 'restarted backend readmitted through synchronization')
    assert call(2, f"/api/files/{final['file_id']}/download", cookie=cookie)[1] == content
    assert call(2, '/api/logout', 'POST', cookie=cookie)[0] == 204
    assert call(0, '/api/me', cookie=cookie)[0] == 401
    print('PASS: logout on node 3 invalidates session on node 1', flush=True)
except BaseException:
    for log in logs:
        log.seek(0)
        print(log.read().decode(), file=sys.stderr)
    raise
finally:
    for index in range(3):
        stop(index)
    for log in logs:
        log.close()
    sql('DROP SCHEMA acervo_runtime_test.' + schema + ' CASCADE')
