"""Regenerate the published figures from the final evaluation CSVs."""

import csv
from pathlib import Path
from shutil import copyfile
from statistics import median

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


EVALUATION = Path(__file__).resolve().parents[1]
FINAL = EVALUATION / "data" / "final-agent-720dcad"
RESOURCE_SUMMARY = EVALUATION / "data" / "e3-refresh-20261118T104656Z" / "summary.csv"
FIGURES = EVALUATION / "figures"
TEAL = "#087764"
BLUE = "#426484"


def read_csv(path):
    with path.open(encoding="utf-8", newline="") as source:
        return list(csv.DictReader(source))


def plot_bars(filename, previous_path, title, ylabel, categories, series, upper_limit):
    figure, axes = plt.subplots(figsize=(9, 4.8), layout="constrained")
    width = 0.7 / len(series)
    positions = range(len(categories))
    for index, (label, values, color) in enumerate(series):
        offset = (index - (len(series) - 1) / 2) * width
        bars = axes.bar([position + offset for position in positions], values, width,
                        label=label, color=color)
        axes.bar_label(bars, fmt="%.2f", padding=4, fontsize=10)

    axes.set_title(title, loc="left", fontsize=16, fontweight="bold", pad=14)
    axes.set_ylabel(ylabel)
    axes.set_xticks(list(positions), categories)
    axes.set_ylim(0, upper_limit)
    axes.spines[["top", "right"]].set_visible(False)
    axes.grid(axis="y", color="#D9E2DB")
    axes.set_axisbelow(True)
    if len(series) > 1:
        axes.legend(loc="upper left", frameon=False)

    path = FIGURES / filename
    figure.savefig(path, dpi=160)
    plt.close(figure)
    copyfile(path, FINAL / "figures" / previous_path)


def plot_latency(experiment, filename, previous_path, title, upper_limit):
    pooled = {
        row["mode"]: row
        for row in read_csv(FINAL / experiment / "summary.csv")
        if row["block"] == "all"
    }
    modes = ("hlcp", "ssh_fresh", "ssh_mux")
    plot_bars(
        filename, previous_path, title, "Time (ms)",
        ("HLCP", "New SSH connection", "Open SSH connection"),
        (
            ("Median", [float(pooled[mode]["median_ms"]) for mode in modes], TEAL),
            ("95th percentile", [float(pooled[mode]["p95_ms"]) for mode in modes], BLUE),
        ),
        upper_limit,
    )


def main():
    FIGURES.mkdir(exist_ok=True)
    (FINAL / "figures").mkdir(exist_ok=True)
    plot_latency("e1", "status-latency.png", "status.png", "Time to check container status", 225)
    plot_latency("e2", "restart-latency.png", "restart.png", "Time to restart the test container", 350)

    resource_runs = read_csv(RESOURCE_SUMMARY)
    roles = ("controller", "agent")
    plot_bars(
        "service-cpu.png", "cpu.png", "CPU use at idle and at one status check per second",
        "Share of one logical CPU (%)", ("Controller", "Agent"),
        [
            (label, [
                median(float(row["cpu_pct_one_core"]) for row in resource_runs
                       if row["mode"] == mode and row["role"] == role)
                for role in roles
            ], color)
            for mode, label, color in (("idle", "Idle", BLUE), ("load", "One status check/s", TEAL))
        ],
        8,
    )
    plot_bars(
        "service-memory.png", "memory.png", "Median memory use in each two-minute load run",
        "Service memory (MB)", ("Run 1", "Run 2", "Run 3"),
        [
            (role.capitalize(), [
                float(row["memory_median_mib"]) * 1.048576 for row in resource_runs
                if row["mode"] == "load" and row["role"] == role
            ], color)
            for role, color in zip(roles, (BLUE, TEAL), strict=True)
        ],
        19,
    )

    pauses = read_csv(FINAL / "silent" / "samples.csv")
    plot_bars(
        "silent-recovery.png", "recovery.png", "Reconnection after five successive agent pauses",
        "Time from resume to reconnect (s)",
        [f"Pause {row['trial']}" for row in pauses],
        [("Reconnect", [float(row["new_session_observed_ms"]) / 1000 for row in pauses], TEAL)],
        18,
    )


if __name__ == "__main__":
    main()
