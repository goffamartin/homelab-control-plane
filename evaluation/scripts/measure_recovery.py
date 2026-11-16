#!/usr/bin/env python3
"""Measure disconnect and policy behavior with disposable HLCP processes."""
import argparse
import csv
from datetime import datetime, timezone
import json
from pathlib import Path
import secrets
import socket
import ssl
import subprocess as sp
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import uuid


NODE = "evaluation-agent"
AGENT = """
import json, os, pathlib, signal, subprocess, sys, tempfile
os.umask(0o077)
files = json.loads(sys.stdin.readline())
with tempfile.TemporaryDirectory(prefix="hlcp-e4-agent-") as work:
    path = pathlib.Path(work)
    config = files.pop("config")
    for name, content in files.items():
        (path / name).write_text(content)
    (path / "agent.yaml").write_text(json.dumps(config))
    with subprocess.Popen(["hlcp-agent", "-config", "agent.yaml"], cwd=work,
                          stdin=subprocess.DEVNULL) as child:
        try:
            for command in sys.stdin:
                if command.strip() == "pause":
                    child.send_signal(signal.SIGSTOP)
                elif command.strip() == "resume":
                    child.send_signal(signal.SIGCONT)
        finally:
            if child.poll() is None:
                child.send_signal(signal.SIGCONT)
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
"""


def free_port(host):
    with socket.socket() as sock:
        sock.bind((host, 0))
        return sock.getsockname()[1]


