'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');

const { prepareReview, cancelSupersededReviews } = require('./ai-review.cjs');

const repo = { owner: 'example', repo: 'microceph' };
const head = 'a'.repeat(40);
const base = 'b'.repeat(40);
const pull = { number: 17, state: 'open', draft: false, head: { sha: head }, base: { sha: base } };
const comment = {
  id: 91,
  body: '/ai-review',
  user: { type: 'User', login: 'maintainer' },
  created_at: '2026-11-06T00:00:00Z',
  updated_at: '2026-11-06T00:00:00Z',
};

function reviewAPI({ eventName = 'issue_comment', action = 'created', permission = 'write',
  currentPull = pull, currentComment = comment, eventComment = comment, eventPull = pull } = {}) {
  const outputs = {};
  return {
    outputs,
    core: { setOutput: (name, value) => { outputs[name] = value; } },
    context: {
      repo,
      actor: 'maintainer',
      eventName,
      payload: { action, comment: eventComment, issue: { number: 17, pull_request: {} }, pull_request: eventPull },
    },
    github: { rest: {
      pulls: { get: async () => ({ data: currentPull }) },
      issues: { getComment: async () => ({ data: currentComment }) },
      repos: { getCollaboratorPermissionLevel: async () => ({ data: { permission } }) },
    } },
  };
}

test('a maintainer command pins the revision selected when authorization runs', async () => {
  const currentPull = { ...pull, head: { sha: 'c'.repeat(40) } };
  const api = reviewAPI({ currentPull });
  await prepareReview(api);
  assert.deepEqual(api.outputs, {
    ready: 'true', number: '17', head_sha: currentPull.head.sha, base_sha: base, cancel: 'true',
  });
});

for (const [name, options] of [
  ['a reader command', { permission: 'read' }],
  ['an ordinary maintainer comment', { eventComment: { ...comment, body: 'Looks good' } }],
  ['an edited command', { currentComment: { ...comment, updated_at: '2026-11-06T00:01:00Z' } }],
]) {
  test(`${name} cannot authorize inference or cancel another review`, async () => {
    const api = reviewAPI(options);
    await prepareReview(api);
    assert.equal(api.outputs.ready, 'false');
    assert.notEqual(api.outputs.cancel, 'true');
  });
}

for (const [action, currentPull] of [
  ['synchronize', pull],
  ['closed', { ...pull, state: 'closed' }],
  ['converted_to_draft', { ...pull, draft: true }],
]) {
  test(`a current ${action} event can cancel older runs without write access`, async () => {
    const api = reviewAPI({ eventName: 'pull_request_target', action, permission: 'read', currentPull, eventPull: currentPull });
    await prepareReview(api);
    assert.equal(api.outputs.cancel, 'true');
    assert.equal(api.outputs.number, '17');
    assert.equal(api.outputs.ready, 'false');
  });
}

test('a superseded push cannot cancel a review of the new head', async () => {
  const api = reviewAPI({
    eventName: 'pull_request_target', action: 'synchronize', currentPull: { ...pull, head: { sha: 'c'.repeat(40) } },
  });
  await prepareReview(api);
  assert.equal(api.outputs.ready, 'false');
  assert.notEqual(api.outputs.cancel, 'true');
});

function workflowRun(id, number, status = 'in_progress') {
  return { id, run_number: id, workflow_id: 8, display_title: `AI advisory review PR #${number}`, status };
}

test('a delayed older run cannot cancel the newer run that superseded it', async () => {
  const runs = [
    workflowRun(10, 17),
    workflowRun(11, 17, 'queued'),
    workflowRun(12, 18),
    workflowRun(13, 17, 'completed'),
    workflowRun(20, 17),
    workflowRun(21, 17),
  ];
  const cancelled = [];
  const github = {
    paginate: async () => runs,
    rest: { actions: {
      listWorkflowRuns: () => {},
      getWorkflowRun: async ({ run_id }) => ({ data: runs.find((run) => run.id === run_id) }),
      cancelWorkflowRun: async ({ run_id }) => {
        cancelled.push(run_id);
        runs.find((run) => run.id === run_id).status = 'completed';
      },
    } },
  };
  const api = { github, core: { info: () => {} }, number: 17 };

  await cancelSupersededReviews({ ...api, context: { repo, runId: 20 } });
  assert.deepEqual(cancelled, [10, 11]);
  // The old cancellation job may still finish after its cancellation was requested.
  await cancelSupersededReviews({ ...api, context: { repo, runId: 10 } });
  assert.deepEqual(cancelled, [10, 11]);
  assert.equal(runs.find((run) => run.id === 20).status, 'in_progress');
  assert.equal(runs.find((run) => run.id === 21).status, 'in_progress');
});

