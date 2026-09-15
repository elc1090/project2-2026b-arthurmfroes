#!/usr/bin/env python3
"""Generate an application-only test stack using the existing isolated data services.

Build images acervo-application-backend:test and acervo-application-lb:test first.
The dedicated acervo_app_test database must exist; this script starts nothing.
"""
import json

services = {}
for index in range(1, 4):
    name = f'app-node-{index}'
    services[name] = {
        'image': 'acervo-application-backend:test',
        'mem_limit': '192m',
        'environment': {
            'PORT': '8080', 'NODE_ID': name,
            'NODE_BOOTSTRAP': 'true',
            'BACKEND_ENDPOINT': f'http://{name}:8080',
            'DATABASE_URL': f'postgresql://root@cockroach-{index}:26257/acervo_app_test?sslmode=disable',
            'S3_ENDPOINT': f'http://minio-{index}:9000',
            'S3_BUCKET': 'drive-clone', 'S3_ACCESS_KEY': 'minioadmin', 'S3_SECRET_KEY': 'minioadmin',
            'CONTROL_TOKEN': 'acervo-application-test-token', 'CONTROL_INTERVAL': '1s',
            'CONTROL_TIMEOUT': '2s', 'MANAGER_LEASE_TTL': '12s', 'FAILURE_THRESHOLD': '2',
            'GOMEMLIMIT': '144MiB', 'ENABLE_DEV_FAULTS': 'true', 'ADMIN_LOGIN': 'admin', 'ADMIN_PASSWORD': 'admin', 'SECURE_COOKIES': 'false',
        },
        'ports': [f'127.0.0.1:{28100+index}:8080'],
        'networks': ['data'],
    }
services['load-balancer'] = {
    'image': 'acervo-application-lb:test', 'mem_limit': '192m',
    'environment': {
        'CONTROL_ENDPOINTS': ' '.join(f'http://app-node-{i}:8080' for i in range(1,4)),
        'CONTROL_TOKEN': 'acervo-application-test-token',
    },
    'ports': ['127.0.0.1:28100:8080'], 'networks': ['data'],
}
print(json.dumps({'name': 'acervo-app-test', 'services': services,
                  'networks': {'data': {'external': True, 'name': 'acervo-infra-runtime_default'}}}, indent=2))
