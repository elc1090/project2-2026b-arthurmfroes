#!/usr/bin/env python3
"""Check loss of authority without uploading data. Default: offline plan only.

Requires the sibling verify-distributed-failures.py harness and a private JSON
state: {"cookie": "acervo_session=...", "file_id": "UUID", "size": 268435456}.
Do not run alongside other infrastructure tests. --recover restores this journal.
"""
import argparse
import fcntl
import http.client
import importlib.util
import json
from pathlib import Path
import signal
import socket
import threading
import time
import uuid

MIB = 1024 * 1024


def harness():
    path = Path(__file__).with_name('verify-distributed-failures.py')
    spec = importlib.util.spec_from_file_location('failure_harness', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class Stream:
    def __init__(self, index, cookie, file_id, size, delay):
        self.index, self.expected, self.delay = index, size, delay
        self.stop = threading.Event()
        self.conn = http.client.HTTPConnection('127.0.0.1', 28100 + index, timeout=15)
        self.received, self.outcome, self.error = 0, None, None
        self.ended = None
        try:
            self.conn.request('GET', '/api/files/' + file_id + '/download', headers={'Cookie': cookie})
            self.response = self.conn.getresponse()
            if self.response.status != 200 or int(self.response.getheader('Content-Length', '-1')) != size:
                raise RuntimeError(f'Inconclusive: stream {index} did not start with expected 200/length')
            first = self.response.read(MIB)
            self.received = len(first)
            if len(first) != MIB or self.received >= size:
                raise RuntimeError('Inconclusive: stream ended before fault')
        except BaseException:
            self.conn.close()
            raise
        self.thread = threading.Thread(target=self.read, daemon=True)
        self.thread.start()

    def read(self):
        try:
            while not self.stop.wait(self.delay):
                body = self.response.read(MIB)
                self.received += len(body)
                if not body or self.received >= self.expected:
                    self.outcome = 'eof' if not body else 'complete'
                    return
        except socket.timeout:
            self.outcome = 'client_timeout'  # Never claim this as a server abort.
        except (OSError, http.client.HTTPException) as error:
            self.outcome, self.error = 'transport_error', type(error).__name__
            if isinstance(error, http.client.IncompleteRead):
                self.received += len(error.partial)
        finally:
            self.ended = time.monotonic()

    def close(self):
        self.stop.set()
        if self.conn.sock:
            try:
                self.conn.sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        self.conn.close()
        self.thread.join(timeout=16)

    def evidence(self, fault_at):
        return {'endpoint': self.index, 'bytes': self.received, 'expected': self.expected,
                'outcome': self.outcome, 'error': self.error,
                'seconds_after_fault': None if self.ended is None else self.ended - fault_at}


def execute(proof, args):
    state_path = Path(args.state)
    if state_path.stat().st_mode & 0o077:
        raise RuntimeError('State with cookie must have private permissions (chmod 600)')
    state = json.loads(state_path.read_text())
    file_id = str(uuid.UUID(state['file_id']))
    size = state['size']
    if type(size) is not int or size < 128 * MIB or not state.get('cookie'):
        raise ValueError('Use an existing large published file and its owner cookie')
    proof.user_cookie = state['cookie']
    proof.preflight(privileged=True)
    config = json.loads(Path(args.app_compose).read_text())
    # This stack's manager lease is explicitly checked, rather than inferred from
    # the three-second stream guard or Nginx worker shutdown interval.
    lease = config['services']['app-node-1']['environment'].get('MANAGER_LEASE_TTL', '')
    if lease != '12s':
        raise RuntimeError('Review wait budget for changed MANAGER_LEASE_TTL: ' + str(lease))
    streams = []
    fault_at = None
    try:
        for index in (0, args.direct_node):
            streams.append(Stream(index, state['cookie'], file_id, size, args.read_delay))
        if any(s.ended is not None for s in streams):
            raise RuntimeError('Inconclusive: a stream ended before partition')
        proof.event('streams_started', endpoints=[s.index for s in streams], bytes_each=MIB, size=size)
        fault_at = time.monotonic()
        proof.mutate_container('pause', 'cockroach-2')
        proof.mutate_container('pause', 'cockroach-3')
        if any(s.ended is not None and s.ended <= fault_at for s in streams):
            raise RuntimeError('Inconclusive: stream ended naturally before fault')
        proof.event('quorum_paused', elapsed=time.monotonic() - fault_at)
        time.sleep(15)  # Exceeds configured 12s authority lease.
        results = []
        for index in (0, 1, 2, 3):
            status, _, _ = proof.http(index, '/api/me', cookie=state['cookie'])
            results.append({'endpoint': index, 'status': status})
        proof.event('new_requests_without_authority', requests=results)
        assert all(item['status'] == 503 for item in results), 'Every new request must return HTTP503'
        deadline = fault_at + args.observe_seconds
        while time.monotonic() < deadline and any(s.ended is None for s in streams):
            time.sleep(.25)
        evidence = [s.evidence(fault_at) for s in streams]
        proof.event('streams_after_authority_loss', streams=evidence)
        assert all(s.outcome in ('eof', 'transport_error') and s.received < size
                   and s.ended > fault_at for s in streams), 'Stream abort unproven (complete/timeout/unfinished is not PASS)'
        proof.event('authority_loss_assertions_pass')
    finally:
        for stream in streams:
            stream.close()
        proof.restore()
        proof.wait(proof.ready, 'restore three ready nodes', seconds=150)
        proof.event('restored', ready_nodes=3, all_actions_restored=all(a.get('restored') for a in proof.actions))
    proof.report['complete'] = True
    proof.event('authority_loss_pass')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app-compose', default='/tmp/acervo-application-test.json')
    parser.add_argument('--infra-compose', default='/tmp/acervo-infra-runtime.json')
    parser.add_argument('--state', help='Private existing-file/owner-cookie JSON; never included in report')
    parser.add_argument('--report', default='/tmp/acervo-authority-loss-report.json')
    parser.add_argument('--journal', default='/tmp/acervo-authority-loss-journal.json')
    parser.add_argument('--direct-node', type=int, choices=[1, 2, 3], default=1)
    parser.add_argument('--read-delay', type=float, default=.25)
    parser.add_argument('--observe-seconds', type=float, default=60)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--recover', action='store_true')
    args = parser.parse_args()
    if not args.execute and not args.recover:
        print('OFFLINE PLAN: open existing file through LB and direct backend; pause test SQL2/3; require incomplete streams and four HTTP503; restore journal. No services contacted.')
        return
    if args.execute and not args.state:
        parser.error('--execute requires --state')
    if not .1 <= args.read_delay <= 1 or not 30 <= args.observe_seconds <= 120:
        parser.error('read-delay must be .1..1s and observe-seconds 30..120s')
    args.phase, args.start_node, args.start_mode = 'authority', 1, 'backend'
    with open('/tmp/acervo-distributed-failures.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        proof = harness().Proof(args)
        journal = Path(args.journal)
        if args.recover:
            data = json.loads(journal.read_text())
            proof.run_id, proof.actions = data['run_id'], data['actions']
            proof.restore()
            proof.wait(proof.ready, 'recovered stack', seconds=150)
            return
        if journal.exists() and any(not a.get('restored') for a in json.loads(journal.read_text())['actions']):
            raise RuntimeError('Pending restoration: use --recover')
        def interrupted(*_):
            raise KeyboardInterrupt('Interrupted; restore journal')
        signal.signal(signal.SIGTERM, interrupted)
        execute(proof, args)


if __name__ == '__main__':
    main()