test('cancellation lists only nonterminal statuses and preserves run boundaries', async () => {
  const statuses = ['queued', 'requested', 'waiting', 'pending', 'in_progress'];
  const currentRun = workflowRun(20, 17);
  const runs = [
    ...statuses.map((status, index) => workflowRun(10 + index, 17, status)),
    workflowRun(15, 17, 'completed'),
    workflowRun(16, 18, 'queued'),
    currentRun,
    workflowRun(21, 17, 'pending'),
    { ...workflowRun(9, 17, 'requested'), workflow_id: 99 },
  ];
  const listed = [];
  const cancelled = [];
  const actions = {
    listWorkflowRuns: () => {},
    getWorkflowRun: async ({ run_id }) => {
      assert.equal(run_id, currentRun.id);
      return { data: currentRun };
    },
    cancelWorkflowRun: async ({ run_id }) => { cancelled.push(run_id); },
  };
  await cancelSupersededReviews({
    number: 17,
    context: { repo, runId: currentRun.id },
    core: { info: () => {} },
    github: {
      rest: { actions },
      paginate: async (method, params) => {
        assert.equal(method, actions.listWorkflowRuns);
        assert.ok(statuses.includes(params.status), 'completed history must not be queried');
        assert.deepEqual(params, { ...repo, workflow_id: 8, status: params.status, per_page: 100 });
        listed.push(params.status);
        return runs.filter((run) => run.workflow_id === params.workflow_id && run.status === params.status);
      },
    },
  });
  assert.deepEqual(listed, statuses);
  assert.deepEqual(cancelled, [10, 11, 12, 13, 14]);
});

test('a run moving between status listings is cancelled only once', async () => {
  const olderRun = workflowRun(10, 17, 'queued');
  const currentRun = workflowRun(20, 17);
  const cancelled = [];
  await cancelSupersededReviews({
    number: 17,
    context: { repo, runId: currentRun.id },
    core: { info: () => {} },
    github: {
      paginate: async (_method, { status }) => {
        if (status === 'queued') return [{ ...olderRun }];
        if (status === 'in_progress') return [{ ...olderRun, status: 'in_progress' }];
        return [];
      },
      rest: { actions: {
        listWorkflowRuns: () => {},
        getWorkflowRun: async () => ({ data: currentRun }),
        cancelWorkflowRun: async ({ run_id }) => { cancelled.push(run_id); },
      } },
    },
  });
  assert.deepEqual(cancelled, [olderRun.id]);
});

for (const [name, status, completed] of [
  ['a run completing during cancellation', 409, true],
  ['a conflict on a still-active run', 409, false],
  ['a permission failure', 403, false],
]) {
  test(`cancellation handles ${name}`, async () => {
    const error = Object.assign(new Error('Cancellation failed'), { status });
    const olderRun = workflowRun(10, 17);
    const currentRun = workflowRun(20, 17);
    const cancelled = [];
    const api = {
      number: 17,
      context: { repo, runId: 20 },
      core: { info: () => {} },
      github: {
        paginate: async () => [olderRun, workflowRun(11, 17)],
        rest: { actions: {
          listWorkflowRuns: () => {},
          getWorkflowRun: async ({ run_id }) => ({ data: run_id === 20 ? currentRun : olderRun }),
          cancelWorkflowRun: async ({ run_id }) => {
            if (run_id === olderRun.id) {
              if (completed) olderRun.status = 'completed';
              throw error;
            }
            cancelled.push(run_id);
          },
        } },
      },
    };
    if (completed) {
      await cancelSupersededReviews(api);
      assert.deepEqual(cancelled, [11]);
    } else {
      await assert.rejects(cancelSupersededReviews(api), (actual) => actual === error);
    }
  });
}
