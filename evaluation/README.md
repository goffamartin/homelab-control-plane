# HLCP semester project

HomeLab Control Plane lets an operator find a service on a remote machine, read its logs and run an allowed action. The semester project tested this workflow on a Debian node running two applications. Timing and restart checks used a dedicated Podman container.

## Submission

- [English report](report.md)
- [Czech report](report-cs.md)
- [English presentation with Czech speaker notes](HLCP_final_results_EN.pptx)
- [Talk outline](presentation-outline.md)
- [Final measurements and commands to reproduce the summaries](data/final-agent-720dcad/README.md)

## What we tested

| Experiment | Setup and checks |
|---|---|
| Status and restart | HLCP, a new SSH connection per command and an already open SSH connection. Each method had 300 measured status checks and 30 restarts. We inspected the same container before and after each restart to confirm a changed start time and a running state. |
| CPU and memory | Separate controller and agent service groups. Three two-minute runs at idle and three at one status request per second. |
| Recovery | Five orderly stops and relaunches, plus five successive pauses of the same process with its connection open. We measured offline detection from the interruption and measured reconnection and the first successful status from relaunch or resume. |
| Access rules | Three disposable runs checked certificate and token handling, disallowed actions, conflicting command IDs and lookup or resubmission of a saved result. |

The test setup and build details are recorded with the [final measurements](data/final-agent-720dcad/README.md). For setup and preflight commands, start with the [pilot runbook](pilot-runbook.md). The [experiment register](data/results.md) includes earlier runs as well as the final dataset.

## Reproduce the summaries

From the repository root, run the following commands with Python 3:

```sh
python3 evaluation/scripts/analyze_status.py evaluation/data/final-agent-720dcad/e1/samples.csv
python3 evaluation/scripts/analyze_status.py evaluation/data/final-agent-720dcad/e2/samples.csv
python3 evaluation/scripts/summarize_resource_pairs.py evaluation/data/e3-refresh-20261118T104656Z
python3 -m unittest discover -s evaluation/scripts -p 'test_*.py'
```

The resource summary records memory in MiB. The reports and presentation use decimal MB, calculated from the same source values. The published samples and their checksums preserve the original units.
