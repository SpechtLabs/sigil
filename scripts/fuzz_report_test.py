"""Exercise issue publication with a fake gh CLI; never contact GitHub."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


REPORTER = Path(__file__).with_name("report-fuzz-failure.sh")


class FuzzReportTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        directory = Path(self.directory.name)
        self.calls = directory / "calls.jsonl"
        fake_gh = directory / "gh"
        fake_gh.write_text(
            f"#!{sys.executable}\n"
            "import json, os, pathlib, sys\n"
            "args = sys.argv[1:]\n"
            "call = {'args': args}\n"
            "if '--body-file' in args:\n"
            "    call['body'] = pathlib.Path(args[args.index('--body-file') + 1]).read_text()\n"
            "with open(os.environ['TEST_CALLS'], 'a') as log:\n"
            "    log.write(json.dumps(call) + '\\n')\n"
            "if args[0] == 'api':\n"
            "    print(os.environ.get('TEST_ISSUES', ''), end='')\n"
            "    sys.exit(int(os.environ.get('TEST_API_EXIT', '0')))\n"
            "sys.exit(int(os.environ.get('TEST_WRITE_EXIT', '0')))\n"
        )
        fake_gh.chmod(0o755)
        self.env = {
            **os.environ,
            "PATH": f"{directory}{os.pathsep}{os.environ['PATH']}",
            "TEST_CALLS": str(self.calls),
            "GITHUB_REF": "refs/heads/main",
            "GITHUB_EVENT_NAME": "schedule",
            "GITHUB_REPOSITORY": "example/sigil",
            "GITHUB_SERVER_URL": "https://github.com",
            "GITHUB_RUN_ID": "123",
            "GITHUB_RUN_ATTEMPT": "2",
            "GITHUB_SHA": "abc123",
            "TARGETS_RESULT": "success",
            "FUZZ_RESULT": "failure",
            "FUZZTIME": "60m",
        }

    def report(self, **changes):
        self.calls.unlink(missing_ok=True)
        result = subprocess.run(
            ["bash", str(REPORTER)],
            env={**self.env, **changes},
            capture_output=True,
            text=True,
            check=False,
        )
        calls = []
        if self.calls.exists():
            calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        return result, calls

    def test_creates_issue_with_reproduction_context(self):
        result, calls = self.report()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertIn("--paginate", calls[0]["args"])
        self.assertIn("repos/example/sigil/issues?state=open&per_page=100", calls[0]["args"])
        self.assertEqual(calls[1]["args"][:2], ["issue", "create"])
        body = calls[1]["body"]
        for text in (
            "<!-- sigil:extended-fuzzing -->",
            "https://github.com/example/sigil/actions/runs/123/attempts/2",
            "https://github.com/example/sigil/commit/abc123",
            "https://github.com/example/sigil/actions/runs/123#artifacts",
            "60m, with two workers",
            "reproduction command printed in fuzz.log",
        ):
            self.assertIn(text, body)

    def test_repeated_failures_comment_on_existing_issue(self):
        result, calls = self.report(TEST_ISSUES="42\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls[1]["args"][:3], ["issue", "comment", "42"])

    def test_multiple_matches_publish_only_one_comment(self):
        result, calls = self.report(TEST_ISSUES="42\n43\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[1]["args"][:3], ["issue", "comment", "42"])

    def test_setup_failure_reports_even_when_fuzzing_is_skipped(self):
        result, calls = self.report(TARGETS_RESULT="failure", FUZZ_RESULT="skipped")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Target discovery: failure", calls[1]["body"])
        self.assertIn("Fuzz jobs: skipped", calls[1]["body"])

    def test_manual_main_failure_reports_selected_duration(self):
        result, calls = self.report(GITHUB_EVENT_NAME="workflow_dispatch", FUZZTIME="10m")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("10m, with two workers", calls[1]["body"])

    def test_success_cancellation_and_untrusted_refs_do_not_contact_github(self):
        for changes in (
            {"FUZZ_RESULT": "success"},
            {"FUZZ_RESULT": "cancelled"},
            {"GITHUB_EVENT_NAME": "pull_request"},
            {"GITHUB_REF": "refs/heads/feature"},
            {"GITHUB_REF": "refs/tags/main"},
        ):
            with self.subTest(changes=changes):
                result, calls = self.report(**changes)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls, [])

    def test_lookup_error_never_creates_a_duplicate(self):
        result, calls = self.report(TEST_API_EXIT="1", TEST_ISSUES="42\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["args"][0], "api")

    def test_publication_failure_fails_the_job(self):
        for issues in ("", "42\n"):
            with self.subTest(issues=issues):
                result, calls = self.report(TEST_ISSUES=issues, TEST_WRITE_EXIT="1")
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(calls), 2)
