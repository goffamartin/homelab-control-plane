#!/usr/bin/env python3
"""E1: live read-only status latency. Run on the controller, never as root.

No container mutations, credential creation, retries or network fault injection.
Requires an existing running hlcp-bench container, matching YAML and verified SSH keys.
"""
import argparse
import csv
import datetime as dt
import json
import os
from pathlib import Path
import random
import re
import shlex
import subprocess
import sys
import tempfile
import time
import uuid

MODES = ("hlcp", "ssh_fresh", "ssh_mux")
FIELDS = ("sequence", "utc", "phase", "block", "sample", "mode", "command_id",
          "duration_ns", "returncode", "success", "error", "stdout_bytes", "stderr_bytes")


def validate_result(mode, output, command_id):
    if mode != "hlcp":
        return output.strip() == "running"
    try:
        result = json.loads(output)
        return (isinstance(result, dict) and result.get("command_id") == command_id
                and result.get("success") is True and result.get("code") == "OK"
                and result.get("message") == "running")
    except (ValueError, TypeError):
        return False


def invoke(argv, timeout):
    started = time.perf_counter_ns()
    try:
        result = subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout)
        duration = time.perf_counter_ns() - started
        return duration, result.returncode, result.stdout, result.stderr, ""
    except subprocess.TimeoutExpired as error:
        return time.perf_counter_ns() - started, -1, error.stdout or b"", error.stderr or b"", "TIMEOUT"
    except OSError:
        return time.perf_counter_ns() - started, -1, b"", b"", "OS_ERROR"


def require(argv, timeout=15):
    _, code, stdout, _, error = invoke(argv, timeout)
    if code != 0 or error:
        raise RuntimeError("Preflight/control command failed; check SSH/profile manually. No credentials/output logged.")
    return stdout.decode("utf-8", errors="strict")


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--node", required=True)
    p.add_argument("--ssh-target", required=True, help="Explicit USER@HOST/IP of the agent; no SSH alias config")
    p.add_argument("--ssh-identity", type=Path)
    p.add_argument("--resource", default="hlcp-bench")
    p.add_argument("--container", default="hlcp-bench")
    p.add_argument("--hlcp-config", type=Path, help="Existing CLI profile path; never a token value")
    p.add_argument("--out", type=Path, required=True, help="New run directory; existing paths are refused")
    p.add_argument("--blocks", type=int, default=1)
    p.add_argument("--samples", type=int, default=10, help="Samples per mode per block")
    p.add_argument("--warmups", type=int, default=2, help="Logged but excluded from summaries")
    p.add_argument("--timeout", type=float, default=30)
    p.add_argument("--pause", type=float, default=0.1)
    p.add_argument("--seed", type=int, default=20261103)
    return p


