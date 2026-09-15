#!/usr/bin/env python3
"""Tiny HTTP proof run inside the dev load-balancer. Default is offline.

--execute creates a folder and one three-part tiny upload; --resume only verifies
saved IDs after restart. State has no cookie/password. No Docker/SQL operations.
"""
import argparse
import hashlib
import http.client
import json
from pathlib import Path
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--resume', action='store_true')
    parser.add_argument('--state', default='/tmp/acervo-dev-client-state.json')
    args = parser.parse_args()
    if not args.execute and not args.resume:
        print('OFFLINE PLAN: login admin, same session on three nodes, three distributed parts, four downloads and folder listings. No services contacted.')
        return
    cookie = None
    report = {'mode': 'resume' if args.resume else 'execute', 'requests': [], 'complete': False}

    def call(index, path, method='GET', body=None):
        host = '127.0.0.1' if index == 0 else f'backend-node-{index}'
        conn = http.client.HTTPConnection(host, 8080, timeout=15)
        headers = {'Cookie': cookie} if cookie else {}
        if isinstance(body, bytes):
            headers['Content-Type'] = 'application/octet-stream'
        elif body is not None:
            headers['Content-Type'] = 'application/json'
            body = json.dumps(body).encode()
        started = time.monotonic()
        try:
            conn.request(method, path, body, headers)
            response = conn.getresponse()
            raw = response.read()
            report['requests'].append({'node': index, 'path': path, 'method': method,
                                       'status': response.status, 'seconds': time.monotonic()-started})
            return response.status, raw, dict(response.getheaders())
        finally:
            conn.close()

    def wait(check, label, seconds):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                if check():
                    return
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(.5)
        raise AssertionError(label + ' deadline exceeded')

    try:
        wait(lambda: all(call(n, '/health/ready')[0] == 200 for n in (1, 2, 3)), 'three ready backends', 180)
        status, body, headers = call(1, '/api/login', 'POST', {'login': 'admin', 'password': 'admin'})
        assert status == 200, ('login', status)
        cookie = headers['Set-Cookie'].split(';')[0]
        identities = []
        for n in (1, 2, 3):
            status, body, _ = call(n, '/api/me')
            assert status == 200, ('shared session', n, status)
            identities.append(json.loads(body))
        assert identities[0] == identities[1] == identities[2], 'Session identity differs between replicas'
        if args.resume:
            state = json.loads(Path(args.state).read_text())
        else:
            if Path(args.state).exists():
                raise RuntimeError('Existing state: use --resume or a distinct --state; no overwrite')
            unique = uuid.uuid4().hex
            status, body, _ = call(2, '/api/folders', 'POST', {'name': 'dev-proof-' + unique, 'parent_id': None})
            assert status == 201, ('create folder', status)
            folder = json.loads(body)['id']
            chunks = [('dev-proof-' + unique + '-part-' + str(n)).encode() for n in (1, 2, 3)]
            content = b''.join(chunks)
            digest = hashlib.sha256(content).hexdigest()
            manifest = {'idempotency_key': str(uuid.uuid4()), 'folder_id': folder,
                        'name': 'three-nodes.example', 'size': len(content), 'sha256': digest, 'parts': []}
            offset = 0
            for i, chunk in enumerate(chunks):
                manifest['parts'].append({'index': i, 'offset': offset, 'size': len(chunk), 'sha256': hashlib.sha256(chunk).hexdigest()})
                offset += len(chunk)
            status, body, _ = call(1, '/api/uploads', 'POST', manifest)
            assert status == 201, ('create upload', status)
            op = json.loads(body)
            for i, chunk in enumerate(chunks):
                status, _, _ = call(i + 1, '/api/uploads/' + op['id'] + '/parts/' + str(i), 'PUT', chunk)
                assert status == 204, ('part', i, status)
            def published():
                nonlocal op
                status, body, _ = call(2, '/api/uploads/' + op['id'])
                if status != 200:
                    return False
                op = json.loads(body)
                return op['status'] == 'available'
            wait(published, 'publication', 90)
            state = {'folder_id': folder, 'operation_id': op['id'], 'file_id': op['file_id'], 'size': len(content), 'sha256': digest}
            Path(args.state).write_text(json.dumps(state) + '\n')
        for n in (0, 1, 2, 3):
            status, body, _ = call(n, '/api/files/' + state['file_id'] + '/download')
            assert status == 200 and len(body) == state['size'] and hashlib.sha256(body).hexdigest() == state['sha256'], ('download mismatch', n, status)
            status, body, _ = call(n, '/api/folders')
            assert status == 200 and state['folder_id'] in {item['id'] for item in json.loads(body)['folders']}, ('folder not visible', n)
            status, body, _ = call(n, '/api/folders?parent_id=' + state['folder_id'])
            assert status == 200 and state['file_id'] in {item['id'] for item in json.loads(body)['files']}, ('file not visible', n)
        report.update(state)
        report['complete'] = True
    finally:
        print(json.dumps(report, indent=2), flush=True)


if __name__ == '__main__':
    main()
