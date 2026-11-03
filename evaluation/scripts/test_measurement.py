"""Synthetic checks for data validation/analysis. No SSH or HLCP processes are invoked."""
import json
import csv
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
from analyze_status import summarize
from measure_status import main, validate_result


class MeasurementTests(unittest.TestCase):
    def test_hlcp_requires_matching_terminal_success_and_state(self):
        result = dict(command_id="id", success=True, code="OK", message="running")
        self.assertTrue(validate_result("hlcp", json.dumps(result), "id"))
        for changes in ({"command_id": "other"}, {"success": False}, {"code": "TIMEOUT"},
                        {"message": "stopped"}, {"success": 1}):
            self.assertFalse(validate_result("hlcp", json.dumps(result | changes), "id"))
        for invalid in ("not JSON", "[]", "null", "{}"):
            self.assertFalse(validate_result("hlcp", invalid, "id"))

    def test_ssh_does_not_equate_empty_or_stopped_output_with_success(self):
        self.assertTrue(validate_result("ssh_mux", "running\n", ""))
        for output in ("", "stopped", "running\nwarning", "{\"message\":\"running\"}"):
            self.assertFalse(validate_result("ssh_fresh", output, ""))

    def test_failures_remain_in_denominator_without_inflating_success_latency(self):
        rows = [dict(duration_ns="1000000", success="1", error=""),
                dict(duration_ns="3000000", success="1", error=""),
                dict(duration_ns="30000000000", success="0", error="TIMEOUT")]
        result = summarize(rows)
        self.assertEqual((result["attempts"], result["successes"], result["failures"], result["timeouts"]), (3, 2, 1, 1))
        self.assertAlmostEqual(result["success_rate"], 2 / 3)
        self.assertEqual(result["median_ms"], 2)
        self.assertEqual(result["p95_ms"], 3)

    def test_all_failures_have_no_fabricated_latency(self):
        result = summarize([dict(duration_ns="1000", success="0", error="EXIT_NONZERO")])
        self.assertEqual(result["success_rate"], 0)
        self.assertIsNone(result["median_ms"])
        self.assertIsNone(result["p95_ms"])

    def test_nearest_rank_p95(self):
        rows = [dict(duration_ns=str(n * 1000000), success="1", error="") for n in range(1, 101)]
        self.assertEqual(summarize(rows)["p95_ms"], 95)

    def test_full_synthetic_pilot_records_all_modes_and_never_mutates(self):
        commands = []

        def fake_invoke(argv, timeout):
            commands.append(argv)
            if argv[0] == "ssh":
                output = "running\n"
            elif "--version" in argv:
                output = "synthetic HLCP version"
            elif "--dry-run" in argv:
                output = json.dumps(dict(node="synthetic", resource="hlcp-bench", action="status", read_only=True, known_operation=False))
            elif "--command-id" in argv:
                output = json.dumps(dict(command_id=argv[argv.index("--command-id") + 1], success=True, code="OK", message="running"))
            else:
                output = '{"type":"container"}'
            return 1000000, 0, output.encode(), b"", ""

        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "run"
            with mock.patch("measure_status.sys.platform", "linux"), \
                 mock.patch("measure_status.os.geteuid", return_value=1000, create=True), \
                 mock.patch("measure_status.os.uname", return_value=SimpleNamespace(nodename="synthetic"), create=True), \
                 mock.patch("measure_status.invoke", side_effect=fake_invoke), \
                 mock.patch("measure_status.time.sleep"):
                self.assertEqual(main(["--node", "synthetic", "--ssh-target", "tester@synthetic",
                                       "--blocks", "2", "--samples", "2", "--warmups", "1", "--out", str(output)]), 0)
            with (output / "samples.csv").open(newline="", encoding="utf-8") as stream:
                rows = list(csv.DictReader(stream))
            self.assertEqual(len(rows), 15)
            self.assertEqual(sum(row["phase"] == "measure" for row in rows), 12)
            self.assertTrue(all(row["success"] == "1" for row in rows))
            ids = [row["command_id"] for row in rows if row["mode"] == "hlcp"]
            self.assertEqual(len(set(ids)), 5)
            self.assertEqual(json.loads((output / "metadata.json").read_text())["status"], "complete")
            self.assertFalse(any("restart" in command or "stop" in command for command in commands))
            self.assertTrue(any("ProxyCommand=false" in command for command in commands))


if __name__ == "__main__":
    unittest.main()
