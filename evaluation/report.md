# HLCP — Managing containers in a HomeLab

Martin Goffa — ANI-COM

18 November 2026

[Project source](../README.md) · [Measurement data](data/final-agent-720dcad/README.md)

## Abstract

We run two applications across several containers in our HomeLab. We built HomeLab Control Plane (HLCP) so we could find a container, follow its logs and run an allowed action from one place. On a dedicated test container, a status check took about 80 ms through HLCP and a restart took 165 ms at the median. Opening a fresh SSH connection took longer for both tasks, while reusing one was slightly faster. At one status check per second, the agent used about 7% of one logical CPU.

## 1. Problem and research question

One application in our HomeLab spans three containers. A second runs in a single container on the same node. When investigating a fault, the operator can select an application tag, find the relevant container and read its logs in the web client. The CLI exposes the same inventory and allowed actions. For a restart, the controller assigns a command ID that the operator can use to look up the outcome later.

This deployment motivated two questions. How much time does the controller add to a live status check or restart compared with SSH? When an agent disconnects or a request exceeds its permissions, what does the operator actually observe? We timed the core CLI path on a dedicated test container. The deployed web client illustrates the workflow. Its response time and operator usability would require separate measurements.

## 2. Design choices

Opening an SSH connection takes time, and OpenSSH can share an existing connection across commands [1]. We measured both ways of using SSH because HLCP keeps its connection to the agent open.

The controller and agent authenticate each other with TLS certificates [2]. The controller checks which actions the caller may run, and the agent only exposes actions configured for each resource. They exchange messages encoded in CBOR [3], with a frame around each message so the receiver knows where it ends.

A restart can finish even if the operator loses the connection before hearing back. HLCP assigns each command an ID so the operator can look up a saved result later. If the agent crashes before saving the result, the outcome can remain unknown.

## 3. What we built and deployed

When an application has a problem, we usually start with its logs. In HLCP we can filter the container list by application and follow the output in the browser. The same containers are available from the command line. On a test container we checked that we could follow new output while it ran and read recent lines after it stopped.

We also used HLCP to check the state of a live application container. Starting, stopping and restarting a container depends on which actions the administrator has enabled. We verified restarts on a dedicated test container.

### How a command runs

[HLCP Web](https://github.com/goffamartin/hlcp-web) and the CLI both send requests to the controller. The controller checks access and passes the command to the agent on the host running the containers. For a live status check, the agent asks Podman for the container's current state. The caller can retrieve a saved result later using the command ID.

## 4. How we tested HLCP

The controller and agent ran on separate virtual machines, each with two logical CPUs. We used the same test container and the same account on the agent for all three access paths. A short pilot gave us a chance to check that each path worked before collecting data.

- **Status and restart.** We compared HLCP, SSH with a fresh connection for each command and SSH with a shared connection. After warming up the status commands, we measured 300 status checks per path. We timed another 30 restarts per path separately. We shuffled the order within each block and timed each command from launch to return. After every restart, we checked that the same container was running with a new start time.
- **Resource use.** We sampled CPU and memory for both services over three two-minute windows at idle and three windows with one status check per second. Memory covers the entire service cgroup [4].
- **Recovery and access.** On a temporary setup, we stopped and relaunched the agent five times and paused it with its connection open five times. We recorded when the controller noticed the interruption and when a live status check worked again. In three separate runs we also checked how HLCP handled invalid credentials, disallowed actions and requests for a saved result.

The latency table reports the median and p95. The p95 is the time by which at least 95% of successful commands had finished. [Scripts and measurement data](data/final-agent-720dcad/README.md)

## 5. Results

### Live status and restart

| Task and access path | Successful / attempted | Median (ms) | p95 (ms) |
|---|---:|---:|---:|
| Live status · HLCP | 300/300 | 80.29 | 92.54 |
| Live status · Fresh SSH | 300/300 | 179.10 | 192.59 |
| Live status · Multiplexed SSH | 300/300 | 66.69 | 74.81 |
| Restart · HLCP | 30/30 | 165.31 | 191.24 |
| Restart · Fresh SSH | 30/30 | 272.37 | 301.17 |
| Restart · Multiplexed SSH | 30/30 | 159.41 | 199.46 |

Opening a new SSH connection took the longest at the median for both tasks. With a shared SSH connection, the median was 13.60 ms below HLCP for status and 5.90 ms below HLCP for restart. For restart, HLCP had a p95 that was 8.22 ms lower than shared SSH. All 90 restarts passed independent inspection of container identity, start time and running state. The timed command ends before that inspection. Application readiness would require an application-level probe.

### Resource use during polling

| Service | Idle CPU (%) | CPU at one request/s (%) | Loaded memory, median in each run (MB) |
|---|---:|---:|---:|
| Controller | 0.052–0.054 | 0.47–1.01 | 12.08 / 15.18 / 16.21 |
| Agent | 3.00–3.03 | 6.90–7.11 | 12.49 / 13.46 / 13.50 |

All three runs completed 160 of 160 scheduled requests. CPU values are 120-second run averages as percentages of one logical CPU. Memory values cover the service cgroup. The highest sampled agent memory under load was 14.07 MB.

![Service CPU at idle and under load](figures/service-cpu.png)

[Memory figure](figures/service-memory.png)

### Recovery and access policy

After a clean stop, the controller showed the agent offline after a median of 8.26 ms. After relaunch, the first live status check succeeded after a median of 107.69 ms. [Clean-stop samples](data/final-agent-720dcad/clean/samples.csv)

For a silent interruption, we paused the same process five times in a row with its connection open. The controller showed it offline after a median of 15.03 s from pause. We then measured the time from resuming the process to a new connection. It took 0.97 s after the first pause and 15.80 s after the fifth. A live status check succeeded after every reconnect. [Repeated-pause samples](data/final-agent-720dcad/silent/samples.csv)

The agent deliberately waits longer after short sessions to avoid retrying too often on an unstable connection. The delay resets after a session lasts at least one minute. In this series, each new pause followed shortly after a successful status check, so the delay grew across trials.

TLS rejected connections with a missing client certificate and with a certificate from an unrelated authority. The API returned 401 for an invalid token, 422 for a disallowed restart and 409 for a conflicting command ID. A lookup and a repeated submission returned the same result as the original request. All three independent trials returned the expected responses.

## 6. Conclusion

HLCP gives us one place to find a container, check its logs and look up the outcome of an action. The controller and agent check each other’s certificates, and operators can only run allowed actions. The agent reconnects after an interruption, and operators can look up saved results. In our tests, HLCP added about 14 ms to a typical status check and 6 ms to a restart compared with shared SSH. At one status check per second, the agent used about 7% of one logical CPU and had run medians of 12.49 to 13.50 MB.

We measured latency on one container across two virtual machines, recovery on a single machine and resource use in two-minute windows. The results describe those tasks in our HomeLab setup.

## References

1. OpenBSD. *ssh_config(5).* https://man.openbsd.org/ssh_config (accessed 2026-11-03).
2. Rescorla, E. *The Transport Layer Security (TLS) Protocol Version 1.3.* RFC 8446,
   2018. https://www.rfc-editor.org/rfc/rfc8446.html. Original TLS 1.3 specification.
3. Bormann, C., Hoffman, P. *Concise Binary Object Representation (CBOR).* RFC 8949,
   2020. https://www.rfc-editor.org/rfc/rfc8949.html (accessed 2026-11-03).
4. Linux kernel documentation. *Control Group v2.*
   https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html (accessed 2026-11-03).
5. Goffa, M. *HomeLab Control Plane*, v1.1.0, implementation source.
   https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0.
