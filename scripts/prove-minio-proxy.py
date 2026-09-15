#!/usr/bin/env python3
"""Prove that successful remote HEAD/GET is not a local-copy receipt.
Runs only against acervo-infra-runtime and restores its temporary bucket rules.
Requires boto3 in a separate test environment.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import time
import uuid

import boto3
from botocore.config import Config
from botocore.exceptions import ClientError, ReadTimeoutError

parser = argparse.ArgumentParser()
parser.add_argument('--compose', required=True)
parser.add_argument('--report', required=True)
args = parser.parse_args()
compose = ['docker', 'compose', '-f', args.compose]
config = json.loads(subprocess.check_output([*compose, 'config', '--format', 'json']))
if config['name'] != 'acervo-infra-runtime':
    raise SystemExit('Refusing to change services outside acervo-infra-runtime')
clients = {n: boto3.client('s3', endpoint_url=f'http://127.0.0.1:{27900+n}',
    aws_access_key_id='minioadmin', aws_secret_access_key='minioadmin', region_name='us-east-1',
    config=Config(connect_timeout=3, read_timeout=15, retries={'max_attempts': 0},
                  s3={'addressing_style': 'path'})) for n in range(1,4)}
bucket = 'proxy-proof-' + uuid.uuid4().hex[:12]
clients[1].create_bucket(Bucket=bucket)
report = {'bucket': bucket, 'sites': [], 'complete': False}
original = None
try:
    for _ in range(60):
        try:
            for c in clients.values():
                c.head_bucket(Bucket=bucket)
                assert len(c.get_bucket_replication(Bucket=bucket)['ReplicationConfiguration']['Rules']) == 2
            original = clients[1].get_bucket_replication(Bucket=bucket)['ReplicationConfiguration']
            break
        except ClientError: time.sleep(1)
    if original is None: raise RuntimeError('Temporary bucket replication did not initialize')
    disabled = copy.deepcopy(original)
    for rule in disabled['Rules']: rule['Status'] = 'Disabled'
    clients[1].put_bucket_replication(Bucket=bucket, ReplicationConfiguration=disabled)
    body = b'proxy-proof: source-only bytes\n'
    version = clients[1].put_object(Bucket=bucket, Key='source-only', Body=body)['VersionId']
    report['version'] = version
    report['sha256'] = hashlib.sha256(body).hexdigest()
    for n in [2,3]:
        assert not clients[n].list_object_versions(Bucket=bucket).get('Versions')
        for attempt in range(60):
            try:
                head = clients[n].head_object(Bucket=bucket, Key='source-only', VersionId=version)
                break
            except ClientError:
                if attempt == 59: raise
                time.sleep(1)
        assert head['ResponseMetadata']['HTTPStatusCode'] == 200
        result = clients[n].get_object(Bucket=bucket, Key='source-only', VersionId=version)
        assert result['Body'].read() == body
        assert not clients[n].list_object_versions(Bucket=bucket).get('Versions')
        report['sites'].append({'site': n, 'head_with_source': 200, 'get_matches': True,
                                'version_listing_empty_after_get': True})
    subprocess.run([*compose, 'stop', 'minio-1'], check=True)
    for item in report['sites']:
        try:
            clients[item['site']].head_object(Bucket=bucket, Key='source-only', VersionId=version)
        except ClientError as error:
            item['head_without_source'] = error.response['ResponseMetadata']['HTTPStatusCode']
            item['error'] = error.response['Error']['Code']
        except ReadTimeoutError:
            item['head_without_source'] = 'timeout after 15s'
        else: raise AssertionError('HEAD unexpectedly succeeded with no source')
        clients[item['site']].head_bucket(Bucket=bucket)
        item['local_bucket_still_available'] = True
    report['complete'] = True
finally:
    subprocess.run([*compose, 'start', 'minio-1'], check=True)
    if original is not None:
        for attempt in range(60):
            try:
                clients[1].put_bucket_replication(Bucket=bucket, ReplicationConfiguration=original)
                report['replication_restored'] = True
                break
            except Exception:
                if attempt == 59: raise
                time.sleep(1)
    Path(args.report).write_text(json.dumps(report, indent=2))
print(json.dumps(report, indent=2))
