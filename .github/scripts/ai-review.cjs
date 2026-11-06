'use strict';

const REVIEW_ACTIONS = ['opened', 'reopened', 'ready_for_review', 'synchronize'];
const REQUEST_MARKER = '<!-- microceph-ai-review-approval-request -->';

// GitHub supplies event identifiers and commit hashes; only comment text is parsed.
async function readReviewContext({ github, context }) {
  const event = context.payload;
  const automatic = context.eventName === 'pull_request_target';
  let number;

  if (automatic) {
    if (![...REVIEW_ACTIONS, 'closed', 'converted_to_draft'].includes(event.action)) return;
    number = event.pull_request.number;
  } else {
    if (context.eventName !== 'issue_comment' || event.action !== 'created'
        || !event.issue.pull_request || event.comment.user.type !== 'User'
        || event.comment.user.login !== context.actor) return;
    if (!/^\s*\/ai-review\s*$/.test(event.comment.body)) return;
    number = event.issue.number;

    // A deleted or edited command is not a pending approval.
    const { data: comment } = await github.rest.issues.getComment({
      ...context.repo, comment_id: event.comment.id,
    });
    if (comment.body !== event.comment.body || comment.user.login !== context.actor
        || comment.created_at !== comment.updated_at) return;
  }

  const { data: pull } = await github.rest.pulls.get({
    ...context.repo, pull_number: number,
  });
  if (automatic && (pull.head.sha !== event.pull_request.head.sha
      || pull.state !== event.pull_request.state
      || pull.draft !== event.pull_request.draft)) return;
  return { pull, automatic };
}

async function hasWriteAccess({ github, context }) {
  const { data } = await github.rest.repos.getCollaboratorPermissionLevel({
    ...context.repo, username: context.actor,
  });
  return ['admin', 'maintain', 'write'].includes(data.permission)
    || data.user?.permissions?.push === true;
}

async function prepareReview(api) {
  const { core } = api;
  core.setOutput('ready', 'false');
  const review = await readReviewContext(api);
  if (!review) return;
  const { pull, automatic } = review;
  core.setOutput('number', String(pull.number));

  // Current PR transitions cancel obsolete runs even without authorization.
  if (automatic) core.setOutput('cancel', 'true');
  if (pull.state !== 'open' || pull.draft) return;
  if (automatic && !REVIEW_ACTIONS.includes(api.context.payload.action)) return;
  if (!await hasWriteAccess(api)) return;

  core.setOutput('head_sha', pull.head.sha);
  core.setOutput('base_sha', pull.base.sha);
  core.setOutput('ready', 'true');
  core.setOutput('cancel', 'true');
}

async function cancelSupersededReviews({ github, context, core, number }) {
  const { data: currentRun } = await github.rest.actions.getWorkflowRun({
    ...context.repo, run_id: context.runId,
  });
  const title = `AI advisory review PR #${number}`;
  const runs = await github.paginate(github.rest.actions.listWorkflowRuns, {
    ...context.repo, workflow_id: currentRun.workflow_id, per_page: 100,
  });

  for (const run of runs) {
    if (run.display_title !== title || run.run_number >= currentRun.run_number
        || run.status === 'completed') continue;

    try {
      await github.rest.actions.cancelWorkflowRun({
        ...context.repo, run_id: run.id,
      });
      core.info(`Cancelled superseded review run ${run.id}.`);
    } catch (error) {
      if (error.status !== 409) throw error;
      // A run can finish after listing it but before the cancellation request.
      const { data: latest } = await github.rest.actions.getWorkflowRun({
        ...context.repo, run_id: run.id,
      });
      if (latest.status !== 'completed') throw error;
    }
  }
}

async function requestReview(api) {
  const { github, context } = api;
  if (context.eventName !== 'pull_request_target'
      || !REVIEW_ACTIONS.includes(context.payload.action)) return;
  const review = await readReviewContext(api);
  if (!review || review.pull.state !== 'open' || review.pull.draft) return;
  if (await hasWriteAccess(api)) return;

  const body = `${REQUEST_MARKER}
AI review requires approval from a maintainer with repository write access.

Post a new comment containing exactly:

\`\`\`
/ai-review
\`\`\`

This authorizes one review of the current head when the authorization job runs. It does not authorize future pushes.
`;
  const comments = await github.paginate(github.rest.issues.listComments, {
    ...context.repo, issue_number: review.pull.number, per_page: 100,
  });
  const existing = comments.find((comment) => (
    comment.user.login === 'github-actions[bot]' && comment.user.type === 'Bot'
    && comment.body?.startsWith(`${REQUEST_MARKER}\n`)
  ));
  if (!existing) {
    await github.rest.issues.createComment({
      ...context.repo, issue_number: review.pull.number, body,
    });
  } else if (existing.body !== body) {
    await github.rest.issues.updateComment({
      ...context.repo, comment_id: existing.id, body,
    });
  }
}

module.exports = { prepareReview, cancelSupersededReviews, requestReview };
