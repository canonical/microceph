#!/usr/bin/env python3
"""
Generates a sample CI health report with synthetic data and writes it to a file.
Usage: python3 simulate_report.py [output.md]
"""

import sys
from collections import defaultdict
from datetime import datetime, timezone

from ci_health_report import build_report

# Each entry: (workflow, job, buckets)
# Buckets run oldest → newest; each is {runs, failures}.
SCENARIOS = [
    # Clearly getting worse — failure rate climbing week over week
    ("Tests", "lint",        [{"runs": 20, "failures": 1},
                              {"runs": 20, "failures": 3},
                              {"runs": 20, "failures": 8},
                              {"runs": 20, "failures": 14}]),
    # Clearly improving — failure rate falling
    ("Tests", "unit",        [{"runs": 20, "failures": 12},
                              {"runs": 20, "failures": 8},
                              {"runs": 20, "failures": 3},
                              {"runs": 20, "failures": 1}]),
    # Flat / stable low failure rate
    ("Tests", "build",       [{"runs": 20, "failures": 2},
                              {"runs": 20, "failures": 2},
                              {"runs": 20, "failures": 2},
                              {"runs": 20, "failures": 2}]),
    # Flat / stable high failure rate
    ("Integration", "smoke", [{"runs": 20, "failures": 14},
                              {"runs": 20, "failures": 15},
                              {"runs": 20, "failures": 13},
                              {"runs": 20, "failures": 14}]),
    # Spike in the middle, now recovering
    ("Integration", "full",  [{"runs": 20, "failures": 2},
                              {"runs": 20, "failures": 18},
                              {"runs": 20, "failures": 18},
                              {"runs": 20, "failures": 3}]),
    # Sparse — only 2 runs total, should show —
    ("Nightly", "deploy",    [{"runs": 1,  "failures": 1},
                              {"runs": 0,  "failures": 0},
                              {"runs": 0,  "failures": 0},
                              {"runs": 1,  "failures": 0}]),
]

stats = defaultdict(lambda: {"runs": 0, "failures": 0, "buckets": []})
for workflow, job, buckets in SCENARIOS:
    key = (workflow, job)
    stats[key]["runs"]     = sum(b["runs"]     for b in buckets)
    stats[key]["failures"] = sum(b["failures"] for b in buckets)
    stats[key]["buckets"]  = buckets

now = datetime(2026, 4, 7, 9, 0, 0, tzinfo=timezone.utc)
report = build_report(stats, lookback_days=30, top_n=5, now=now)

output = sys.argv[1] if len(sys.argv) > 1 else "sample_report.md"
with open(output, "w") as f:
    f.write(report + "\n")

print(f"Written to {output}")
print()
print(report)
