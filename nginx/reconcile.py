"""Authoritative routing snapshots; stdlib only, supervised Nginx child."""
import concurrent.futures
import ipaddress
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.parse


class InvalidSnapshot(ValueError):
    pass


def endpoint(raw):
    if not isinstance(raw, str) or any(c.isspace() for c in raw):
        raise InvalidSnapshot("invalid endpoint")
    u = urllib.parse.urlsplit(raw)
    if u.scheme not in ("http", "https") or not u.hostname or u.username is not None or u.password is not None or u.path not in ("", "/") or u.query or u.fragment:
        raise InvalidSnapshot("invalid endpoint")
    host = u.hostname
    try:
        address = ipaddress.ip_address(host)
        host = "[" + str(address) + "]" if address.version == 6 else str(address)
    except ValueError:
        if not re.fullmatch(r"[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?", host):
            raise InvalidSnapshot("invalid host")
    try:
        port = u.port or (443 if u.scheme == "https" else 80)
    except ValueError as error:
        raise InvalidSnapshot("invalid port") from error
    if not 1 <= port <= 65535 or u.netloc.endswith(":"):
        raise InvalidSnapshot("invalid port")
    return u.scheme, f"{host}:{port}"


def snapshot(data):
    config = data["configuration"]
    version = config["Version"]
    ttl = data["valid_for_ms"]
    if type(version) is not int or version < 0 or type(ttl) is not int or ttl <= 0:
        raise InvalidSnapshot("invalid authority")
    members = config["Members"]
    if not isinstance(members, list):
        raise InvalidSnapshot("invalid members")
    backends = []
    identities = set()
    for member in members:
        if member["State"] != "ready" or not isinstance(member["ID"], str) or not member["ID"] or member["ID"] in identities:
            raise InvalidSnapshot("invalid member")
        identities.add(member["ID"])
        backends.append(endpoint(member["BackendEndpoint"]))
    if len(set(backends)) != len(backends):
        raise InvalidSnapshot("incompatible or repeated backends")
    return version, ttl / 1000, tuple(sorted(backends))


def render(template, resolver, backends):
    address = ipaddress.ip_address(resolver)
    resolver = f"[{address}]" if address.version == 6 else str(address)
    upstream, proxy = "", "return 503;"
    if backends:
        choices, tls_names = [], []
        weight = math.floor(10_000 / len(backends)) / 100
        for index, (scheme, host) in enumerate(backends):
            origin = f"{scheme}://{host}"
            percent = "*" if index == len(backends) - 1 else f"{weight:.2f}%"
            choices.append(f'{percent} "{origin}";')
            tls_name = urllib.parse.urlsplit(origin).hostname
            tls_names.append(f'"{origin}" "{tls_name}";')
        upstream = 'split_clients "$request_id" $backend_origin {\n' + "\n".join(choices) + "\n}\n"
        upstream += 'map $backend_origin $backend_tls_name { default "";\n' + "\n".join(tls_names) + "\n}"
        proxy = """proxy_pass $backend_origin;
            proxy_ssl_server_name on;
            proxy_ssl_name $backend_tls_name;
            proxy_ssl_verify on;
            proxy_ssl_trusted_certificate /etc/ssl/cert.pem;
            proxy_set_header Host $http_host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_request_buffering off;
            proxy_buffering off;
            proxy_connect_timeout 2s;
            proxy_read_timeout 300s;
            proxy_send_timeout 300s;
            proxy_next_upstream error timeout http_502 http_503 http_504;
            proxy_next_upstream_timeout 5s;"""
    return template.replace("@@RESOLVER@@", resolver).replace("@@UPSTREAM@@", upstream).replace("@@PROXY@@", proxy)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Reconciler:
    def __init__(self, endpoints, token, timeout, max_stale, fetch=None, clock=time.monotonic):
        if not endpoints or not token or not all(math.isfinite(v) and v > 0 for v in (timeout, max_stale)):
            raise ValueError("invalid control configuration")
        for raw in endpoints:
            endpoint(raw)
        self.endpoints, self.token = endpoints, token
        self.timeout, self.max_stale = timeout, max_stale
        self.clock, self.fetch = clock, fetch
        self.executor = concurrent.futures.ThreadPoolExecutor(max_workers=len(endpoints)) if fetch is None else None
        self.pending = {}
        self.version, self.backends, self.deadline = -1, (), 0
        self.revoked = True
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def _fetch(self, raw):
        request = urllib.request.Request(raw.rstrip("/") + "/internal/cluster", headers={"Authorization": "Bearer " + self.token, "Cache-Control": "no-cache"})
        timeout = self.timeout
        if self.backends and not self.revoked:
            timeout = min(timeout, max(0.001, self.deadline - self.clock()))
        with self.opener.open(request, timeout=timeout) as response:
            body = response.read(1024 * 1024 + 1)
            if len(body) > 1024 * 1024:
                raise InvalidSnapshot("oversized snapshot")
            return json.loads(body)

    def poll(self):
        for raw in self.endpoints:
            started = self.clock()
            if self.backends and not self.revoked and started >= self.deadline:
                self.revoked = True
                return ()
            try:
                if self.fetch is not None:
                    data = self.fetch(raw)
                else:
                    # Socket timeouts do not bound DNS resolution. Keep one
                    # in-flight request per endpoint and bound the manager wait.
                    if raw not in self.pending:
                        self.pending[raw] = (started, self.executor.submit(self._fetch, raw))
                    started, future = self.pending[raw]
                    budget = self.timeout
                    if self.backends and not self.revoked:
                        budget = min(budget, max(0.001, self.deadline - self.clock()))
                    try:
                        data = future.result(timeout=budget)
                    finally:
                        if future.done():
                            del self.pending[raw]
                version, ttl, backends = snapshot(data)
                expires = started + min(ttl, self.max_stale)
                if expires <= self.clock() or version < self.version or (version == self.version and backends != self.backends):
                    continue
                self.version, self.backends, self.deadline = version, backends, expires
                self.revoked = False
                self.endpoints = [raw] + [item for item in self.endpoints if item != raw]
                return backends
            except (OSError, ValueError, KeyError, TypeError):
                continue
        if self.clock() < self.deadline:
            return self.backends
        self.revoked = True
        return ()


