#!/usr/bin/env python3
"""
CI Health Report

Queries completed GitHub Actions workflow runs over a lookback window,
calculates per-job failure rates, and posts a summary to a GitHub issue.

Required environment variables:
  GH_TOKEN      - GitHub token with actions:read and issues:write
  GH_REPO       - Repository in "owner/repo" format
  REPORT_ISSUE  - Issue number to post the report to
  LOOKBACK_DAYS - How many days back to look (default: 30)
"""

import os
import sys
import time
from datetime import datetime, timedelta, timezone
from collections import defaultdict
import urllib.request
import urllib.error
import json

COUNTED_CONCLUSIONS = {"success", "failure"}


def _headers(token):
    return {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }


def _urlopen(req):
    """Execute a request with one automatic retry on rate limit (403/429)."""
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.read()
    except urllib.error.HTTPError as e:
        if e.code not in (403, 429):
            raise RuntimeError(f"GitHub API error {e.code} for {req.full_url}") from e
        retry_after = e.headers.get("Retry-After")
        reset = e.headers.get("X-RateLimit-Reset")
        if retry_after:
            wait = int(retry_after) + 5
        elif reset:
            wait = max(0, int(reset) - int(time.time())) + 5
        else:
            wait = 60
        print(f"Rate limited (HTTP {e.code}). Waiting {wait}s before retry...", file=sys.stderr)
        time.sleep(wait)
    # Retry once outside the except block so a second failure is handled cleanly.
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.read()
    except urllib.error.HTTPError as retry_e:
        print(
            f"Error: rate limit persists after retry (HTTP {retry_e.code}). Giving up.",
            file=sys.stderr,
        )
        sys.exit(1)


def gh_get(token, path):
    """Fetch a single page from the GitHub API and return parsed JSON."""
    url = f"https://api.github.com{path}"
    req = urllib.request.Request(url, headers=_headers(token))
    return json.loads(_urlopen(req))


def get_runs(token, repo, since):
    """Return all completed workflow runs created on or after `since`."""
    runs = []
    page = 1
    while True:
        data = gh_get(token, (
            f"/repos/{repo}/actions/runs"
            f"?status=completed&created=>={since}&per_page=100&page={page}"
        ))
        batch = data.get("workflow_runs", [])
        if not batch:
            break
        runs.extend(batch)
        page += 1
    return runs


def get_jobs(token, repo, run_id):
    """Return all jobs for a workflow run."""
    jobs = []
    page = 1
    while True:
        data = gh_get(token, (
            f"/repos/{repo}/actions/runs/{run_id}/jobs"
            f"?per_page=100&page={page}"
        ))
        batch = data.get("jobs", [])
        if not batch:
            break
        jobs.extend(batch)
        page += 1
    return jobs


def post_comment(token, repo, issue_number, body):
    """Post a comment to a GitHub issue."""
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    payload = json.dumps({"body": body}).encode()
    req = urllib.request.Request(url, data=payload, headers={
        **_headers(token),
        "Content-Type": "application/json",
    })
    _urlopen(req)


def build_report(stats, lookback_days, top_n, now):
    """Build the markdown report string from aggregated job stats."""
    # Sort by failure rate descending for the main table
    rows = sorted(stats.items(), key=lambda x: x[1]["failures"] / x[1]["runs"], reverse=True)

    table_lines = []
    for (workflow, job), s in rows:
        rate = s["failures"] / s["runs"] * 100
        table_lines.append(f"| {workflow} | {job} | {s['runs']} | {s['failures']} | {rate:.1f}% |")

    # Top N by absolute failure count
    top = sorted(stats.items(), key=lambda x: x[1]["failures"], reverse=True)[:top_n]
    top_lines = []
    for rank, ((workflow, job), s) in enumerate(top, start=1):
        rate = s["failures"] / s["runs"] * 100
        top_lines.append(f"{rank}. **{workflow} / {job}** — {s['failures']} failures ({rate:.1f}%)")

    total_runs = sum(s["runs"] for s in stats.values())
    total_failures = sum(s["failures"] for s in stats.values())
    overall_rate = (total_failures / total_runs * 100) if total_runs else 0.0

    timestamp = now.strftime("%Y-%m-%dT%H:%M:%SZ")

    lines = [
        "## CI Health Report",
        "",
        f"_Last {lookback_days} days — generated {timestamp}_",
        "",
        "### Job Failure Rates",
        "",
        "| Workflow | Job | Runs | Failures | Rate |",
        "|----------|-----|------|----------|------|",
        *table_lines,
        "",
        f"### Top {top_n} Most Failing Jobs",
        "",
        *top_lines,
        "",
        "### Summary",
        "",
        f"- **Total job runs:** {total_runs}",
        f"- **Total failures:** {total_failures}",
        f"- **Overall failure rate:** {overall_rate:.1f}%",
    ]
    return "\n".join(lines)


