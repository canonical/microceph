# AI PR reviews

- GitHub Agentic Workflows + Pi + OpenRouter; read-only analysis, advisory comments.
- Automatic: triggering actor has write, maintain, or admin access.
- Otherwise: maintainer posts `/ai-review`; current revision selected when the job runs.
- Approval requests reuse only the marked `github-actions[bot]` comment: create once, leave the current message unchanged, or update outdated instructions. Stale, draft, closed, and already-authorized events do not request approval.
- Head/base and open/non-draft checks run before inference and publication; both review outputs are pinned to the authorized head.
- A push after the publication check can still leave a review on the older commit, but findings are never attributed to the newer, unreviewed commit.
- Current PR transitions and authorized commands cancel only lower-run-number runs of this workflow for the same PR, including pending activation and publication jobs; only the trusted cancellation job receives `actions: write`. Cancellation lists `queued`, `requested`, `waiting`, `pending`, and `in_progress` runs separately, not completed history, and deduplicates runs that move between listings.

## Edit

| File | Settings |
| --- | --- |
| [workflows/ai-review.md](workflows/ai-review.md) | Prompt, triggers, `on.roles`, run name, provider, tools, limits, revision checks and pins. |
| [scripts/ai-review.cjs](scripts/ai-review.cjs) | `hasWriteAccess()`, event checks, `/ai-review`, cancellation, approval-request text. |
| [workflows/ai-review-request.yml](workflows/ai-review-request.yml) | Approval-request triggers and permissions. |

Keep `on.roles` and `hasWriteAccess()` aligned. The `run-name` format must match the title used by `cancelSupersededReviews()`, including for comment-triggered runs.

Generated; do not edit: `workflows/ai-review.lock.yml`, `aw/actions-lock.json`.

## Settings

Target repository → **Settings → Secrets and variables → Actions**.

| Setting | Location / value |
| --- | --- |
| `OPENROUTER_API_KEY` | Repository secret; dedicated inference key. |
| `AI_REVIEW_MODEL` | Repository variable; `z-ai/glm-5.3-flash`. |
| `model-provider` | Workflow: `openai`; endpoint: `https://openrouter.ai/api/v1`. |
| `timeout-minutes` / `max-turns` | Workflow: `15` / `40`. |
| `max-ai-credits` | Workflow: `200` = USD 2 estimated per run. |
| Monthly budget | OpenRouter key: USD 10, monthly reset; configured separately. |

## Check and compile — gh-aw v1.0.1

From the repository root; commit regenerated files with source changes:

```bash
node --test .github/scripts/ai-review.test.cjs
gh aw compile ai-review --validate --shellcheck --no-check-update
```
