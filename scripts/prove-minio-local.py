#!/usr/bin/env python3
"""Exercise local MinIO persistence in the isolated acervo-infra-runtime project.
Requires boto3 in a separate test environment. Never points at the regular dev stack.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
import uuid

import boto3
from botocore.config import Config

parser = argparse.ArgumentParser()
parser.add_argument('--compose', required=True)
parser.add_argument('--report', required=True)
args = parser.parse_args()
config = json.loads(subprocess.check_output(
    ['docker', 'compose', '-f', args.compose, 'config', '--format', 'json']))
if config['name'] != 'acervo-infra-runtime':
    raise SystemExit('Refusing to change services outside acervo-infra-runtime')
clients = {n: boto3.client('s3', endpoint_url=f'http://127.0.0.1:{27900+n}',
    aws_access_key_id='minioadmin', aws_secret_access_key='minioadmin', region_name='us-east-1',
    config=Config(connect_timeout=3, read_timeout=15, retries={'max_attempts': 0},
                  s3={'addressing_style': 'path'})) for n in range(1,4)}
report = {'prefix': f'infrastructure-proof/{uuid.uuid4()}', 'sites': [], 'complete': False}
bucket = 'drive-clone'


def compose(*cmd):
    subprocess.run(['docker', 'compose', '-f', args.compose, *cmd], check=True)


def ready(n):
    for _ in range(60):
        try:
            clients[n].head_bucket(Bucket=bucket)
            return
        except Exception:
            time.sleep(1)
    raise RuntimeError(f'Site {n} did not become ready')


def check_object(n, key, version, expected):
    response = clients[n].get_object(Bucket=bucket, Key=key, VersionId=version)
    checksum = hashlib.sha256()
    size = 0
    for block in response['Body'].iter_chunks(1024 * 1024):
        checksum.update(block)
        size += len(block)
    assert size == len(expected)
    assert checksum.hexdigest() == hashlib.sha256(expected).hexdigest()
    return {'key': key, 'version': version, 'bytes': size, 'sha256': checksum.hexdigest()}


try:
    for n in range(1,4):
        ready(n)
    # Seed an object and wait for its version to appear in each site's listing.
    # The following isolated GETs are what prove its local persistence.
    baseline = b'identical immutable bytes across explicit writes\n'
    shared_key = report['prefix'] + '/already-replicated'
    baseline_version = clients[1].put_object(Bucket=bucket, Key=shared_key, Body=baseline)['VersionId']
    for n in range(1,4):
        for _ in range(60):
            versions = clients[n].list_object_versions(Bucket=bucket, Prefix=shared_key).get('Versions', [])
            if any(v['VersionId'] == baseline_version for v in versions): break
            time.sleep(1)
        else: raise RuntimeError(f'Baseline not listed at site {n}')
    for n in range(1,4):
        compose('start', f'minio-{n}')
        ready(n)
        peers = [f'minio-{peer}' for peer in range(1,4) if peer != n]
        compose('stop', *peers)
        c = clients[n]
        records = [check_object(n, shared_key, baseline_version, baseline)]
        new_version = c.put_object(Bucket=bucket, Key=shared_key, Body=baseline)['VersionId']
        assert new_version != baseline_version
        records.append(check_object(n, shared_key, new_version, baseline))
        parts = [b'A' * (6 * 1024 * 1024), b'B' * (1024 * 1024)]
        key = f"{report['prefix']}/multipart-{n}"
        upload = c.create_multipart_upload(Bucket=bucket, Key=key)['UploadId']
        receipts = []
        for position, body in enumerate(parts, 1):
            result = c.upload_part(Bucket=bucket, Key=key, UploadId=upload, PartNumber=position, Body=body)
            receipts.append({'PartNumber': position, 'ETag': result['ETag']})
        complete = c.complete_multipart_upload(Bucket=bucket, Key=key, UploadId=upload,
                                              MultipartUpload={'Parts': receipts})
        assert '-' in complete['ETag'], 'Expected multipart ETag'
        records.append(check_object(n, key, complete['VersionId'], b''.join(parts)))
        empty_key = f"{report['prefix']}/empty-{n}"
        empty_version = c.put_object(Bucket=bucket, Key=empty_key, Body=b'')['VersionId']
        records.append(check_object(n, empty_key, empty_version, b''))
        incomplete_key = f"{report['prefix']}/incomplete-{n}"
        incomplete_id = c.create_multipart_upload(Bucket=bucket, Key=incomplete_key)['UploadId']
        c.upload_part(Bucket=bucket, Key=incomplete_key, UploadId=incomplete_id, PartNumber=1, Body=parts[0])
        compose('restart', f'minio-{n}')
        ready(n)
        for record, expected in zip(records, [baseline, baseline, b''.join(parts), b'']):
            check_object(n, record['key'], record['version'], expected)
        retained = c.list_parts(Bucket=bucket, Key=incomplete_key, UploadId=incomplete_id)['Parts']
        assert retained[0]['Size'] == len(parts[0])
        c.abort_multipart_upload(Bucket=bucket, Key=incomplete_key, UploadId=incomplete_id)
        report['sites'].append({'site': n, 'isolated_and_after_restart': records,
                                'multipart_etag': complete['ETag'], 'incomplete_parts_retained': True})
        Path(args.report).write_text(json.dumps(report, indent=2))
        print(f'PASS: site {n}, isolated PUT/multipart/empty/version rewrite and restart', flush=True)
    report['complete'] = True
finally:
    compose('start', 'minio-1', 'minio-2', 'minio-3')
    Path(args.report).write_text(json.dumps(report, indent=2))
