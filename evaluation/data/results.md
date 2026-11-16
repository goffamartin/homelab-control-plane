# Experiment register

| Run | Scenario | Evidence | Status |
|---|---|---|---|
| E1 pilot | Live container status, 10 measured requests per mode | Operator screenshot. HLCP median 71.49 ms, fresh SSH 184.26 ms, multiplexed SSH 71.68 ms. All 30 succeeded. | Pilot completed |
| E1 final, 2026-11-16 08:37:22 UTC | Live container status, three blocks of 100 requests per mode | [Validated samples and summary](e1-final-20261116T083722Z/). All 12 summary rows reproduced. 900 measured requests plus 30 warm-ups. | Complete |
| E3 idle | Three paired 120-second windows for controller and agent | [Per-run summary](e3-idle-summary.csv), raw pairs [01](e3-idle-pair-01/), [02](e3-idle-pair-02/), [03](e3-idle-pair-03/) | 3 of 3 completed |
| E3 load | Three paired 120-second windows at one status request per second | [Per-run summary](e3-load-summary.csv), raw pairs [01](e3-load-pair-01/), [02](e3-load-pair-02/), [03](e3-load-pair-03/) | 3 of 3 completed |
| E2 preflight | Dedicated container restart | Operator screenshots confirm both preflight restarts. `StartedAt` advanced after HLCP and after fresh SSH. Both returned to running. | Complete |
| E2 timed pilot, 2026-11-16 11:31:56 UTC | One restart per mode, script revision ded1adf | Screenshot summary. Fresh SSH 281.52 ms, multiplexed SSH 146.35 ms, HLCP 160.91 ms. All three passed running-state and StartedAt checks. | Pilot completed |
| E2 final, 2026-11-16 11:51:11 UTC | Three blocks of 10 restarts per mode, script revision ded1adf | [Archived samples and checksums](e2-final-20261116T115111Z/). Recomputed statistics match the supplied summary. | Complete. 90 successful trials. |
| E4 clean stop, 2026-11-16 13:01 UTC | Five stops and relaunches of a disposable agent on Debian loopback | [Timing samples and method](e4-clean-20261116T130119Z/) | 5 of 5 completed |
| E4 silent interruption, 2026-11-16 13:04 UTC | Five pauses and resumes of one agent process over loopback | [Timing samples and method](e4-silent-20261116T130404Z/) | 5 of 5 completed |
| E5, 2026-11-16 13:06 UTC | Functional policy and retained-result checks on test controller | [Observed TLS and API responses](e5-policy-20261116T130605Z/policy.json) | 7 checks passed in one run |

Keep the original CSV files for each run. Record the script revision, service
versions, run duration and any concurrent activity. Publish sanitized copies of
metadata alongside the samples. The E1 and E2 final summaries were recalculated
from the measured samples and agree with the original summaries.
