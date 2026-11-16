#!/usr/bin/env python3
"""Send one live HLCP status request per second while E3 samplers run."""
import argparse
import csv
from datetime import datetime, timezone
from pathlib import Path
import sys
import time
import uuid

from measure_status import invoke, require, validate_result
import json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    command = ["hlcp", "-n", args.node, "-r", "hlcp-bench", "-a", "status", "--json"]
    preview = json.loads(require(command + ["--dry-run"]))
    if not (preview.get("read_only") is True and preview.get("resource") == "hlcp-bench"
            and preview.get("node") == args.node and preview.get("action") == "status"):
        parser.exit(1, "Expected a read-only status action for hlcp-bench.\n")
    first_id = uuid.uuid4().hex
    if not validate_result("hlcp", require(command + ["--command-id", first_id]), first_id):
        parser.exit(1, "Expected a running hlcp-bench container.\n")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("x", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(("slot", "utc", "scheduled_ns", "started_ns", "duration_ns",
                         "command_id", "returncode", "success", "error"))
        print("Load started. Start both service samplers now.", flush=True)
        origin = time.monotonic_ns()
        failures = 0
        for slot in range(160):
            scheduled = slot * 10**9
            remaining = origin + scheduled - time.monotonic_ns()
            if remaining > 0:
                time.sleep(remaining / 10**9)
            started = time.monotonic_ns() - origin
            utc = datetime.now(timezone.utc).isoformat()
            if started >= scheduled + 10**9:
                writer.writerow((slot, utc, scheduled, "", "", "", "", 0, "MISSED_SLOT"))
                failures += 1
            else:
                command_id = uuid.uuid4().hex
                duration, code, stdout, _, error = invoke(command + ["--command-id", command_id], 2)
                ok = not error and code == 0 and validate_result("hlcp", stdout, command_id)
                if not ok:
                    failures += 1
                    error = error or ("EXIT_NONZERO" if code else "UNEXPECTED_RESULT")
                writer.writerow((slot, utc, scheduled, started, duration, command_id, code, int(ok), error))
            stream.flush()
            if failures >= 5:
                parser.exit(1, "Five failed or missed slots. Preserve the partial run.\n")
    print(f"Recorded 160 slots with {failures} failed or missed in {args.out}")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, ValueError) as error:
        print(f"Load stopped: {error}", file=sys.stderr)
        raise SystemExit(1)
