"""Opt-in real Docker/Nginx/DNS/TLS integration; leaves other containers alone."""
import json
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request
import uuid

from test_reconcile import response


BACKEND = '''import http.server,json,ssl,threading
from pathlib import Path
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  if self.path=='/internal/cluster':
   if self.headers.get('Authorization')!='Bearer test-token': self.send_error(401);return
   state=json.loads(Path('/fixture/state.json').read_text())
   if state is None: self.send_error(503);return
   self.send_response(200);self.end_headers();self.wfile.write(json.dumps(state).encode());return
  self.send_response(200);self.end_headers();self.wfile.write(b'https' if isinstance(self.connection,ssl.SSLSocket) else b'http')
 def log_message(self,*a):pass
plain=http.server.ThreadingHTTPServer(('0.0.0.0',8081),Handler)
secure=http.server.ThreadingHTTPServer(('0.0.0.0',8443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixture/server.crt','/fixture/server.key')
secure.socket=ctx.wrap_socket(secure.socket,server_side=True)
threading.Thread(target=plain.serve_forever,daemon=True).start()
secure.serve_forever()
'''


@unittest.skipUnless(os.getenv('ACERVO_NGINX_TEST_IMAGE'), 'ACERVO_NGINX_TEST_IMAGE enables isolated Docker integration')
class RuntimeTests(unittest.TestCase):
    def test_dns_mixed_tls_fallback_and_expiry(self):
        image = os.environ['ACERVO_NGINX_TEST_IMAGE']
        suffix = uuid.uuid4().hex[:10]
        network, backend, lb = [f'acervo-lb-test-{kind}-{suffix}' for kind in ('net', 'backend', 'lb')]
        def docker(*args):
            return subprocess.check_output(['docker', *args], text=True, stderr=subprocess.STDOUT).strip()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'backend.py').write_text(BACKEND)
            (root/'index.html').write_text('Acervo SPA fixture')
            def openssl(*args):
                subprocess.run(['openssl', *args], cwd=root, check=True, capture_output=True)
            openssl('req','-x509','-newkey','rsa:2048','-nodes','-keyout','ca.key','-out','ca.crt','-days','1','-subj','/CN=Acervo Test CA')
            openssl('req','-newkey','rsa:2048','-nodes','-keyout','server.key','-out','server.csr','-subj','/CN=backend')
            (root/'extensions').write_text('subjectAltName=DNS:backend\nextendedKeyUsage=serverAuth\n')
            openssl('x509','-req','-in','server.csr','-CA','ca.crt','-CAkey','ca.key','-CAcreateserial','-out','server.crt','-days','1','-extfile','extensions')
            members=[{'ID':'plain','State':'ready','BackendEndpoint':'http://backend:8081'}, {'ID':'tls','State':'ready','BackendEndpoint':'https://backend:8443'}]
            def state(data):
                (root/'next.json').write_text(json.dumps(data));(root/'next.json').replace(root/'state.json')
            state(response(members=members,ttl=800))
            docker('network','create',network)
            try:
                docker('run','-d','--name',backend,'--network',network,'--network-alias','backend','--network-alias','wrongname','-v',f'{root}:/fixture:ro','--entrypoint','python3',image,'/fixture/backend.py')
                docker('run','-d','--name',lb,'--network',network,'-p','127.0.0.1::8080','-e','CONTROL_ENDPOINTS=http://missing:8081 http://backend:8081','-e','CONTROL_TOKEN=test-token','-e','CONTROL_TIMEOUT=0.15','-e','CONTROL_INTERVAL=0.05','-e','CONTROL_MAX_STALE=0.4','-v',f'{root}/ca.crt:/etc/ssl/cert.pem:ro','-v',f'{root}/index.html:/usr/share/nginx/html/index.html:ro',image)
                port=docker('port',lb,'8080/tcp').split(':')[-1]
                origin=f'http://127.0.0.1:{port}'
                def get(path):
                    try:
                        with urllib.request.urlopen(origin+path,timeout=2) as r:return r.status,r.read().decode()
                    except urllib.error.HTTPError as e:return e.code,e.read().decode()
                def wait_status(expected,path='/api/files'):
                    deadline=time.monotonic()+8
                    while time.monotonic()<deadline:
                        try:
                            status,body=get(path)
                            if status==expected:return body
                        except OSError:pass
                        time.sleep(.05)
                    self.fail(f'expected {expected}; nginx logs:\n'+docker('logs','--tail','30',lb))
                wait_status(200)
                time.sleep(.2)
                observed=set()
                for _ in range(40):
                    status, body=get('/api/files')
                    if status==200: observed.add(body)
                self.assertEqual(observed,{'http','https'})
                self.assertEqual(get('/internal/cluster')[0],404)
                self.assertIn('Acervo SPA fixture',get('/some/folder')[1])
                # Same trusted CA, wrong DNS name: certificate verification must fail.
                bad=[{'ID':'tls','State':'ready','BackendEndpoint':'https://wrongname:8443'}]
                state(response(version=2,members=bad,ttl=800));wait_status(502)
                state(response(version=3,members=[],ttl=800));wait_status(503)
                state(response(version=4,members=members,ttl=800));wait_status(200)
                state(None);wait_status(503)
                state(response(version=5,members=members,ttl=800));wait_status(200)
                print('real nginx: Docker DNS, HTTP+verified HTTPS, bad TLS rejected, control fallback, SPA, zero members and expiry passed')
            finally:
                for name in (lb,backend):subprocess.run(['docker','rm','-f',name],capture_output=True)
                subprocess.run(['docker','network','rm',network],capture_output=True)


if __name__=='__main__':unittest.main()