def main(argv=None):
    p = parser()
    args = p.parse_args(argv)
    if sys.platform != "linux":
        p.error("Run the real experiment on the Linux controller VM.")
    if os.geteuid() == 0:
        p.error("Use the configured non-root CLI user.")
    if args.resource != "hlcp-bench" or args.container != "hlcp-bench":
        p.error("This initial protocol is restricted to the dedicated hlcp-bench resource/container.")
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9_.:-]*", args.ssh_target):
        p.error("Use a literal USER@HOST/IP, without shell syntax or SSH options.")
    if not (1 <= args.blocks <= 10 and 1 <= args.samples <= 1000 and 0 <= args.warmups <= 100
            and 1 <= args.timeout <= 120 and 0 <= args.pause <= 10):
        p.error("Invalid experiment limits.")
    if (args.blocks * args.samples + args.warmups) > 2000:
        p.error("Too many HLCP operations for one run; review journal capacity first.")
    os.umask(0o077)
    args.out.mkdir(parents=True, exist_ok=False)
    hlcp = ["hlcp"] + (["--config", str(args.hlcp_config)] if args.hlcp_config else [])
    status = ["--node", args.node, "--resource", args.resource, "--action", "status", "--json"]
    preview = json.loads(require(hlcp + status + ["--dry-run"]))
    if not (isinstance(preview, dict) and preview.get("read_only") is True
            and preview.get("node") == args.node and preview.get("resource") == args.resource
            and preview.get("action") == "status" and preview.get("known_operation") is False):
        raise RuntimeError("The controller did not confirm a fresh read-only status action.")
    detail = json.loads(require(hlcp + ["-n", args.node, "-r", args.resource, "--json"]))
    if not isinstance(detail, dict) or detail.get("type") != "container":
        raise RuntimeError("The dedicated resource must be a container. Verify its YAML target manually.")

    ssh = ["ssh", "-F", "/dev/null", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
           "-o", "ConnectTimeout=5", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
           "-o", "ClearAllForwardings=yes", "-o", "Compression=no", "-o", "LogLevel=ERROR"]
    if args.ssh_identity:
        ssh += ["-i", str(args.ssh_identity), "-o", "IdentitiesOnly=yes"]
    remote = shlex.join(["podman", "container", "inspect", "--format", "{{.State.Status}}", args.container])
    fresh = ssh + ["-o", "ControlMaster=no", "-o", "ControlPath=none", args.ssh_target, remote]
    if require(fresh).strip() != "running":
        raise RuntimeError("The SSH baseline did not observe a running test container.")
    metadata = {"protocol_version": 1, "status": "incomplete", "arguments": vars(args).copy(),
                "host": os.uname().nodename, "python": sys.version,
                "hlcp_version": require(["hlcp", "--version"]).strip(), "preview": preview,
                "clock": "perf_counter_ns", "metric": "CLI launch to validated terminal result",
                "ssh_common_argv": ssh, "ssh_remote_command": remote,
                "note": "Verify and record same user, YAML target and pinned container image separately."}
    meta_path = args.out / "metadata.json"

    def save_meta():
        meta_path.write_text(json.dumps(metadata, indent=2, default=str) + "\n", encoding="utf-8")

    save_meta()
    rng = random.Random(args.seed)
    with tempfile.TemporaryDirectory(prefix="hlcp-e1-") as temp:
        socket = str(Path(temp) / "master")
        control = ssh + ["-S", socket]
        try:
            require(control + ["-M", "-N", "-f", "-o", "ControlPersist=600", args.ssh_target])
            check_master = control + ["-O", "check", args.ssh_target]
            mux = control + ["-o", "ControlMaster=no", "-o", "ProxyCommand=false", args.ssh_target, remote]
            if require(mux).strip() != "running":
                raise RuntimeError("The multiplexed SSH baseline did not observe the running container.")
            sequence = 0
            failures = 0
            with (args.out / "samples.csv").open("x", newline="", encoding="utf-8") as stream:
                writer = csv.DictWriter(stream, fieldnames=FIELDS)
                writer.writeheader()
                for block in range(args.blocks + 1):
                    phase = "warmup" if block == 0 else "measure"
                    count = args.warmups if block == 0 else args.samples
                    schedule = [(mode, sample) for sample in range(1, count + 1) for mode in MODES]
                    rng.shuffle(schedule)
                    require(check_master)
                    for mode, sample in schedule:
                        command_id = uuid.uuid4().hex if mode == "hlcp" else ""
                        command = hlcp + status + ["--command-id", command_id] if mode == "hlcp" else fresh if mode == "ssh_fresh" else mux
                        utc = dt.datetime.now(dt.timezone.utc).isoformat()
                        duration, code, stdout, stderr, error = invoke(command, args.timeout)
                        valid = not error and code == 0 and validate_result(mode, stdout.decode("utf-8", errors="replace"), command_id)
                        if not valid and not error:
                            error = "EXIT_NONZERO" if code else "UNEXPECTED_RESULT"
                        sequence += 1
                        writer.writerow(dict(zip(FIELDS, (sequence, utc, phase, block, sample, mode, command_id,
                            duration, code, int(valid), error, len(stdout), len(stderr)))))
                        stream.flush()
                        if not valid:
                            failures += 1
                            print(f"Recorded failed sample {sequence}: {mode}/{error}", file=sys.stderr)
                        if failures >= 5:
                            raise RuntimeError("Five failures recorded; run stopped without retrying requests. Inspect manually.")
                        time.sleep(args.pause)
                    require(check_master)
            metadata["status"] = "complete"
        finally:
            # Close only our private master; ignore absence if its creation failed.
            invoke(control + ["-O", "exit", args.ssh_target], 5)
            save_meta()
    print(f"Recorded {sequence} rows in {args.out}. These are pilot results unless final conditions were met.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, ValueError) as error:
        print(f"Stopped: {error}", file=sys.stderr)
        raise SystemExit(1)