def install(path, text, reload, runner=subprocess.run):
    """Validate candidate before replacing; restore disk state on signal failure."""
    path = Path(path)
    previous = path.read_text() if path.exists() else None
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as candidate:
        candidate.write(text)
        temporary = candidate.name
    try:
        validation = runner(["nginx", "-t", "-c", temporary], capture_output=True)
        if validation.returncode:
            print("Nginx rejected candidate configuration", file=sys.stderr, flush=True)
            return False
        os.replace(temporary, path)
        if reload and runner(["nginx", "-s", "reload"], capture_output=True).returncode:
            print("Nginx reload failed; restoring configuration", file=sys.stderr, flush=True)
            if previous is not None:
                with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as restore:
                    restore.write(previous)
                os.replace(restore.name, path)
            return False
        return True
    finally:
        Path(temporary).unlink(missing_ok=True)


def main():
    reconciler = Reconciler(os.environ.get("CONTROL_ENDPOINTS", "").split(), os.environ.get("CONTROL_TOKEN", ""), float(os.environ.get("CONTROL_TIMEOUT", "1")), float(os.environ.get("CONTROL_MAX_STALE", "3")))
    interval = float(os.environ.get("CONTROL_INTERVAL", "0.5"))
    if not math.isfinite(interval) or interval <= 0:
        raise ValueError("invalid control interval")
    resolver = next(line.split()[1] for line in Path("/etc/resolv.conf").read_text().splitlines() if line.startswith("nameserver "))
    template = Path("/etc/nginx/templates/nginx.conf.template").read_text()
    path = "/etc/nginx/nginx.conf"
    closed = render(template, resolver, ())
    active = closed
    active_deadline = 0
    if not install(path, active, False):
        raise RuntimeError("Nginx rejected initial configuration")
    nginx = subprocess.Popen(["nginx", "-g", "daemon off;"])
    stopped = False
    def stop(*_):
        nonlocal stopped
        stopped = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        while not stopped and nginx.poll() is None:
            backends = reconciler.poll()
            wanted = render(template, resolver, backends)
            if wanted != active:
                if install(path, wanted, True):
                    active = wanted
                    active_deadline = reconciler.deadline
                elif not backends:
                    raise RuntimeError("cannot revoke expired routing")
                elif active != closed and time.monotonic() >= active_deadline:
                    if not install(path, closed, True):
                        raise RuntimeError("cannot revoke previous routing after reload failure")
                    active = closed
            else:
                active_deadline = reconciler.deadline
            remaining = min(reconciler.deadline, active_deadline) - time.monotonic()
            time.sleep(min(interval, max(0.01, remaining)) if backends else interval)
    finally:
        if nginx.poll() is None:
            nginx.send_signal(signal.SIGQUIT)
            try:
                nginx.wait(timeout=6)
            except subprocess.TimeoutExpired:
                nginx.kill()
                nginx.wait()


if __name__ == "__main__":
    main()
