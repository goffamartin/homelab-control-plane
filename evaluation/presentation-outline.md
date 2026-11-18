# HLCP results talk

The [presentation (PDF)](HLCP_final_results_EN.pdf) has 13 English slides. The suggested pace is about 13 minutes 55 seconds.

| Slide | Time | Point to make |
|---|---|---|
| 1. HomeLab Control Plane | 0:25 | Introduce the semester project. |
| 2. Why I built HLCP | 0:35 | Explain the problem of finding a service and acting on it from one place. |
| 3. Find a container from the terminal | 0:55 | Use the example commands for synthetic-node and synthetic-second. |
| 4. Check the same container in the browser | 0:55 | Open the detail page, find the logs and see the result of the example restart. |
| 5. How HLCP runs a command | 1:15 | Follow a request from the terminal or browser through the controller to the agent and Podman. |
| 6. How I compared HLCP with SSH | 1:35 | Explain the three access paths, sample counts and independent restart check. |
| 7. Status latency | 1:15 | HLCP took about 80 ms at the median. An open SSH connection was about 14 ms faster. |
| 8. Restart latency | 1:10 | HLCP had a 6 ms higher median and an 8 ms lower p95 than open SSH in this test. |
| 9. Agent CPU | 1:10 | At one status check per second the agent used about 7% of one logical CPU. |
| 10. Agent memory | 1:05 | Agent medians were 12.49, 13.46 and 13.50 MB across three runs. |
| 11. Recovery | 1:25 | Show the time to reconnect across five consecutive pauses. Explain why the agent waits longer after short sessions. |
| 12. What HLCP allows and blocks | 1:05 | Cover certificates, permission checks and lookup of saved results. |
| 13. What HLCP adds to the HomeLab | 1:05 | Summarize remote control, security and recovery against the measured cost. |

[English report](report.md) · [Czech report](report-cs.md) · [Final measurements](data/final-agent-720dcad/README.md)