def main():
    token = os.environ.get("GH_TOKEN", "")
    repo = os.environ.get("GH_REPO", "")
    issue_number = os.environ.get("REPORT_ISSUE", "")
    lookback_days_str = os.environ.get("LOOKBACK_DAYS", "")
    top_jobs_str = os.environ.get("TOP_JOBS", "")

    if not token or not repo or not issue_number or not lookback_days_str or not top_jobs_str:
        print("Error: GH_TOKEN, GH_REPO, REPORT_ISSUE, LOOKBACK_DAYS, and TOP_JOBS must all be set.", file=sys.stderr)
        sys.exit(1)

    lookback_days = int(lookback_days_str)
    top_jobs = int(top_jobs_str)

    now = datetime.now(timezone.utc)
    since = (now - timedelta(days=lookback_days)).strftime("%Y-%m-%dT%H:%M:%SZ")

    print(f"Fetching workflow runs since {since}...")
    runs = get_runs(token, repo, since)
    print(f"Found {len(runs)} completed runs.")

    if not runs:
        print("No runs found. Skipping report.")
        return

    # Aggregate: (workflow_name, job_name) -> {runs, failures}
    # Only "success" and "failure" conclusions are counted; skipped/cancelled are excluded.
    stats = defaultdict(lambda: {"runs": 0, "failures": 0})

    for i, run in enumerate(runs, start=1):
        print(f"  Fetching jobs for run {i}/{len(runs)} (id={run['id']})...")
        jobs = get_jobs(token, repo, run["id"])
        for job in jobs:
            conclusion = job.get("conclusion")
            if conclusion not in COUNTED_CONCLUSIONS:
                continue
            key = (run["name"], job["name"])
            stats[key]["runs"] += 1
            if conclusion == "failure":
                stats[key]["failures"] += 1

    if not stats:
        print("No job data collected. Skipping report.")
        return

    report = build_report(stats, lookback_days, top_jobs, now)

    # Write to GitHub step summary if available
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        with open(summary_path, "a") as f:
            f.write(report + "\n")

    print(f"Posting report to issue #{issue_number}...")
    post_comment(token, repo, issue_number, report)
    print("Report generated successfully.")

## TESTS ##

import unittest
from unittest.mock import MagicMock, patch


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

    def test_build_report_structure_and_totals(self):
        """build_report produces a markdown table and correct summary totals."""
        stats = defaultdict(lambda: {"runs": 0, "failures": 0})
        stats[("Tests", "build")]["runs"] = 10
        stats[("Tests", "build")]["failures"] = 3
        now = datetime(2026, 1, 1, 9, 0, 0, tzinfo=timezone.utc)
        report = build_report(stats, 30, 5, now)
        self.assertIn("| Workflow | Job | Runs | Failures | Rate |", report)
        self.assertIn("| Tests | build | 10 | 3 | 30.0% |", report)
        self.assertIn("**Total job runs:** 10", report)
        self.assertIn("**Total failures:** 3", report)
        self.assertIn("**Overall failure rate:** 30.0%", report)

    @patch("ci_health_report.post_comment")
    @patch("ci_health_report.get_jobs")
    @patch("ci_health_report.get_runs")
    def test_skipped_and_cancelled_not_counted(self, mock_runs, mock_jobs, mock_comment):
        """skipped and cancelled conclusions are excluded from run and failure counts."""
        mock_runs.return_value = [{"id": 1, "name": "Tests"}]
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
    main()
