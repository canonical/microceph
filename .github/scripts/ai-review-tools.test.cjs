'use strict';

const assert = require('node:assert/strict');
const { execFileSync, spawnSync } = require('node:child_process');
const { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const test = require('node:test');

// Use the executable lock configuration, not its descriptive tool manifest.
const lock = readFileSync(path.join(__dirname, '../workflows/ai-review.lock.yml'), 'utf8');
const match = lock.match(/^([ \t]+)"github": (\{[\s\S]*?^\1\}),?$/m);
assert.ok(match, 'compiled GitHub MCP server configuration is missing');
const server = JSON.parse(match[2]
  .replace(/\$\{GH_AW_GUARD_[A-Z_]+\}/g, '[]')
  .replace(/,\s*([}\]])/g, '$1'));
const configMatch = lock.match(/^\s+GH_AW_PI_CONFIG: '(.*)'$/m);
assert.ok(configMatch, 'compiled Pi runtime configuration is missing');
const config = JSON.parse(configMatch[1]);

// Dependencies are prepared online once; this test neither installs nor downloads.
const actionsRoot = process.env.AI_REVIEW_ACTIONS_ROOT;
const piRoot = process.env.GH_AW_PI_PACKAGE_ROOT;
assert.ok(actionsRoot && piRoot, 'set AI_REVIEW_ACTIONS_ROOT and GH_AW_PI_PACKAGE_ROOT; see AI-REVIEW-README.md');
const actionPin = lock.match(/uses: github\/gh-aw-actions\/setup@([a-f0-9]{40})/)[1];
assert.equal(execFileSync('git', ['-C', actionsRoot, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), actionPin);
const piVersion = lock.match(/@earendil-works\/pi-coding-agent@([0-9.]+)/)[1];
assert.equal(JSON.parse(readFileSync(path.join(piRoot, 'package.json'), 'utf8')).version, piVersion);
const { transformPiEntry } = require(path.join(actionsRoot, 'setup/js/convert_gateway_config_pi.cjs'));
const { preparePiRuntime } = require(path.join(actionsRoot, 'setup/js/pi_runtime.cjs'));

test('Pi exposes only review tools while retaining guard metadata tools on the backend', { timeout: 30_000 }, async (t) => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'microceph-ai-review-tools-'));
  const name = `microceph-ai-review-tools-${process.pid}`;
  const savedEnv = { ...process.env };
  t.after(() => {
    spawnSync('docker', ['rm', '-f', name], { timeout: 10_000, stdio: 'ignore' });
    for (const key of Object.keys(process.env)) if (!(key in savedEnv)) delete process.env[key];
    Object.assign(process.env, savedEnv);
    rmSync(dir, { recursive: true, force: true });
  });
  process.env.RUNNER_TEMP = dir;
  process.env.PI_CODING_AGENT_DIR = path.join(dir, 'agent');
  process.env.GH_AW_PI_STAGING_DIR = path.join(dir, 'staging');
  process.env.PI_OFFLINE = '1';
  delete process.env.GH_AW_PI_MODEL;
  delete process.env.GH_AW_MODEL_ROUTING;
  const gatewayDir = path.join(dir, 'gh-aw/mcp-config');
  mkdirSync(gatewayDir, { recursive: true });
  const converted = transformPiEntry({ url: 'http://localhost/mcp/github' }, 'http://localhost');
  writeFileSync(path.join(gatewayDir, 'mcp-servers.json'), JSON.stringify({ mcpServers: { github: converted } }));
  preparePiRuntime(config);
  const mcpPath = path.join(process.env.PI_CODING_AGENT_DIR, 'mcp.json');
  const prepared = JSON.parse(readFileSync(mcpPath, 'utf8'));

  // Connect Pi to the real backend over stdio instead of the HTTP gateway.
  // Preserve the converter/runtime exposure policy; only replace the transport.
  // The backend remains broader so the gateway can make internal metadata calls.
  const env = { ...server.env, GITHUB_HOST: 'https://github.com', GITHUB_PERSONAL_ACCESS_TOKEN: 'offline-tool-catalog-test' };
  const args = ['run', '--rm', '-i', '--pull=never', '--network=none', '--name', name];
  for (const [key, value] of Object.entries(env)) args.push('-e', `${key}=${value}`);
  args.push(...(server.args || []), server.container);
  delete prepared.mcpServers.github.url;
  Object.assign(prepared.mcpServers.github, { type: 'stdio', command: 'docker', args });
  writeFileSync(mcpPath, JSON.stringify(prepared));

  const sdk = await import(pathToFileURL(path.join(piRoot, 'dist/index.js')).href);
  const settingsManager = sdk.SettingsManager.inMemory({ defaultTools: ['codemode', 'tool_search'], defaultProjectTrust: 'never' });
  const resourceLoader = new sdk.DefaultResourceLoader({
    cwd: dir, agentDir: process.env.PI_CODING_AGENT_DIR, settingsManager,
    noSkills: true, noPromptTemplates: true, noExtensions: true,
    extensionFactories: [sdk.createMcpExtension(), sdk.createCodemodeExtension({ mode: 'on' }), sdk.createToolSearchExtension()],
  });
  await resourceLoader.reload();
  const { session } = await sdk.createAgentSession({
    cwd: dir, agentDir: process.env.PI_CODING_AGENT_DIR, resourceLoader, settingsManager,
    sessionManager: sdk.SessionManager.inMemory(),
  });
  t.after(() => session.dispose());
  await session.bindExtensions({});
  // This is a local extension command: it waits for discovery without inference.
  await session.prompt('/mcp');
  const githubTools = (names) => names.filter((tool) => tool.startsWith('mcp__github__')).sort();
  const expected = ['mcp__github__get_file_contents', 'mcp__github__issue_read', 'mcp__github__pull_request_read'];
  assert.deepEqual(githubTools(session.getActiveToolNames()), expected);
  assert.deepEqual(githubTools(session.getCallableToolNames()), expected);
  const metadata = session.getAllTools().find((tool) => tool.name === 'mcp__github__search_repositories');
  assert.ok(metadata, 'the repository guard still needs backend visibility lookups');
  assert.equal(metadata.exposure, 'hidden');
  // Tool search or a resumed selection must not activate a hidden tool.
  session.setActiveToolsByName([...session.getActiveToolNames(), metadata.name]);
  assert.deepEqual(githubTools(session.getActiveToolNames()), expected);
  assert.deepEqual(githubTools(session.getCallableToolNames()), expected);
});
