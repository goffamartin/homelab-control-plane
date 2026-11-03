#!/usr/bin/env python3
"""Describe E1 data without excluding failed attempts or interpreting warm-ups as results."""
import argparse
import csv
from collections import defaultdict
import math
from pathlib import Path
import statistics
import sys


def summarize(rows):
    values = sorted(int(row["duration_ns"]) / 1e6 for row in rows if row["success"] == "1")
    attempts = len(rows)
    return {"attempts": attempts, "successes": len(values), "failures": attempts - len(values),
            "timeouts": sum(row["error"] == "TIMEOUT" for row in rows),
            "success_rate": len(values) / attempts if attempts else None,
            "median_ms": statistics.median(values) if values else None,
            "p95_ms": values[math.ceil(0.95 * len(values)) - 1] if values else None,
            "min_ms": min(values) if values else None, "max_ms": max(values) if values else None}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("samples", type=Path, help="One run's samples.csv; do not mix pilots with final runs")
    args = p.parse_args()
    groups = defaultdict(list)
    sequences = set()
    with args.samples.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream):
            if row["sequence"] in sequences or int(row["duration_ns"]) < 0:
                p.error("Duplicate sequence or invalid duration.")
            sequences.add(row["sequence"])
            if row["success"] not in ("0", "1") or row["phase"] not in ("warmup", "measure"):
                p.error("Invalid phase/success value.")
            if row["phase"] == "measure":
                groups[(row["mode"], row["block"])].append(row)
                groups[(row["mode"], "all")].append(row)
    results = [{"mode": mode, "block": block, **summarize(rows)} for (mode, block), rows in sorted(groups.items())]
    if not results:
        p.error("No measured rows found.")
    writer = csv.DictWriter(sys.stdout, fieldnames=list(results[0]))
    writer.writeheader()
    writer.writerows(results)
    print("Latency describes successful samples only; retain failure counts and metadata status. p95 uses nearest rank.", file=sys.stderr)


if __name__ == "__main__":
    main()
