#!/usr/bin/env python3
"""Print isolated, resource-limited Compose JSON. Does not start containers."""
import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
config = json.loads(subprocess.check_output([
    'docker', 'compose', '-f', str(root / 'docker-compose.dev.yml'),
    'config', '--format', 'json']))
config['name'] = 'acervo-infra-runtime'
for kind in ['networks', 'volumes']:
    for resource in config.get(kind, {}).values(): resource.pop('name', None)
config['services'].pop('load-balancer')
for name, service in config['services'].items():
    service.pop('ports', None)
    if name.startswith('cockroach-') and name != 'cockroach-init':
        n = int(name[-1])
        service.pop('build', None)
        service['image'] = 'cockroachdb/cockroach:v23.2.0'
        service['entrypoint'] = ['/cockroach/cockroach']
        service['command'] = ['start', '--insecure', '--listen-addr=:26257',
            f'--advertise-addr={name}:26257', '--http-addr=:8080',
            '--join=cockroach-1:26257,cockroach-2:26257,cockroach-3:26257',
            '--store=/cockroach/cockroach-data', '--cache=64MiB', '--max-sql-memory=64MiB']
        service['mem_limit'] = '512m'
        # Three-node SQL + application probes exceeded the old half-CPU quota.
        service['cpus'] = 2
        service['ports'] = [{'target': 26257, 'published': str(27656+n),
                             'host_ip': '127.0.0.1', 'protocol': 'tcp'}]
    elif name.startswith('minio-') and name != 'minio-init':
        n = int(name[-1])
        service['mem_limit'] = '384m'
        service['cpus'] = 0.5
        service['environment']['GOMEMLIMIT'] = '256MiB'
        service['ports'] = [{'target': 9000, 'published': str(27900+n),
                             'host_ip': '127.0.0.1', 'protocol': 'tcp'}]
    elif name.startswith('backend-node-'):
        n = int(name[-1])
        service['image'] = 'acervo-infra-runtime-backend-node-1'
        service['mem_limit'] = '64m'
        service['cpus'] = 0.25
        service['ports'] = [{'target': 8080, 'published': str(27800+n),
                             'host_ip': '127.0.0.1', 'protocol': 'tcp'}]
print(json.dumps(config, indent=2))
