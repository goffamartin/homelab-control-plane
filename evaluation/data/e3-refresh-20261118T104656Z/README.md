# Agent CPU after removing redundant inventory refreshes

In v1.1.0, a native status request inspected the target and also scheduled an observation round for all resources. The revised agent reads the target directly and retains periodic observations and refreshes after potentially mutating actions. The controller continued to run v1.1.0. The agent ran build `v1.1.0-status-refresh.1`, revision `720dcad18f0105f7fe65d33a1f55d4281a492a3c`, Go 1.27.3, linux/amd64. Executable SHA-256: `c24b74835a6ca3da1c0e803b9fbaa113e810759a592dee9914db9aa068d38883`.

The agent service was restarted to deploy the build. The benchmark container was started before testing and returned to its original stopped state afterwards. The controller observed both states through the live status action.

| Metric | v1.1.0 on 16 November | Revised agent on 18 November |
|---|---:|---:|
| Agent CPU at idle, range of three run averages (% of one logical CPU) | 2.94–2.98 | 3.00–3.03 |
| Agent CPU at one live status/s, three run averages (%) | 36.96, 36.93, 37.07 | 6.90, 7.03, 7.11 |
| Agent cgroup memory under load, three run medians (MiB) | 21.50, 22.16, 54.99 | 11.91, 12.83, 12.88 |
| Highest sampled agent cgroup memory under load (MiB) | 66.95 | 13.42 |
| Successful scheduled requests | 480/480 | 480/480 |

The median run-average CPU fell by 29.93 percentage points, approximately 81% relative to baseline. Both measurements used the same VM pair and benchmark target on different days. The original series lacks a memory breakdown, and the new agent had just restarted. The earlier memory spike did not recur during these windows. Its cause and long-term memory behavior remain open.

## Sampling and checks

Three idle pairs preceded three load pairs. In each load run, the controller scheduled 160 requests at one per second. Each service sampler recorded 121 observations spanning 120 seconds, with 120 successful request starts inside each load window by recorded UTC. For CPU, divide cumulative cgroup CPU time consumed by elapsed time. For memory, the series includes cgroup usage, anonymous, file and kernel memory, plus the main process RSS. RSS and cgroup memory use different accounting and must not be added.

The local private archive retains the original files. All 15 downloaded CSVs passed verification against hashes computed on the controller. The only column removed from the shareable request files is the command ID. The summaries computed from both copies match. The checksums beside this document cover the shareable CSVs.

From the evaluation checkout, reproduce the [summary](summary.csv) with:

```sh
python3 evaluation/scripts/summarize_resource_pairs.py evaluation/data/e3-refresh-20261118T104656Z
```

Original measurements: [idle](../e3-idle-summary.csv) and [load](../e3-load-summary.csv).
