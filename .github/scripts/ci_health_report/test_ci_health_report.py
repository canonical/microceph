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
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, 30, 5, now)
        self.assertIn("| Workflow | Job | Runs | Failures | Rate | Trend |", report)
        self.assertIn("| Tests | build | 10 | 5 | 50.0% | ↑", report)
        self.assertIn("**Total job runs:** 10", report)
        self.assertIn("**Overall failure rate:** 50.0%", report)

    def test_build_report_trend_suppressed_when_sparse(self):
        """build_report shows — for trend when runs < num_buckets * 2."""
        buckets = [{"runs": 1, "failures": 1}, {"runs": 0, "failures": 0},
                   {"runs": 0, "failures": 0}, {"runs": 1, "failures": 0}]
        stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
        stats[("Nightly", "deploy")]["runs"] = 2
        stats[("Nightly", "deploy")]["failures"] = 1
        stats[("Nightly", "deploy")]["buckets"] = buckets
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, 30, 5, now)
        self.assertIn("| Nightly | deploy | 2 | 1 | 50.0% | — |", report)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_skipped_and_cancelled_not_counted(self, mock_runs, mock_jobs, mock_comment):
        """skipped and cancelled conclusions are excluded from run and failure counts."""
        mock_runs.return_value = [{"id": 1, "name": "Tests", "created_at": "2026-01-15T00:00:00Z"}]
        mock_jobs.return_value = [
            {"name": "build", "conclusion": "success"},
            {"name": "build", "conclusion": "failure"},
            {"name": "build", "conclusion": "skipped"},
            {"name": "build", "conclusion": "cancelled"},
        ]
        with patch.dict("os.environ", {"GH_TOKEN": "tok", "GH_REPO": "o/r", "REPORT_ISSUE": "1", "LOOKBACK_DAYS": "30", "TOP_JOBS": "5"}):
            main()
        report = mock_comment.call_args[0][3]
        self.assertIn("| Tests | build | 2 | 1 |", report)


if __name__ == "__main__":
    unittest.main()
