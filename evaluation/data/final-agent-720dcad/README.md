# Final evaluation dataset

We used the same controller and agent builds across all five experiments. The table links to the measurements behind the report and presentation.

| Experiment | Evidence |
|---|---|
| E1. Live status | [Samples including warm-ups](e1/samples.csv) and [per-block and pooled statistics](e1/summary.csv) |
| E2. Restart | [Timing and independently observed container start times](e2/samples.csv) and [summary](e2/summary.csv) |
| E3. Service CPU and memory | [Twelve service windows](../e3-refresh-20261118T104656Z/summary.csv), [checksums of individual CSV files](../e3-refresh-20261118T104656Z/SHA256SUMS) |
| E4. Recovery | Five [orderly agent relaunches](clean/samples.csv) and five [successive silent interruptions](silent/samples.csv) |
| E5. Access policy and retained results | Independent disposable instances [1](policy/policy.json), [2](policy-02/policy.json) and [3](policy-03/policy.json) |

E1 and E2 used the deployed controller and agent on separate VMs. A dedicated rootless Podman container served as the only mutation target. Warm-up rows are tagged separately. E4 and E5 ran over loopback on the agent VM with temporary credentials and private state. The additional policy checks performed by the silent-recovery harness are outside the three E5 series.

In the repeated-pause test, the next pause followed the first successful status response. The agent increases its retry delay after short sessions and resets it after a session lasts at least one minute. The sample file records offline detection from the start of the pause and both recovery measurements from the moment the agent resumes.

The resource summary uses MiB. The reports and presentation convert memory to decimal MB using 1 MiB = 1.048576 MB.

The private archive preserves original data and command IDs. The shareable latency CSVs omit the command ID column. All recorded durations, outcomes and restart observations are preserved. [Checksums](SHA256SUMS) identify the shareable E1, E2, E4 and E5 files. E3 has a separate manifest.

From the repository root, reproduce the reported summaries with:

```sh
python3 evaluation/scripts/analyze_status.py evaluation/data/final-agent-720dcad/e1/samples.csv
python3 evaluation/scripts/analyze_status.py evaluation/data/final-agent-720dcad/e2/samples.csv
python3 evaluation/scripts/summarize_resource_pairs.py evaluation/data/e3-refresh-20261118T104656Z
```

<details>
<summary>Build details for reproducing the tests</summary>

All five experiments characterize the same build pair on 18 November 2026. The controller and CLI used v1.1.0, revision `0f119514b1739652d709aee7b07db742d44dc0a0`. The agent used revision `720dcad18f0105f7fe65d33a1f55d4281a492a3c`. Both were built with Go 1.27.3 for Linux amd64. The agent executable had SHA-256 `c24b74835a6ca3da1c0e803b9fbaa113e810759a592dee9914db9aa068d38883`.

</details>
