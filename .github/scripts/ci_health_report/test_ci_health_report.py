import unittest
from unittest.mock import MagicMock, patch
from collections import defaultdict
from datetime import datetime, timezone
import urllib.request
import urllib.error

import ci_health_report
from ci_health_report import (
    bucket_count,
    trend_indicator,
    build_report,
    main,
    _urlopen,
)


class _Tests(unittest.TestCase):

    @patch("time.sleep")
    @patch("urllib.request.urlopen")
    def test_rate_limit_retries_with_wait(self, mock_urlopen, mock_sleep):
        """_urlopen sleeps Retry-After + 5s on 429 then retries successfully."""
        import http.client
        msg = http.client.HTTPMessage()
        msg["Retry-After"] = "10"
        resp = MagicMock()
        resp.read.return_value = b'{"ok": true}'
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.side_effect = [
            urllib.error.HTTPError("https://api.github.com/test", 429, "Too Many Requests", msg, None),
            resp,
        ]
        result = _urlopen(urllib.request.Request("https://api.github.com/test"))
        mock_sleep.assert_called_once_with(15)  # Retry-After(10) + 5
        self.assertEqual(result, b'{"ok": true}')

    def test_bucket_count(self):
        """bucket_count returns daily, weekly, or monthly bucket counts."""
        self.assertEqual(bucket_count(7),  7)   # daily
        self.assertEqual(bucket_count(14), 14)  # daily (boundary)
        self.assertEqual(bucket_count(30), 4)   # weekly
        self.assertEqual(bucket_count(90), 12)  # weekly (boundary)
        self.assertEqual(bucket_count(91), 3)   # monthly

    def test_trend_indicator_increasing(self):
        """trend_indicator returns ↑ when recent half has higher failure rate."""
        buckets = [
            {"runs": 10, "failures": 1},
            {"runs": 10, "failures": 1},
            {"runs": 10, "failures": 5},
            {"runs": 10, "failures": 5},
        ]
        result = trend_indicator(buckets)
        self.assertTrue(result.startswith("↑"))

    def test_trend_indicator_decreasing(self):
        """trend_indicator returns ↓ when recent half has lower failure rate."""
        buckets = [
            {"runs": 10, "failures": 5},
            {"runs": 10, "failures": 5},
            {"runs": 10, "failures": 1},
            {"runs": 10, "failures": 1},
        ]
        result = trend_indicator(buckets)
        self.assertTrue(result.startswith("↓"))

    def test_build_report_trend_shown_when_sufficient_runs(self):
        """build_report shows trend arrow when runs >= num_buckets * 2."""
        buckets = [{"runs": 5, "failures": 1}, {"runs": 5, "failures": 4}]  # 10 runs, threshold=4
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Tests", "build")]["runs"] = 10
        stats[("Tests", "build")]["failures"] = 5
        stats[("Tests", "build")]["buckets"] = buckets
        branch_totals = {"main": {"runs": 10, "failures": 5}}
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("| Workflow | Job | Runs | Failures | Rate | Trend |", report)
        self.assertIn("| Tests | build | 10 | 5 | 50.0% | ↑", report)
        self.assertIn("**Total job runs:** 10", report)
        self.assertIn("**Flaky rate:** 50.0% ❌", report)

    def test_build_report_flaky_rate_within_acceptance(self):
        """build_report marks the flaky rate ✅ when below the 3% threshold."""
        buckets = [{"runs": 50, "failures": 1}, {"runs": 50, "failures": 1}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Tests", "build")]["runs"] = 100
        stats[("Tests", "build")]["failures"] = 2
        stats[("Tests", "build")]["buckets"] = buckets
        branch_totals = {"main": {"runs": 100, "failures": 2}}
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("**Flaky rate:** 2.0% ✅ (acceptance: < 3%)", report)

    def test_build_report_per_branch_breakdown(self):
        """build_report names monitored branches and lists per-branch flaky rates."""
        buckets = [{"runs": 10, "failures": 2}, {"runs": 10, "failures": 2}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Tests", "build")]["runs"] = 20
        stats[("Tests", "build")]["failures"] = 4
        stats[("Tests", "build")]["buckets"] = buckets
        branch_totals = {
            "main":  {"runs": 15, "failures": 3},
            "squid": {"runs": 5,  "failures": 1},
        }
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("on main, squid —", report)
        self.assertIn("- `main`: 20.0% ❌ (3 failures / 15 job runs)", report)
        self.assertIn("- `squid`: 20.0% ❌ (1 failure / 5 job runs)", report)

    def test_build_report_flaky_rate_at_threshold_fails(self):
        """build_report marks exactly 3.0% as ❌ (acceptance is strictly below)."""
        buckets = [{"runs": 50, "failures": 2}, {"runs": 50, "failures": 1}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Tests", "build")]["runs"] = 100
        stats[("Tests", "build")]["failures"] = 3
        stats[("Tests", "build")]["buckets"] = buckets
        branch_totals = {"main": {"runs": 100, "failures": 3}}
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("**Flaky rate:** 3.0% ❌", report)
        self.assertIn("- `main`: 3.0% ❌", report)

    def test_build_report_zero_run_branch_shows_no_data(self):
        """build_report renders 'no data' for a monitored branch with no runs."""
        buckets = [{"runs": 5, "failures": 0}, {"runs": 5, "failures": 0}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Tests", "build")]["runs"] = 10
        stats[("Tests", "build")]["buckets"] = buckets
        branch_totals = {
            "main":  {"runs": 10, "failures": 0},
            "squid": {"runs": 0,  "failures": 0},
        }
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("- `main`: 0.0% ✅", report)
        self.assertIn("- `squid`: no data", report)
        self.assertNotIn("- `squid`: 0.0%", report)

    def test_build_report_trend_suppressed_when_sparse(self):
        """build_report shows — for trend when runs < num_buckets * 2."""
        buckets = [{"runs": 1, "failures": 1}, {"runs": 0, "failures": 0},
                   {"runs": 0, "failures": 0}, {"runs": 1, "failures": 0}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Nightly", "deploy")]["runs"] = 2
        stats[("Nightly", "deploy")]["failures"] = 1
        stats[("Nightly", "deploy")]["buckets"] = buckets
        branch_totals = {"main": {"runs": 2, "failures": 1}}
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, branch_totals, 30, 5, now)
        self.assertIn("| Nightly | deploy | 2 | 1 | 50.0% | — |", report)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_skipped_and_cancelled_not_counted(self, mock_runs, mock_jobs, mock_comment):
        """skipped and cancelled conclusions are excluded from run and failure counts."""
        mock_runs.return_value = [{"id": 1, "name": "Tests", "created_at": "2026-01-15T00:00:00Z", "event": "push"}]
        mock_jobs.return_value = [
            {"name": "build", "conclusion": "success"},
            {"name": "build", "conclusion": "failure"},
            {"name": "build", "conclusion": "skipped"},
            {"name": "build", "conclusion": "cancelled"},
        ]
        env = {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1",
               "LOOKBACK_DAYS": "30", "TOP_JOBS": "5", "REPORT_BRANCHES": "main"}
        with patch.dict("os.environ", env):
            main()
        report = mock_comment.call_args[0][3]
        self.assertIn("| Tests | build | 2 | 1 |", report)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_pull_request_runs_not_counted(self, mock_runs, mock_jobs, mock_comment):
        """runs from events other than push/schedule/workflow_dispatch are excluded."""
        mock_runs.return_value = [
            {"id": 1, "name": "Tests", "created_at": "2026-01-15T00:00:00Z", "event": "push"},
            {"id": 2, "name": "Tests", "created_at": "2026-01-15T00:00:00Z", "event": "pull_request"},
        ]
        mock_jobs.return_value = [{"name": "build", "conclusion": "failure"}]
        env = {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1",
               "LOOKBACK_DAYS": "30", "TOP_JOBS": "5", "REPORT_BRANCHES": "main"}
        with patch.dict("os.environ", env):
            main()
        report = mock_comment.call_args[0][3]
        # Only the push run's single job is counted
        self.assertIn("| Tests | build | 1 | 1 |", report)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_runs_fetched_per_monitored_branch(self, mock_runs, mock_jobs, mock_comment):
        """main() queries each branch in REPORT_BRANCHES separately."""
        mock_runs.return_value = [{"id": 1, "name": "Tests", "created_at": "2026-01-15T00:00:00Z", "event": "push"}]
        mock_jobs.return_value = [{"name": "build", "conclusion": "success"}]
        env = {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1",
               "LOOKBACK_DAYS": "30", "TOP_JOBS": "5", "REPORT_BRANCHES": "main, squid"}
        with patch.dict("os.environ", env):
            main()
        branches_queried = [call.args[3] for call in mock_runs.call_args_list]
        self.assertEqual(branches_queried, ["main", "squid"])

    @patch("ci_health_report.gh_get")
    def test_get_runs_filters_by_branch(self, mock_gh_get):
        """get_runs passes the branch as a query parameter."""
        mock_gh_get.return_value = {"workflow_runs": []}
        ci_health_report.get_runs("tok", "o/r", "2026-01-01T00:00:00Z", "squid")
        path = mock_gh_get.call_args[0][1]
        self.assertIn("branch=squid", path)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_failures_attributed_to_correct_branch(self, mock_runs, mock_jobs, mock_comment):
        """per-branch totals reflect the branch each run was fetched from."""
        def runs_for(token, repo, since, branch):
            run_id = {"main": 1, "squid": 2}[branch]
            return [{"id": run_id, "name": "Tests", "created_at": "2026-01-15T00:00:00Z", "event": "push"}]

        def jobs_for(token, repo, run_id):
            return {
                1: [{"name": "build", "conclusion": "success"}],
                2: [{"name": "build", "conclusion": "failure"}],
            }[run_id]

        mock_runs.side_effect = runs_for
        mock_jobs.side_effect = jobs_for
        env = {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1",
               "LOOKBACK_DAYS": "30", "TOP_JOBS": "5", "REPORT_BRANCHES": "main,squid"}
        with patch.dict("os.environ", env):
            main()
        report = mock_comment.call_args[0][3]
        self.assertIn("- `main`: 0.0% ✅ (0 failures / 1 job run)", report)
        self.assertIn("- `squid`: 100.0% ❌ (1 failure / 1 job run)", report)

    def test_whitespace_only_branches_exits(self):
        """a REPORT_BRANCHES value with no branch names is a configuration error."""
        env = {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1",
               "LOOKBACK_DAYS": "30", "TOP_JOBS": "5", "REPORT_BRANCHES": " , "}
        with patch.dict("os.environ", env):
            with self.assertRaises(SystemExit) as ctx:
                main()
        self.assertEqual(ctx.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
