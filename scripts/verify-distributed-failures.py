#!/usr/bin/env python3
"""Destructive-to-availability checks, exclusively on the two disposable projects.

Default invocation is read-only preflight. Pass --execute only after coordinating
exclusive Docker access. --recover restores pending actions from this run's journal.
No volumes, images, existing accounts, or existing file metadata are deleted.
"""
import argparse
import fcntl
import hashlib
import http.client
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import uuid

APP = 'acervo-app-test'
INFRA = 'acervo-infra-runtime'
NETWORK = INFRA + '_default'


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=45).stdout


def inspect(container):
    return json.loads(command('docker', 'inspect', container))[0]


class Proof:
    def __init__(self, args):
        self.args = args
        self.run_id = uuid.uuid4().hex
        self.actions = []
        self.report = {'run_id': self.run_id, 'phase': args.phase, 'start_node': args.start_node, 'start_mode': args.start_mode, 'complete': False, 'cases': []}
        self.containers = {}
        self.admin_cookies = {}
        self.user_cookie = None
        for path, project in [(args.app_compose, APP), (args.infra_compose, INFRA)]:
            config = json.loads(command('docker', 'compose', '-f', path, 'config', '--format', 'json'))
            if config.get('name') != project:
                raise RuntimeError('Refusing unexpected Compose project: ' + str(config.get('name')))
            services = ['app-node-1', 'app-node-2', 'app-node-3'] if project == APP else ['cockroach-1', 'cockroach-2', 'cockroach-3']
            for service in services:
                cid = command('docker', 'compose', '-f', path, 'ps', '--all', '-q', service).strip()
                if not cid or '\n' in cid:
                    raise RuntimeError('Expected exactly one running container: ' + service)
                data = inspect(cid)
                labels = data['Config']['Labels']
                if labels.get('com.docker.compose.project') != project or labels.get('com.docker.compose.service') != service:
                    raise RuntimeError('Container ownership mismatch: ' + service)
                self.containers[service] = data
        self.report['containers'] = {name: data['Id'] for name, data in self.containers.items()}
        app_config = json.loads(Path(args.app_compose).read_text())
        self.admin_login = app_config['services']['app-node-1']['environment']['ADMIN_LOGIN']
        self.admin_password = app_config['services']['app-node-1']['environment']['ADMIN_PASSWORD']
        self.control_token = app_config['services']['app-node-1']['environment']['CONTROL_TOKEN']
        for n in range(1, 4):
            service = app_config['services'][f'app-node-{n}']
            if service['environment']['DATABASE_URL'].split('/')[3].split('?')[0] != 'acervo_app_test':
                raise RuntimeError('Application must use isolated acervo_app_test database')
            if f'127.0.0.1:{28100+n}:8080' not in service['ports']:
                raise RuntimeError('Unexpected direct HTTP port')

    def event(self, name, **details):
        self.report['cases'].append({'name': name, 'at': time.time(), **details})
        Path(self.args.report).write_text(json.dumps(self.report, indent=2) + '\n')
        print(name, json.dumps(details), flush=True)

    def save_journal(self):
        path = Path(self.args.journal)
        temporary = path.with_suffix('.tmp')
        temporary.write_text(json.dumps({'run_id': self.run_id, 'actions': self.actions}, indent=2) + '\n')
        os.replace(temporary, path)

    def http(self, index, path, method='GET', body=None, cookie=None, connection=None, timeout=8, internal=False):
        own = connection is None
        conn = connection or http.client.HTTPConnection('127.0.0.1', 28100+index, timeout=timeout)
        headers = {}
        if internal:
            headers['Authorization'] = 'Bearer '+self.control_token
        if cookie:
            headers['Cookie'] = cookie
        if body is not None:
            if isinstance(body, bytes):
                headers['Content-Type'] = 'application/octet-stream'
            else:
                body = json.dumps(body).encode()
                headers['Content-Type'] = 'application/json'
        try:
            conn.request(method, path, body, headers)
            response = conn.getresponse()
            raw = response.read()
            return response.status, raw, dict(response.getheaders())
        finally:
            if own:
                conn.close()

    def login_admin(self, index):
        status, _, headers = self.http(index, '/api/login', 'POST', {'login': self.admin_login, 'password': self.admin_password})
        if status != 200:
            raise RuntimeError('Admin login failed: ' + str(status))
        self.admin_cookies[index] = headers['Set-Cookie'].split(';')[0]

    def admin(self, path, method='GET', body=None):
        last = None
        for index in [2, 3, 1]:
            try:
                if index not in self.admin_cookies:
                    self.login_admin(index)
                status, raw, _ = self.http(index, path, method, body, self.admin_cookies[index])
                if 200 <= status < 300:
                    return json.loads(raw) if raw else None
                last = RuntimeError(f'{path}: HTTP {status}')
            except (OSError, http.client.HTTPException, RuntimeError) as error:
                last = error
        raise RuntimeError('No healthy admin endpoint') from last

    def wait(self, check, label, seconds=90):
        deadline = time.monotonic()+seconds
        last = None
        while time.monotonic() < deadline:
            try:
                result = check()
                if result:
                    return result
            except (OSError, http.client.HTTPException, RuntimeError) as error:
                last = str(error)
            time.sleep(.5)
        raise AssertionError(f'{label} timed out; last error={last}')

    def ready(self):
        view = self.admin('/api/admin/cluster')
        nodes = [node for node in view['nodes'] if node['state'] != 'removed']
        expected = {f'app-node-{i}' for i in [1, 2, 3]}
        return {node['node_id'] for node in nodes} == expected and all(node['state'] == 'ready' and node['simulation'] == 'none' for node in nodes) and all(self.http(i, '/health/ready')[0] == 200 for i in [1, 2, 3])

    def excluded(self, node_id):
        view = self.admin('/api/admin/cluster')
        return any(node['id'] == node_id and node['state'] == 'unavailable' for node in view['nodes'])

    def preflight(self, privileged=False):
        for name, data in self.containers.items():
            if not data['State']['Running'] or data['State']['Paused']:
                raise RuntimeError('Expected running/unpaused service: '+name)
            if NETWORK not in data['NetworkSettings']['Networks']:
                raise RuntimeError('Expected test network missing: '+name)
        self.wait(lambda: all(self.http(i, '/health/ready')[0] == 200 for i in [1, 2, 3]), 'three ready nodes')
        if not privileged:
            self.event('read_only_preflight', ready_nodes=3)
            return
        self.wait(self.ready, 'three ready nodes without simulation')
        view = self.admin('/api/admin/cluster')
        if not view['simulation_enabled']:
            raise RuntimeError('Fault injection must be enabled on the test stack')
        self.node_ids = {i: next(node['id'] for node in view['nodes'] if node['node_id'] == f'app-node-{i}') for i in [1, 2, 3]}
        self.target = 1
        self.target_id = self.node_ids[1]
        self.event('preflight', target_id=self.target_id, manager_id=view['manager_id'])

    def account(self):
        self.login = 'failproof_'+self.run_id[:16]
        password = uuid.uuid4().hex
        status, _, _ = self.http(2, '/api/register', 'POST', {'login': self.login, 'password': password})
        if status != 201:
            raise AssertionError('Test account registration failed: '+str(status))
        def login():
            status, body, headers = self.http(2, '/api/login', 'POST', {'login': self.login, 'password': password})
            if status == 503:
                self.event('preparation_login_503')
                try:
                    self.snapshot('preparation-login')
                except subprocess.TimeoutExpired:
                    pass
                return False
            if status != 200:
                raise AssertionError(('Test account login failed', status, body))
            self.user_cookie = headers['Set-Cookie'].split(';')[0]
            return True
        self.wait(login, 'test account login', seconds=45)
        self.event('exclusive_test_account', login=self.login)

    def record(self, action):
        self.actions.append(action)
        self.save_journal()  # Persist intent BEFORE mutation, making interruption recoverable.

    def fault(self, mode):
        self.record({'kind': 'fault', 'node_id': self.target_id, 'index': self.target})
        self.admin('/api/admin/nodes/'+self.target_id+'/fault', 'POST', {'mode': mode})

    def mutate_container(self, kind, service):
        original = self.containers[service]
        current = inspect(original['Id'])
        if current['Created'] != original['Created'] or not current['State']['Running'] or current['State']['Paused']:
            raise RuntimeError('Container changed since preflight: '+service)
        action = {'kind': kind, 'id': original['Id'], 'created': original['Created'], 'service': service,
                  'project': current['Config']['Labels']['com.docker.compose.project']}
        if kind == 'disconnect':
            action['network'] = current['NetworkSettings']['Networks'][NETWORK]
        self.record(action)
        if kind == 'disconnect':
            command('docker', 'network', 'disconnect', NETWORK, original['Id'])
        else:
            command('docker', kind, original['Id'])

    def restore(self):
        errors = []
        for action in reversed(self.actions):
            if action.get('restored'):
                continue
            try:
                if action['kind'] == 'fault':
                    try:
                        nodes = self.admin('/api/admin/cluster')['nodes']
                        if not any(n['id'] == action['node_id'] and n['node_id'] in ['app-node-1', 'app-node-2', 'app-node-3'] for n in nodes):
                            raise RuntimeError('Refusing fault restore outside test nodes')
                        self.admin('/api/admin/nodes/'+action['node_id']+'/fault', 'POST', {'mode': 'none'})
                    except RuntimeError:
                        # The internal endpoint deliberately bypasses eligibility.
                        # Only clear this run's known local fault; never set one here.
                        index = action.get('index')
                        if index not in [1, 2, 3]:
                            raise RuntimeError('Old journal lacks target index for internal recovery')
                        current = inspect(self.containers[f'app-node-{index}']['Id'])
                        if current['Config']['Labels'].get('com.docker.compose.project') != APP:
                            raise RuntimeError('Foreign internal recovery target')
                        code, _, _ = self.http(index, '/internal/faults', 'POST', {'mode': 'none'}, internal=True)
                        if code != 204:
                            raise RuntimeError('Internal recovery failed: '+str(code))
                else:
                    current = inspect(action['id'])
                    labels = current['Config']['Labels']
                    if action['project'] not in [APP, INFRA] or labels.get('com.docker.compose.project') != action['project'] or labels.get('com.docker.compose.service') != action['service'] or current['Created'] != action['created']:
                        raise RuntimeError('Refusing restore of replaced/foreign container')
                    kind = action['kind']
                    if kind == 'stop' and not current['State']['Running']:
                        command('docker', 'start', action['id'])
                    elif kind == 'pause' and current['State']['Paused']:
                        command('docker', 'unpause', action['id'])
                    elif kind == 'disconnect' and NETWORK not in current['NetworkSettings']['Networks']:
                        network = json.loads(command('docker', 'network', 'inspect', NETWORK))[0]
                        if network['Id'] != action['network']['NetworkID']:
                            raise RuntimeError('Test network replaced; manual restore required')
                        options = []
                        for alias in action['network'].get('Aliases') or []:
                            options += ['--alias', alias]
                        requested_ip = (action['network'].get('IPAMConfig') or {}).get('IPv4Address')
                        if requested_ip:
                            options += ['--ip', requested_ip]
                        command('docker', 'network', 'connect', *options, NETWORK, action['id'])
                action['restored'] = True
                self.save_journal()
            except Exception as error:
                errors.append(str(error))
        if errors:
            raise RuntimeError('RESTORATION INCOMPLETE: '+'; '.join(errors))

    def select_target(self, index):
        self.target = index
        self.target_id = self.node_ids[index]
        self.survivors = [i for i in [1, 2, 3] if i != index]

    def snapshot(self, label):
        prefix = Path(self.args.report).with_suffix('')
        sql = "SET statement_timeout='3s'; SELECT node_id,state,health,reason,observed_at FROM cluster_nodes ORDER BY node_id; SELECT holder_id,term,expires_at,clock_timestamp() FROM manager_lease; SELECT * FROM cluster_membership; SELECT node_id,mode FROM node_faults;"
        result = subprocess.run(['docker', 'exec', self.containers['cockroach-1']['Id'], 'cockroach', 'sql', '--insecure', '--database=acervo_app_test', '--execute', sql], capture_output=True, text=True, timeout=6)
        Path(str(prefix)+'-'+label+'-state.txt').write_text(result.stdout+result.stderr)
        for index in [1, 2, 3]:
            logs = subprocess.run(['docker', 'logs', '--since', '2m', self.containers[f'app-node-{index}']['Id']], capture_output=True, text=True, timeout=3)
            Path(str(prefix)+'-'+label+f'-node-{index}.log').write_text(logs.stdout+logs.stderr)

    def transfer(self, label):
        started = time.monotonic()
        deadline = started+90
        attempts = []
        payload = ('distributed-failure-proof/'+self.run_id+'/'+label).encode()
        digest = hashlib.sha256(payload).hexdigest()
        manifest = {'idempotency_key': str(uuid.uuid4()), 'name': label+'-'+uuid.uuid4().hex,
                    'size': len(payload), 'sha256': digest,
                    'parts': [{'index': 0, 'offset': 0, 'size': len(payload), 'sha256': digest}]}
        def request(path, method='GET', body=None, expected=200, index=None):
            while time.monotonic() < deadline:
                for candidate in ([index] if index else self.survivors):
                    remaining = deadline-time.monotonic()
                    if remaining <= 0:
                        break
                    try:
                        code, raw, _ = self.http(candidate, path, method, body, self.user_cookie, timeout=min(20, remaining))
                    except (TimeoutError, http.client.HTTPException, OSError) as error:
                        attempts.append({'node': candidate, 'method': method, 'status': 0, 'elapsed': time.monotonic()-started, 'ambiguous': str(error)})
                        self.event('ambiguous_transfer_response', label=label, attempt=attempts[-1])
                        if method == 'PUT':
                            op_path = '/api/uploads/'+path.split('/')[3]
                            index_part = int(path.rsplit('/', 1)[1])
                            known_missing = False
                            while time.monotonic() < deadline and not known_missing:
                                for peer in self.survivors:
                                    remaining = deadline-time.monotonic()
                                    if remaining <= 0:
                                        break
                                    try:
                                        observed, data, _ = self.http(peer, op_path, cookie=self.user_cookie, timeout=min(20, remaining))
                                    except (TimeoutError, http.client.HTTPException, OSError):
                                        continue
                                    if observed != 200:
                                        continue
                                    operation_state = json.loads(data)
                                    part = operation_state['parts'][index_part]
                                    self.event('ambiguous_transfer_reconciled', label=label, operation_status=operation_state['status'], availability=part['availability'])
                                    if operation_state['status'] == 'available' or part['available']:
                                        return b''  # No 204 was observed; metadata confirmed prior receipt/publication.
                                    known_missing = part['availability'] == 'missing'
                                    if known_missing:
                                        break
                                if not known_missing:
                                    time.sleep(.3)
                        continue
                    attempts.append({'node': candidate, 'method': method, 'status': code, 'elapsed': time.monotonic()-started})
                    if code == expected:
                        return raw
                    if code != 503:
                        raise AssertionError((label, path, code, raw))
                    self.event('transient_503', label=label, attempt=attempts[-1])
                    try:
                        self.snapshot(label+'-'+str(len(attempts)))
                    except subprocess.TimeoutExpired:
                        self.event('snapshot_timeout', label=label)
                time.sleep(.3)
            raise AssertionError('Transfer deadline 90s: '+label)
        operation = json.loads(request('/api/uploads', 'POST', manifest, 201))['id']
        request('/api/uploads/'+operation+'/parts/0', 'PUT', payload, 204)
        while True:
            op = json.loads(request('/api/uploads/'+operation))
            if op['status'] == 'available':
                break
            if time.monotonic() >= deadline:
                raise AssertionError('Publication deadline 90s: '+label)
            time.sleep(.3)
        for survivor in self.survivors:
            body = request('/api/files/'+op['file_id']+'/download', index=survivor)
            assert body == payload, (label, survivor, 'download content mismatch')
        return {'operation_id': operation, 'download_nodes': self.survivors, 'sha256': digest, 'requests': attempts, 'transfer_elapsed': time.monotonic()-started}

    def fault_case(self, mode):
        started = time.monotonic()
        conn = http.client.HTTPConnection('127.0.0.1', 28100+self.target, timeout=8)
        try:
            assert self.http(self.target, '/api/me', cookie=self.user_cookie, connection=conn)[0] == 200
            original_socket = conn.sock
            assert original_socket is not None
            self.fault(mode)
            self.wait(lambda: self.excluded(self.target_id), 'automatic exclusion for '+mode)
            detection_elapsed = time.monotonic()-started
            status, _, _ = self.http(self.target, '/api/me', cookie=self.user_cookie, connection=conn)
            assert status == 503, (mode, status)
            assert conn.sock is original_socket, 'Connection was replaced; keep-alive proof invalid'
            transfer = self.transfer('simulation-'+str(self.target)+'-'+mode)
            self.event('simulated_'+mode, node=self.target, automatically_excluded=True, persistent_direct_status=status, detection_elapsed=detection_elapsed, elapsed=time.monotonic()-started, **transfer)
        finally:
            conn.close()
            self.restore()
            self.wait(self.ready, 'restore after '+mode)

    def container_case(self, kind, sql=False, require_succession=False):
        leadership = {}
        if require_succession:
            view = self.admin('/api/admin/cluster')
            expected_names = {f'app-node-{index}' for index in [1, 2, 3]}
            nodes = {node['node_id']: node for node in view['nodes']}
            if not expected_names.issubset(nodes):
                raise RuntimeError('Three named test nodes required before manager partition')
            candidates = [index for index in [1, 2, 3] if nodes[f'app-node-{index}']['id'] == view['manager_id']]
            if len(candidates) != 1:
                raise RuntimeError('Current manager does not belong to the three test nodes')
            self.select_target(candidates[0])
            leadership = {'manager_before': view['manager_id'], 'term_before': view['manager_term']}
            self.event('manager_partition_selected', node=self.target, **leadership)
        started = time.monotonic()
        try:
            self.mutate_container(kind, ('cockroach-' if sql else 'app-node-')+str(self.target))
            self.wait(lambda: self.excluded(self.target_id), 'automatic exclusion after '+kind)
            detection_elapsed = time.monotonic()-started
            if require_succession:
                view = self.admin('/api/admin/cluster')
                leadership.update(manager_after=view['manager_id'], term_after=view['manager_term'])
                assert leadership['manager_after'] in self.node_ids.values() and leadership['manager_after'] != leadership['manager_before'] and leadership['term_after'] > leadership['term_before'], leadership
                self.event('automatic_manager_succession', node=self.target, detection_elapsed=detection_elapsed, **leadership)
            if sql:
                assert self.http(self.target, '/api/me', cookie=self.user_cookie)[0] == 503
            transfer = self.transfer(('sql-' if sql else 'backend-')+kind+'-'+str(self.target))
            self.event(('real_sql_' if sql else 'real_backend_')+kind, node=self.target, automatically_excluded=True, detection_elapsed=detection_elapsed, elapsed=time.monotonic()-started, **leadership, **transfer)
        finally:
            self.restore()
            self.wait(self.ready, 'restore after '+kind)

    def quorum_case(self):
        payload = b'quorum-proof-'+self.run_id.encode()
        digest = hashlib.sha256(payload).hexdigest()
        manifest = {'idempotency_key': str(uuid.uuid4()), 'name': 'quorum-'+self.run_id,
                    'size': len(payload), 'sha256': digest,
                    'parts': [{'index': 0, 'offset': 0, 'size': len(payload), 'sha256': digest}]}
        status, raw, _ = self.http(1, '/api/uploads', 'POST', manifest, self.user_cookie)
        assert status == 201, status
        operation = json.loads(raw)['id']
        observed = []
        try:
            self.mutate_container('pause', 'cockroach-2')
            self.mutate_container('pause', 'cockroach-3')
            # Exceed both manager lease and metadata deadlines while quorum is absent.
            time.sleep(15)
            for index in [1, 2, 3]:
                for path, method, body in [('/api/me', 'GET', None), ('/api/uploads/'+operation+'/parts/0', 'PUT', payload)]:
                    try:
                        code, _, _ = self.http(index, path, method, body, self.user_cookie)
                    except (OSError, http.client.HTTPException):
                        code = 0  # Connection failure/timeout is absence of success, not HTTP 503.
                    assert code == 0 or code >= 500, (index, path, code)
                    observed.append({'node': index, 'path': path, 'status': code})
            self.event('real_sql_quorum_loss', requests=observed, operation_id=operation)
        finally:
            self.restore()
            self.wait(self.ready, 'restore SQL quorum', seconds=150)
        status, raw, _ = self.http(2, '/api/uploads/'+operation, cookie=self.user_cookie)
        assert status == 200
        op = json.loads(raw)
        assert op['status'] == 'pending' and not op.get('file_id') and not op['parts'][0]['available'], op
        self.event('quorum_publication_barrier', status=op['status'], part_available=False)
        status, _, _ = self.http(2, '/api/uploads/'+operation+'/cancel', 'POST', {}, self.user_cookie)
        assert status == 200


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    parser.add_argument('--start-mode', choices=['backend', 'sql', 'storage', 'control', 'total'], default='backend')
    parser.add_argument('--start-node', type=int, choices=[1, 2, 3], default=1)
    parser.add_argument('--phase', choices=['all', 'app', 'sql', 'network'], default='all')
    parser.add_argument('--report', default='/tmp/acervo-distributed-failures-report.json')
    parser.add_argument('--journal', default='/tmp/acervo-distributed-failures-journal.json')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    args = parser.parse_args()
    with open('/tmp/acervo-distributed-failures.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        proof = Proof(args)
        journal = Path(args.journal)
        if args.recover:
            data = json.loads(journal.read_text())
            proof.run_id, proof.actions = data['run_id'], data['actions']
            proof.restore()
            proof.wait(proof.ready, 'recovered stack', seconds=150)
            return
        if journal.exists() and any(not action.get('restored') for action in json.loads(journal.read_text())['actions']):
            raise RuntimeError('Pending recovery journal; run --recover first')
        proof.preflight(privileged=args.execute)
        if not args.execute:
            print('Read-only preflight complete. No faults or Docker mutations executed.')
            return
        def interrupted(signum, frame):
            raise KeyboardInterrupt('Interrupted; restoring test mutations')
        signal.signal(signal.SIGTERM, interrupted)
        try:
            proof.account()
            if args.phase in ['all', 'app']:
                for target in range(args.start_node, 4):
                    proof.select_target(target)
                    modes = ['backend', 'sql', 'storage', 'control', 'total']
                    if target == args.start_node:
                        modes = modes[modes.index(args.start_mode):]
                    for fault in modes:
                        proof.fault_case(fault)
                    proof.container_case('stop')
            if args.phase in ['all', 'app', 'network']:
                proof.select_target(1)
                proof.container_case('disconnect', require_succession=args.phase == 'network')
            if args.phase in ['all', 'sql', 'network']:
                proof.select_target(1)
                proof.container_case('disconnect', sql=True)
                proof.quorum_case()
            proof.report['complete'] = True
            proof.event('complete')
        finally:
            proof.restore()


if __name__ == '__main__':
    main()
