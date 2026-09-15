#!/usr/bin/env python3
"""Generate an isolated fourth-node Compose file using read-only Docker inspection.

Never starts services, creates volumes, calls SQL, or mutates cluster topology.
The generated file contains test credentials and must remain outside the repository.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

PROJECT = "acervo-node4-test"
NETWORK = "acervo-infra-runtime_default"
APP_IMAGE = "acervo-application-backend:topology"
ROOT = Path(__file__).resolve().parents[1]
VOLUMES = ("cockroach-4-data", "minio-4-data")


def inspect(kind, name, required=True):
    result = subprocess.run(
        ["docker", kind, "inspect", name], capture_output=True, text=True, check=False
    )
    if result.returncode:
        if required:
            raise RuntimeError(f"Required Docker {kind} is unavailable: {name}")
        return None
    return json.loads(result.stdout)[0]


def environment(container):
    return dict(value.split("=", 1) for value in container["Config"]["Env"] if "=" in value)


def compose_document(app, minio, cockroach):
    app_env, minio_env = environment(app), environment(minio)
    required = ("CONTROL_TOKEN", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY")
    if any(not app_env.get(key) for key in required):
        raise RuntimeError("Reference application lacks required test configuration")
    if any(not minio_env.get(key) for key in ("MINIO_ROOT_USER", "MINIO_ROOT_PASSWORD")):
        raise RuntimeError("Reference MinIO lacks administrative test configuration")
    if cockroach["Config"]["Image"] != "cockroachdb/cockroach:v23.2.0":
        raise RuntimeError("Reference Cockroach must use v23.2.0")
    for container in (app, minio, cockroach):
        if NETWORK not in container["NetworkSettings"]["Networks"]:
            raise RuntimeError("Reference service is not attached to the expected test network")
    env = {key: app_env[key] for key in required}
    env.update({
        "PORT": "8080", "NODE_ID": "app-node-4", "NODE_BOOTSTRAP": "false",
        "DATABASE_URL": "postgresql://root@cockroach-4:26257/acervo_app_test?sslmode=disable",
        "BACKEND_ENDPOINT": "http://app-node-4:8080",
        "S3_ENDPOINT": "http://minio-4:9000", "ADMIN_LOGIN": "admin",
        "ADMIN_PASSWORD": "admin", "ENABLE_DEV_FAULTS": "true", "SECURE_COOKIES": "false",
        "GOMEMLIMIT": "144MiB",
    })
    for key in ("CONTROL_INTERVAL", "CONTROL_TIMEOUT", "MANAGER_LEASE_TTL", "FAILURE_THRESHOLD"):
        if key in app_env:
            env[key] = app_env[key]
    # Compose interpolates '$' even in JSON. Preserve literal inspected secrets.
    escaped = lambda values: {key: value.replace("$", "$$") for key, value in values.items()}
    return {
        "name": PROJECT,
        "services": {
            "cockroach-4": {
                "image": cockroach["Image"], "pull_policy": "never", "hostname": "cockroach-4",
                "command": ["start", "--insecure", "--listen-addr=:26257",
                            "--advertise-addr=cockroach-4:26257", "--http-addr=:8080",
                            "--join=cockroach-1:26257,cockroach-2:26257,cockroach-3:26257",
                            "--store=/cockroach/cockroach-data", "--cache=64MiB", "--max-sql-memory=64MiB"],
                "mem_limit": "512m", "cpus": 2.0,
                "ports": ["127.0.0.1:27660:26257"],
                "volumes": ["cockroach-4-data:/cockroach/cockroach-data"], "networks": ["cluster"],
            },
            "minio-4": {
                "image": minio["Image"], "pull_policy": "never",
                "command": ["server", "/data", "--console-address", ":9001"],
                "environment": escaped({**{key: minio_env[key] for key in ("MINIO_ROOT_USER", "MINIO_ROOT_PASSWORD")},"GOMEMLIMIT":"256MiB"}),
                "mem_limit": "384m", "cpus": 0.5,
                "ports": ["127.0.0.1:27904:9000"],
                "volumes": ["minio-4-data:/data"], "networks": ["cluster"],
            },
            "app-node-4": {
                "image": APP_IMAGE, "pull_policy": "never", "environment": escaped(env),
                "mem_limit": "192m",
                "ports": ["127.0.0.1:28104:8080"], "networks": ["cluster"],
                "depends_on": {"cockroach-4": {"condition": "service_started"},
                               "minio-4": {"condition": "service_started"}},
            },
        },
        "volumes": {volume: {} for volume in VOLUMES},
        "networks": {"cluster": {"external": True, "name": NETWORK}},
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("/tmp/acervo-node4-test.compose.json"))
    parser.add_argument("--reference-app", default="acervo-app-test-app-node-1-1")
    args = parser.parse_args()
    output = args.output.resolve()
    if output.is_relative_to(ROOT):
        parser.error("Credential artifact must be outside the repository")
    inspect("network", NETWORK)
    for volume in VOLUMES:
        if inspect("volume", f"{PROJECT}_{volume}", required=False):
            raise RuntimeError("Test volume already exists; preserve it and use the existing Compose artifact")
    app = inspect("container", args.reference_app)
    minio = inspect("container", "acervo-infra-runtime-minio-1-1")
    cockroach = inspect("container", "acervo-infra-runtime-cockroach-1-1")
    document = compose_document(app, minio, cockroach)
    available = inspect("image", APP_IMAGE, required=False) is not None
    descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(document, stream, indent=2)
        stream.write("\n")
    print(f"Compose generated: {output} (0600). No services started.")
    if not available:
        print(f"Required application image is not available yet: {APP_IMAGE}")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError) as error:
        print(f"Preparation failed: {error}", file=sys.stderr)
        sys.exit(1)
