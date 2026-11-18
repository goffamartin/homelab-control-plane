#!/usr/bin/env python3
"""Sample an HLCP service's cgroup every second for a 120-second window."""
import argparse
import csv
from datetime import datetime, timezone
from pathlib import Path
import subprocess
import time


def service_properties(role):
    command = ["systemctl"]
    if role == "agent":
        command.append("--user")
    command += ["show", f"hlcp-{role}.service", "-p", "ActiveState",
                "-p", "MainPID", "-p", "ControlGroup", "-p", "InvocationID"]
    output = subprocess.check_output(command, text=True, timeout=10)
    return dict(line.split("=", 1) for line in output.splitlines() if "=" in line)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("role", choices=("controller", "agent"))
    parser.add_argument("output", type=Path)
    parser.add_argument("--memory-breakdown", action="store_true",
                        help="Include cgroup anon/file/kernel bytes and the main service process RSS")
    args = parser.parse_args()

    service = service_properties(args.role)
    group = service.get("ControlGroup", "")
    if service.get("ActiveState") != "active" or not service.get("InvocationID") or not group.startswith("/") or group == "/":
        parser.exit(1, "Expected an active service with a dedicated cgroup.\n")
    cgroup = Path("/sys/fs/cgroup") / group.lstrip("/")
    cpu_stat = cgroup / "cpu.stat"
    memory_current = cgroup / "memory.current"
    inode = cgroup.stat().st_ino
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        fields = ["role", "sample", "utc", "elapsed_ns", "cpu_usage_usec", "memory_bytes"]
        if args.memory_breakdown:
            fields.extend(("anon_bytes", "file_bytes", "kernel_bytes", "main_rss_bytes"))
        writer.writerow(fields)
        start = time.monotonic_ns()
        for index in range(121):
            delay = (start + index * 10**9 - time.monotonic_ns()) / 10**9
            if delay > 0:
                time.sleep(delay)
            if cgroup.stat().st_ino != inode:
                raise RuntimeError("Service cgroup changed during sampling")
            cpu = dict(line.split() for line in cpu_stat.read_text().splitlines())
            memory = int(memory_current.read_text())
            row = [args.role, index, datetime.now(timezone.utc).isoformat(),
                   time.monotonic_ns() - start, int(cpu["usage_usec"]), memory]
            if args.memory_breakdown:
                stats = dict(line.split() for line in (cgroup / "memory.stat").read_text().splitlines())
                status = (Path("/proc") / service["MainPID"] / "status").read_text().splitlines()
                rss_kib = next(int(line.split()[1]) for line in status if line.startswith("VmRSS:"))
                row.extend((stats["anon"], stats["file"], stats.get("kernel", ""), rss_kib * 1024))
            writer.writerow(row)
            stream.flush()
    if service_properties(args.role) != service:
        parser.exit(1, "Service changed during sampling. Keep this CSV as an invalid trial.\n")
    print(f"Recorded 121 samples in {args.output}")


if __name__ == "__main__":
    main()
