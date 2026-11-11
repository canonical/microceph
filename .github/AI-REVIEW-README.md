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

Pi exposes only `get_file_contents`, `pull_request_read`, and `issue_read` from GitHub. Keep `tools.github.allowed` and `engine.config.mcp.toolExposure.github` in sync: the pinned converter drops the former, so Pi's native exposure policy hides tools by default and makes only these three callable. Safe-output tools remain directly callable. Keep the backend toolsets intact: the repository guard internally needs tools such as `search_repositories` to classify private data. Removing those tools from the server can break secrecy enforcement even though the agent never needs to call them. Read-only mode and repository guards remain enabled.

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

## Tests

Run the lightweight regression suite from the repository root with Node.js; no npm install, Docker, Pi, network access, or credentials are required:

```bash
node --test .github/scripts/ai-review.test.cjs
```

The `ai-review-tests` job in [workflows/checks.yml](workflows/checks.yml) runs both suites on pushes and pull requests, independently of the Ceph jobs. Its comments explain each setup and test step. The Node unit tests use an in-memory GitHub API to check authorization, approval-request creation/deduplication/updates, and cancellation boundaries; they do not post comments or cancel real workflow runs.

### Offline Pi tool-catalog regression

This separate test addresses the runtime allowlist finding in https://github.com/canonical/microceph/pull/953#pullrequestreview-5846340130. A configuration-text assertion cannot detect the pinned converter dropping the declared allowlist, so the test runs the real converter, Pi runtime setup, and Pi SDK against the real GitHub MCP server.

CI prepares the dependencies automatically. To run this suite locally, use Node.js 24 and Docker, then prepare the pinned dependencies once while online, outside the repository:

```bash
GITHUB_MCP_IMAGE=ghcr.io/github/github-mcp-server:v2.0.3@sha256:250bc6ce70788207608ef74cef4397230ff2e8481218da74d6093e4bd9101132
docker pull "$GITHUB_MCP_IMAGE"
docker tag "$GITHUB_MCP_IMAGE" ghcr.io/github/github-mcp-server:v2.0.3
git clone --no-checkout https://github.com/github/gh-aw-actions.git /tmp/ai-review-actions
git -C /tmp/ai-review-actions checkout --detach 221ba4f4319de6b8e809d1a15528b3ae6a818aa5
npm install --prefix /tmp/ai-review-pi --ignore-scripts --no-audit --no-fund @earendil-works/pi-coding-agent@1.4.0
```

Then run the regression without downloading dependencies:

```bash
AI_REVIEW_ACTIONS_ROOT=/tmp/ai-review-actions \
GH_AW_PI_PACKAGE_ROOT=/tmp/ai-review-pi/node_modules/@earendil-works/pi-coding-agent \
node --test .github/scripts/ai-review-tools.test.cjs
```

The test reads the executable configuration from `ai-review.lock.yml` and checks the gh-aw action commit and Pi version against it. It replaces the gateway's HTTP transport with a local stdio connection but preserves Pi's exposure policy. The server container uses `--pull=never`, `--network=none`, and a dummy token; Pi's local `/mcp` command discovers tools without model inference or GitHub API requests.

It asserts that only `get_file_contents`, `pull_request_read`, and `issue_read` are active and callable. It also checks that `search_repositories` remains registered for guard metadata lookups, stays hidden, and cannot be activated explicitly. This tests the real tool catalog, not the gateway's repository-scope enforcement, model behavior, or review publication.

When upgrading gh-aw, Pi, or the server, regenerate the workflow lock, update the pinned test dependencies in `checks.yml` and these instructions, and rerun both suites.

## Compile — gh-aw v1.0.1

Only needed when changing the agentic workflow source, not when running tests. Commit regenerated files with source changes:

```bash
gh aw compile ai-review --validate --shellcheck --no-check-update
```