def certificates(path, host):
    def openssl(*args):
        sp.run(["openssl", *args], cwd=path, check=True, capture_output=True, timeout=10)

    openssl("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
            "-nodes", "-subj", "/CN=HLCP evaluation CA", "-days", "1",
            "-addext", "basicConstraints=critical,CA:TRUE",
            "-addext", "keyUsage=critical,keyCertSign,cRLSign",
            "-keyout", "ca.key", "-out", "ca.crt")
    for name, usage, san in (("controller", "serverAuth", f"IP:{host}"),
                             ("agent", "clientAuth", f"URI:urn:hlcp:node:{NODE}")):
        openssl("req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
                "-nodes", "-subj", f"/CN={name}", "-keyout", f"{name}.key", "-out", f"{name}.csr")
        (path / "ext").write_text(
            f"basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\n"
            f"extendedKeyUsage={usage}\nsubjectAltName={san}\n"
        )
        openssl("x509", "-req", "-in", f"{name}.csr", "-CA", "ca.crt", "-CAkey",
                "ca.key", "-CAcreateserial", "-days", "1", "-extfile", "ext", "-out", f"{name}.crt")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--controller-ip", required=True)
    parser.add_argument("--controller-binary", default="hlcp-controller")
    parser.add_argument("--out", required=True, type=Path)
    scenario = parser.add_mutually_exclusive_group()
    scenario.add_argument("--silent", action="store_true")
    scenario.add_argument("--policy-only", action="store_true")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=False)
    agent_command = ["python3", "-c", AGENT]
    with tempfile.TemporaryDirectory(prefix="hlcp-e4-controller-") as temp:
        path = Path(temp)
        certificates(path, args.controller_ip)
        token = secrets.token_hex(32)
        (path / "token").touch(mode=0o600)
        (path / "token").write_text(token)
        (path / "nodes.json").write_text(json.dumps([NODE]))
        listen = f"{args.controller_ip}:{free_port(args.controller_ip)}"
        api = f"127.0.0.1:{free_port('127.0.0.1')}"
        config = dict(controller_address=listen, server_name=args.controller_ip,
                      certificate_file="agent.crt", private_key_file="agent.key", ca_file="ca.crt",
                      journal_file="journal.json", node_id=NODE, agent_version="evaluation",
                      capabilities=["resource.control", "podman"],
                      resources=[dict(id="hlcp-bench", type="container", adapter="podman",
                                      target="hlcp-bench", actions=["status"])])
        payload = {f: (path / f).read_text() for f in ("agent.key", "agent.crt", "ca.crt")}
        payload["config"] = config
        argv = [args.controller_binary, "-listen", listen, "-api-listen", api, "-cert", "controller.crt",
                "-key", "controller.key", "-ca", "ca.crt", "-api-token-file", "token",
                "-node-allowlist-file", "nodes.json", "-ledger-file", "ledger.json"]

        def request(suffix, body=None, credential=token):
            headers = {"Authorization": f"Bearer {credential}", "Content-Type": "application/json"}
            data = json.dumps(body).encode() if body is not None else None
            with urlopen(Request(f"http://{api}/v1/nodes{suffix}", data, headers), timeout=5) as reply:
                return json.load(reply)

        def wait_for(predicate, timeout=20):
            end = time.monotonic() + timeout
            while time.monotonic() < end:
                try:
                    result = request(f"/{NODE}")
                    if predicate(result):
                        return result, time.monotonic_ns()
                except (OSError, HTTPError):
                    pass
                time.sleep(0.05)
            raise RuntimeError("Test node did not reach the expected state")

        def live_status(command_id=None):
            result = request(f"/{NODE}/commands", dict(command_id=command_id or uuid.uuid4().hex,
                             resource_id="hlcp-bench", action="status"))
            if result.get("success") is not True or result.get("message") != "running":
                raise RuntimeError("Test resource did not report running")
            return result

        def policy_checks():
            def refusal(suffix, body, credential, expected):
                try:
                    request(suffix, body, credential)
                except HTTPError as error:
                    with error:
                        actual = (error.code, json.load(error).get("code"))
                    if actual == expected:
                        return {"http": actual[0], "code": actual[1]}
                    raise RuntimeError(f"Unexpected refusal: {actual}") from error
                raise RuntimeError("Expected a refused request.")

            endpoint = f"/{NODE}/commands"
            command_id = uuid.uuid4().hex
            checks = {
                "invalid_token": refusal("", None, secrets.token_hex(32), (401, "UNAUTHORIZED")),
                "unlisted_action": refusal(endpoint, dict(command_id=uuid.uuid4().hex,
                    resource_id="hlcp-bench", action="restart"), token, (422, "ACTION_NOT_SUPPORTED")),
            }
            first = live_status(command_id)
            checks["retained_result_matches"] = request(f"/{NODE}/commands/{command_id}").get("result") == first
            if not checks["retained_result_matches"]:
                raise RuntimeError("Retained result mismatch.")
            checks["same_id_result_matches"] = live_status(command_id) == first
            if not checks["same_id_result_matches"]:
                raise RuntimeError("Duplicate status returned a different result.")
            checks["conflicting_command_id"] = refusal(endpoint, dict(command_id=command_id,
                resource_id="hlcp-bench", action="restart"), token, (409, "COMMAND_CONFLICT"))
            if args.policy_only:
                def tls_alert(cert_path=None):
                    ctx = ssl.create_default_context(cafile=str(path / "ca.crt"))
                    ctx.set_alpn_protocols(["hlcp/1"])
                    if cert_path is not None:
                        ctx.load_cert_chain(cert_path / "agent.crt", cert_path / "agent.key")
                    try:
                        with socket.create_connection((args.controller_ip, int(listen.rsplit(":", 1)[1])), timeout=2) as conn:
                            with ctx.wrap_socket(conn, server_hostname=args.controller_ip) as peer:
                                peer.recv(1)
                    except ssl.SSLError as error:
                        return error.reason
                    raise RuntimeError("No TLS refusal alert received.")

                checks["missing_client_certificate"] = tls_alert()
                other = path / "untrusted"
                other.mkdir()
                certificates(other, args.controller_ip)
                checks["untrusted_client_certificate"] = tls_alert(other)
                if checks["missing_client_certificate"] not in (
                    "TLSV13_ALERT_CERTIFICATE_REQUIRED", "TLSV1_ALERT_CERTIFICATE_REQUIRED"
                ):
                    raise RuntimeError(f"Unexpected missing client certificate alert: {checks['missing_client_certificate']}")
                if checks["untrusted_client_certificate"] != "TLSV1_ALERT_UNKNOWN_CA":
                    raise RuntimeError(f"Unexpected untrusted client certificate alert: {checks['untrusted_client_certificate']}")
            (args.out / "policy.json").write_text(json.dumps(checks, indent=2) + "\n")

        def start_agent():
            child = sp.Popen(agent_command, stdin=sp.PIPE, text=True)
            try:
                child.stdin.write(json.dumps(payload) + "\n")
                child.stdin.flush()
                return child
            except BaseException:
                stop_agent(child)
                raise

        def stop_agent(child):
            child.stdin.close()
            try:
                child.wait(timeout=12)
            except sp.TimeoutExpired:
                child.kill()
                child.wait()
                raise

        controller = sp.Popen(argv, cwd=path, stdin=sp.DEVNULL)
        agent = None
        try:
            agent = start_agent()
            record, _ = wait_for(lambda n: n["state"] == "online")
            live_status()
            if args.silent or args.policy_only:
                policy_checks()
            if args.policy_only:
                print("Policy checks passed.", flush=True)
                return
            with (args.out / "samples.csv").open("x", newline="") as stream:
                writer = csv.writer(stream)
                writer.writerow(("trial", "utc", "disconnect_observed_ms", "new_session_observed_ms",
                                 "first_successful_status_ms"))
                for trial in range(1, 6):
                    old_session = record["session_id"]
                    utc = datetime.now(timezone.utc).isoformat()
                    started = time.monotonic_ns()
                    if args.silent:
                        agent.stdin.write("pause\n")
                        agent.stdin.flush()
                    else:
                        stop_agent(agent)
                        agent = None
                    _, offline = wait_for(lambda n: n["state"] == "offline", timeout=25)
                    started_agent = time.monotonic_ns()
                    if args.silent:
                        agent.stdin.write("resume\n")
                        agent.stdin.flush()
                    else:
                        agent = start_agent()
                    record, online = wait_for(lambda n: n["state"] == "online" and n["session_id"] != old_session)
                    live_status()
                    passed = time.monotonic_ns()
                    writer.writerow((trial, utc, (offline - started) / 1e6,
                                     (online - started_agent) / 1e6, (passed - started_agent) / 1e6))
                    stream.flush()
                    print(f"Trial {trial}/5 passed", flush=True)
        finally:
            try:
                if agent is not None:
                    stop_agent(agent)
            finally:
                controller.terminate()
                try:
                    controller.wait(timeout=5)
                except sp.TimeoutExpired:
                    controller.kill()
                    controller.wait()


if __name__ == "__main__":
    main()
