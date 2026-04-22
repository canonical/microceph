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


def bucket_count(lookback_days):
    """Return the number of trend buckets for a given lookback window.

    Uses natural time units so bucket boundaries are semantically meaningful:
      - daily  for windows up to 14 days
      - weekly for windows up to 90 days
      - ~monthly (28-day) for longer windows
    """
    if lookback_days <= 14:
        return lookback_days
    elif lookback_days <= 90:
        return lookback_days // 7
    else:
        return lookback_days // 28


def trend_indicator(buckets):
    """Compare first-half vs second-half failure rate and return an arrow + delta string."""
    mid = len(buckets) // 2
    early, recent = buckets[:mid], buckets[mid:]
    e_runs  = sum(b["runs"]     for b in early)
    e_fails = sum(b["failures"] for b in early)
    r_runs  = sum(b["runs"]     for b in recent)
    r_fails = sum(b["failures"] for b in recent)
    if e_runs == 0 or r_runs == 0:
        return "—"
    delta = (r_fails / r_runs - e_fails / e_runs) * 100
    if abs(delta) < 1.0:
        return f"→ {delta:+.1f}%"
    return f"{'↑' if delta > 0 else '↓'} {delta:+.1f}%"


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
            wait = max(0, int(reset) - int(time.time())) + 5 # Seconds until reset + 5
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
        min_runs = len(s["buckets"]) * 2
        trend = trend_indicator(s["buckets"]) if s["runs"] >= min_runs else "—"
        table_lines.append(f"| {workflow} | {job} | {s['runs']} | {s['failures']} | {rate:.1f}% | {trend} |")

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
        "| Workflow | Job | Runs | Failures | Rate | Trend |",
        "|----------|-----|------|----------|------|-------|",
        *table_lines,
        "",
        f"_Trend: the {lookback_days}-day window is divided into equal time buckets"
        " (daily for ≤ 14 days, weekly for ≤ 90 days, ~monthly beyond that)."
        " The failure rate in the first half of those buckets is compared to the second half:"
        " ↑ = getting worse, ↓ = improving, → = stable (< 1 pp change)."
        " — = fewer than 2 runs per bucket on average; not enough data._",
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
    since_dt = now - timedelta(days=lookback_days)
    since = since_dt.strftime("%Y-%m-%dT%H:%M:%SZ")
    num_buckets = bucket_count(lookback_days)

    print(f"Fetching workflow runs since {since}...")
    runs = get_runs(token, repo, since)
    print(f"Found {len(runs)} completed runs.")

    if not runs:
        print("No runs found. Skipping report.")
        return

    # Aggregate: (workflow_name, job_name) -> {runs, failures, buckets}
    # Only "success" and "failure" conclusions are counted; skipped/cancelled are excluded.
    # Buckets divide the lookback window into equal time slices (oldest → newest) for trend tracking.
    stats = defaultdict(lambda: {
        "runs": 0,
        "failures": 0,
        "buckets": [{"runs": 0, "failures": 0} for _ in range(num_buckets)],
    })
    window_secs = (now - since_dt).total_seconds()

    for i, run in enumerate(runs, start=1):
        print(f"  Fetching jobs for run {i}/{len(runs)} (id={run['id']})...")
        run_dt = datetime.fromisoformat(run["created_at"].replace("Z", "+00:00"))
        elapsed = (run_dt - since_dt).total_seconds()  # seconds from window start to this run
        # clamp: elapsed==window_secs would produce index num_buckets
        bucket_idx = min(int(elapsed / window_secs * num_buckets), num_buckets - 1)
        bucket_idx = max(0, bucket_idx)  # clamp: clock skew can make elapsed slightly negative

        jobs = get_jobs(token, repo, run["id"])
        for job in jobs:
            conclusion = job.get("conclusion")
            if conclusion not in COUNTED_CONCLUSIONS:
                continue
            key = (run["name"], job["name"])
            stats[key]["runs"] += 1
            stats[key]["buckets"][bucket_idx]["runs"] += 1
            if conclusion == "failure":
                stats[key]["failures"] += 1
                stats[key]["buckets"][bucket_idx]["failures"] += 1

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


if __name__ == "__main__":
    main()
