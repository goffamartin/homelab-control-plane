# Final evaluation figures

| Figure | What it shows |
|---|---|
| [Status latency](status-latency.png) | Median and 95th percentile over 300 measured checks per method. |
| [Restart latency](restart-latency.png) | Median and 95th percentile over 30 measured restarts per method. |
| [Service CPU](service-cpu.png) | Percentage of one logical CPU. Each bar is the median of three run averages. |
| [Service memory](service-memory.png) | Median charged memory in each two-minute load run, expressed in decimal MB. |
| [Reconnect time](silent-recovery.png) | Seconds from resuming the agent to a new connection. The five pauses ran in sequence, so the agent deliberately waited longer before reconnecting. |

Run the plotting script from the repository root in a Python environment with Matplotlib installed.

```sh
python3 evaluation/scripts/plot_final_results.py
```

The script reads the [final CSVs](../data/final-agent-720dcad/README.md) and writes identical copies at the older dataset figure paths so existing links still resolve.
