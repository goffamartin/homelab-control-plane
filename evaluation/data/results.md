# Experiment register

| Run | Scenario | Evidence | Status |
|---|---|---|---|
| E1 pilot | Live container status, 10 measured requests per mode | Operator screenshot. HLCP median 71.49 ms, fresh SSH 184.26 ms, multiplexed SSH 71.68 ms. All 30 succeeded. | Pilot completed |
| E1 final, 2026-11-16 08:37:22 UTC | Live container status, three blocks of 100 requests per mode | [Pooled summary transcribed from operator screenshot](e1-final-20261116T083722Z-summary.csv). 900 measured requests plus 30 warm-ups. | Summary available. Raw samples and metadata pending. |
| E3 idle | Three paired 120-second windows for controller and agent | Service cgroup CPU and memory series | Pending |
| E3 load | Three paired 120-second windows at one status request per second | Service cgroup series and per-request outcomes | Pending |
| E2 | Dedicated container restart | Runtime completion and independent running-state check | Pending |
| E4 | Disconnect and recovery | Timestamped events and fresh inventory | Pending |
| E5 | Access policy and duplicate handling | Expected and observed outcomes | Pending |

Keep the original CSV files for each run. Record the script revision, service
versions, run duration and any concurrent activity. Publish sanitized copies of
metadata alongside the samples. E1 aggregate values remain screenshot-derived
until they can be regenerated from the original samples.
