"""Summarize the paired service CSVs produced by the status-refresh experiment."""
import argparse
import csv
from datetime import datetime
from pathlib import Path
from statistics import median
import sys


def read(path):
    with path.open(newline="", encoding="utf-8") as stream:
        return list(csv.DictReader(stream))


def summarize(directory):
    for mode in ("idle", "load"):
        for pair in ("01", "02", "03"):
            prefix = f"{mode}-{pair}"
            requests = read(directory / f"{prefix}-requests.csv") if mode == "load" else []
            if mode == "load" and [int(r["slot"]) for r in requests] != list(range(160)):
                raise ValueError(f"Incomplete workload in {prefix}")
            successful = [r for r in requests if r["success"] == "1" and r["returncode"] == "0"]
            for role in ("controller", "agent"):
                rows = read(directory / f"{prefix}-{role}.csv")
                if [int(r["sample"]) for r in rows] != list(range(121)) or any(r["role"] != role for r in rows):
                    raise ValueError(f"Incomplete or mismatched service series: {prefix}-{role}")
                first, last = rows[0], rows[-1]
                seconds = (int(last["elapsed_ns"]) - int(first["elapsed_ns"])) / 1e9
                cpu_seconds = (int(last["cpu_usage_usec"]) - int(first["cpu_usage_usec"])) / 1e6
                start, end = (datetime.fromisoformat(r["utc"]) for r in (first, last))
                in_window = sum(start <= datetime.fromisoformat(r["utc"]) < end for r in successful)
                result = dict(mode=mode, pair=pair, role=role, seconds=round(seconds, 4),
                              cpu_pct_one_core=round(100 * cpu_seconds / seconds, 4))
                for key in ("memory_bytes", "main_rss_bytes", "anon_bytes", "file_bytes", "kernel_bytes"):
                    values = [int(r[key]) / 2**20 for r in rows if r[key]]
                    result[key.replace("_bytes", "_median_mib")] = round(median(values), 4) if values else ""
                result.update(memory_max_mib=round(max(int(r["memory_bytes"]) for r in rows) / 2**20, 4),
                              requests_attempted=len(requests), requests_ok=len(successful),
                              successful_starts_in_window=in_window,
                              request_median_ms=round(median(int(r["duration_ns"]) for r in successful) / 1e6, 3)
                              if successful else "")
                yield result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    results = list(summarize(parser.parse_args().directory))
    writer = csv.DictWriter(sys.stdout, fieldnames=results[0], lineterminator="\n")
    writer.writeheader()
    writer.writerows(results)
