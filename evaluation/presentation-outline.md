# HLCP results talk

Allow 14 minutes 35 seconds for the thirteen slides. Use the deployed applications to introduce the problem, then use the dedicated test container for every performance claim.

| Slide | Time | Point to make |
|---|---|---|
| 1. HomeLab Control Plane | 0:45 | Start with a service whose logs the operator needs to inspect. |
| 2. Two deployed applications | 1:00 | One application spans three containers. Tags let the operator find its resources. The web client can open a log stream. |
| 3. Controller and agent | 1:15 | Trace a named action to the local Podman adapter and a retained result. |
| 4. Working features | 0:55 | Show the workflow from tag and log stream to a permitted command and retained result. |
| 5. Experiment | 1:25 | Compare the same live operation with fresh SSH and an established SSH master on one VM pair. |
| 6. Restart trial | 1:00 | Explain exactly when timing starts and stops and how the independent inspection detects a real restart. |
| 7. Live status | 1:15 | Use the medians and p95 values in the chart to distinguish connection setup from command execution. |
| 8. Restart | 1:10 | All 90 trials changed the start time of the same running container. Application readiness needs a separate probe. |
| 9. CPU | 1:10 | At one live query per second the agent service used about 7% of one logical CPU. |
| 10. Memory | 1:05 | The three loaded agent cgroup medians were 11.91, 12.83 and 12.88 MiB. These are two-minute windows. |
| 11. Recovery | 1:25 | On loopback, the controller detected each silent pause in about 15 seconds. After resume, the first successful live status took progressively longer. |
| 12. Policy | 1:05 | Show one rejected restart and one retained result. Each case ran on three disposable instances. |
| 13. Conclusion | 1:05 | The operator gains named actions and retrievable outcomes. All 90 restarts passed an independent inspection. The three polling runs returned all 480 expected results. |

For questions about connection reuse, CPU accounting or repeated pauses, use the [report](report.md) and the [per-run data](data/final-agent-720dcad/README.md).
