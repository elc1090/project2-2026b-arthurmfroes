#!/usr/bin/env python3
"""Read-only Linux memory sampler for one dedicated Chromium process tree.

Run only after opening a named playwright-cli session. Supply its Chromium browser
PID, not the CLI daemon PID. No processes, cgroups or containers are modified.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def process_info(pid):
    try:
        fields = Path(f"/proc/{pid}/stat").read_text().rpartition(")")[2].split()
        return {"pid": pid, "ppid": int(fields[1]), "start_ticks": fields[19]}
    except (OSError, IndexError, ValueError):
        return None


def descendants(root, root_start):
    processes = {}
    for path in Path("/proc").iterdir():
        if path.name.isdigit():
            info = process_info(int(path.name))
            if info:
                processes[info["pid"]] = info
    if root not in processes or processes[root]["start_ticks"] != root_start:
        return []
    found = {root}
    while True:
        additions = {pid for pid, proc in processes.items() if proc["ppid"] in found} - found
        if not additions:
            break
        found.update(additions)
    return sorted(found)


def process_memory(pid):
    try:
        command = Path(f"/proc/{pid}/cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace")
        memory = {}
        for line in Path(f"/proc/{pid}/smaps_rollup").read_text().splitlines():
            key, _, value = line.partition(":")
            if key in ("Rss", "Pss"):
                memory[key.lower() + "_bytes"] = int(value.split()[0]) * 1024
        kind = "browser"
        for arg in command.split():
            if arg.startswith("--type="):
                kind = arg.split("=", 1)[1]
        return {"pid": pid, "kind": kind, **memory}
    except (OSError, ValueError, IndexError) as error:
        return {"pid": pid, "unavailable": type(error).__name__}


def container_cgroup(name):
    pid = int(subprocess.check_output(["docker", "inspect", "--format", "{{.State.Pid}}", name], text=True).strip())
    if pid <= 0:
        raise ValueError(f"Container {name} is not running")
    groups = Path(f"/proc/{pid}/cgroup").read_text().splitlines()
    relative = next(line.split(":", 2)[2] for line in groups if line.startswith("0::"))
    root = Path("/sys/fs/cgroup") / relative.lstrip("/")
    if not (root / "memory.current").is_file():
        raise ValueError(f"Container {name}: cgroup v2 memory.current unavailable at {root}")
    return root


def cgroup_memory(root):
    result = {}
    for filename in ("memory.current", "memory.peak"):
        try:
            result[filename.replace(".", "_") + "_bytes"] = int((root / filename).read_text())
        except (OSError, ValueError):
            result[filename.replace(".", "_") + "_bytes"] = None
    return result


def numeric_max(left, right):
    values = [value for value in (left, right) if value is not None]
    return max(values) if values else None


def summarize(samples, metadata):
    phases = {}
    for sample in samples:
        phase = phases.setdefault(sample["phase"], {"samples": 0, "browser_rss_sampled_max_bytes": None, "browser_pss_sampled_max_bytes": None, "containers": {}})
        phase["samples"] += 1
        for metric in ("rss", "pss"):
            name = f"browser_{metric}_sampled_max_bytes"
            phase[name] = numeric_max(phase[name], sample["browser"].get(f"{metric}_bytes"))
        for name, current in sample["containers"].items():
            target = phase["containers"].setdefault(name, {"memory_current_sampled_max_bytes": None, "memory_peak_cgroup_lifetime_max_bytes": None})
            target["memory_current_sampled_max_bytes"] = numeric_max(target["memory_current_sampled_max_bytes"], current.get("memory_current_bytes"))
            target["memory_peak_cgroup_lifetime_max_bytes"] = numeric_max(target["memory_peak_cgroup_lifetime_max_bytes"], current.get("memory_peak_bytes"))
    return {**metadata, "baseline": samples[0] if samples else None, "samples": len(samples), "phases": phases}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser-pid", type=int, required=True)
    parser.add_argument("--container", action="append", default=[])
    parser.add_argument("--phase-file", type=Path, required=True)
    parser.add_argument("--stop-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--interval", type=float, default=0.5)
    parser.add_argument("--duration", type=float, default=1800)
    args = parser.parse_args()
    if args.interval <= 0 or args.duration <= 0:
        parser.error("interval and duration must be positive")
    if args.stop_file.exists():
        parser.error("stop file already exists; use a fresh run path")
    info = process_info(args.browser_pid)
    if not info:
        parser.error("browser PID is not running")
    command = Path(f"/proc/{args.browser_pid}/cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace")
    if not any(name in command.lower() for name in ("chrome", "chromium")) or "--type=" in command:
        parser.error("PID must identify the Chromium browser root, not a renderer or CLI daemon")
    containers = {name: container_cgroup(name) for name in args.container}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    samples = []
    stopped = False
    def stop(_signum, _frame):
        nonlocal stopped
        stopped = True
    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)
    started = time.monotonic()
    metadata = {"browser_pid": args.browser_pid, "browser_start_ticks": info["start_ticks"], "interval_seconds": args.interval, "started_unix": time.time(), "cgroups": {key: str(value) for key, value in containers.items()}, "measurement": "RSS/PSS sampled per process; cgroup current sampled; cgroup peak includes its prior lifetime. No peak counter reset."}
    with args.output.open("x") as stream:
        while not stopped and not args.stop_file.exists() and time.monotonic() - started < args.duration:
            tick = time.monotonic()
            try:
                phase = args.phase_file.read_text().strip() or "unspecified"
            except OSError:
                phase = "unspecified"
            processes = [process_memory(pid) for pid in descendants(args.browser_pid, info["start_ticks"])]
            observed = [proc for proc in processes if "rss_bytes" in proc and "pss_bytes" in proc]
            sample = {"unix": time.time(), "elapsed_seconds": tick - started, "phase": phase, "browser": {"rss_bytes": sum(p["rss_bytes"] for p in observed) if observed else None, "pss_bytes": sum(p["pss_bytes"] for p in observed) if observed else None, "processes": processes, "incomplete": len(observed) != len(processes) or not processes}, "containers": {name: cgroup_memory(root) for name, root in containers.items()}}
            samples.append(sample)
            stream.write(json.dumps(sample) + "\n")
            stream.flush()
            if not processes:
                break
            time.sleep(max(0, args.interval - (time.monotonic() - tick)))
    summary = summarize(samples, metadata)
    path = args.output.with_suffix(".summary.json")
    path.write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps({"samples": len(samples), "summary": str(path)}))


if __name__ == "__main__":
    main()
