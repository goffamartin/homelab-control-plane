#!/usr/bin/env python3
"""Measure restart completion for the dedicated hlcp-bench container."""
import argparse
import csv
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import random
import re
import sys
import tempfile
import time
import uuid

from measure_status import MODES, invoke, require


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", required=True)
    parser.add_argument("--ssh-target", required=True)
    parser.add_argument("--ssh-identity", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--pilot", action="store_true", help="Run one sample per mode")
    args = parser.parse_args()
    if sys.platform != "linux" or os.geteuid() == 0:
        parser.error("Run as the configured non-root user on the Linux controller.")
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9_.:-]*", args.ssh_target):
        parser.error("Use a literal USER@HOST/IP.")

    os.umask(0o077)
    cli = ["hlcp", "-n", args.node, "-r", "hlcp-bench", "-a", "restart", "--json"]
    preview = json.loads(require(cli + ["--dry-run"]))
    if (preview.get("node"), preview.get("resource"), preview.get("action")) != (args.node, "hlcp-bench", "restart"):
        parser.error("Unexpected HLCP restart preview.")
    ssh = ["ssh", "-F", "/dev/null", "-T", "-i", str(args.ssh_identity),
           "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
           "-o", "ConnectTimeout=5", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes"]
    fresh = ssh + ["-o", "ControlMaster=no", "-o", "ControlPath=none", args.ssh_target]
    restart = "podman restart hlcp-bench"
    inspect = """podman container inspect --format '{{.Id}}|{{index .Config.Labels "hlcp.test"}}|{{.State.Status}}|{{.State.StartedAt}}' hlcp-bench"""

    def observe():
        container, label, state, started = require(fresh + [inspect]).strip().split("|")
        if label != "semester-evaluation" or state != "running":
            raise RuntimeError("Expected the running container labeled hlcp.test=semester-evaluation.")
        return container, started

    container, _ = observe()
    args.out.mkdir(parents=True, exist_ok=False)
    rng = random.Random(20261116)
    blocks, samples = (1, 1) if args.pilot else (3, 10)
    metadata = {"phase": "pilot" if args.pilot else "final", "status": "incomplete", "blocks": blocks,
                "samples_per_mode_per_block": samples, "seed": 20261116,
                "stabilization_s": 2, "hlcp_version": require(["hlcp", "--version"]).strip()}
    metadata_path = args.out / "metadata.json"
    metadata_path.write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    with tempfile.TemporaryDirectory(prefix="hlcp-e2-") as temp:
        control = ssh + ["-S", str(Path(temp) / "master")]
        try:
            require(control + ["-M", "-N", "-f", "-o", "ControlPersist=600", args.ssh_target])
            mux = control + ["-o", "ControlMaster=no", "-o", "ProxyCommand=false", args.ssh_target]
            if require(mux + [inspect]).strip() != require(fresh + [inspect]).strip():
                raise RuntimeError("SSH paths disagree about the container state.")
            with (args.out / "samples.csv").open("x", newline="", encoding="utf-8") as stream:
                writer = csv.writer(stream)
                writer.writerow(("sequence", "utc", "phase", "block", "sample", "mode", "command_id",
                                 "duration_ns", "returncode", "success", "error", "started_before", "started_after"))
                sequence = 0
                for block in range(1, blocks + 1):
                    schedule = [(mode, sample) for mode in MODES for sample in range(1, samples + 1)]
                    rng.shuffle(schedule)
                    for mode, sample in schedule:
                        time.sleep(2)
                        before_id, before = observe()
                        if before_id != container:
                            raise RuntimeError("Container identity changed before restart.")
                        command_id = uuid.uuid4().hex if mode == "hlcp" else ""
                        command = cli + ["--command-id", command_id] if mode == "hlcp" else (fresh if mode == "ssh_fresh" else mux) + [restart]
                        utc = datetime.now(timezone.utc).isoformat()
                        duration, code, stdout, _, error = invoke(command, 30)
                        if not error and code == 0 and mode == "hlcp":
                            try:
                                result = json.loads(stdout)
                                if not (isinstance(result, dict) and result.get("success") is True
                                        and result.get("code") == "OK" and result.get("command_id") == command_id):
                                    error = "UNEXPECTED_RESULT"
                            except ValueError:
                                error = "INVALID_JSON"
                        after = ""
                        try:
                            after_id, after = observe()
                            if after_id != container or after == before:
                                error = error or "RESTART_NOT_VERIFIED"
                        except (RuntimeError, ValueError):
                            error = error or "OBSERVATION_FAILED"
                        error = error or ("EXIT_NONZERO" if code else "")
                        sequence += 1
                        writer.writerow((sequence, utc, "measure", block, sample, mode, command_id,
                                         duration, code, int(not error), error, before, after))
                        stream.flush()
                        if error:
                            raise RuntimeError(f"Sample {sequence}: {error}. Inspect the saved row before another restart.")
                        print(f"{sequence}/{blocks * samples * len(MODES)} {mode}: {duration / 1e6:.1f} ms", flush=True)
        finally:
            invoke(control + ["-O", "exit", args.ssh_target], 5)
    metadata["status"] = "complete"
    metadata_path.write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    print(f"Completed. Samples: {args.out / 'samples.csv'}")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, ValueError) as error:
        print(f"Stopped: {error}", file=sys.stderr)
        raise SystemExit(1)
