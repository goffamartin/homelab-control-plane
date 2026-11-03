# Measurement data

No HomeLab experiment results are present yet. Do not treat generated fixtures,
warm-ups or pilot runs as the final evaluation dataset.

Use a new immutable directory for every run, e.g. `2026-11-03-e1-pilot/`, containing:

- environment inventory for both VMs;
- method/script commit, running binary revisions and container image digest;
- role, node/resource mapping, baseline SSH configuration and command options;
- raw samples including failures, phase, order, block, monotonic duration and UTC time;
- CPU/memory series or recovery/policy event records, when applicable;
- notes on background load, invalidation/exclusion reasons and redactions;
- SHA-256 checksums for every final input file.

Commit sanitized data and analysis code together before the final report. Keep
private keys, tokens, cookies, secret application logs and credential-bearing
configuration outside this repository. Never overwrite a raw file to rerun a scenario.
