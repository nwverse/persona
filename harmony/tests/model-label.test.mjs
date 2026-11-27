import test from 'node:test';
import assert from 'node:assert/strict';
const { launcherModelLabel, claudeModelName, startEfforts } = await import('../entry/src/main/ets/core/ModelLabel.ts');

test('Claude Code aliases name the version they run, as the list it reports words it', () => {
  // What Claude Code 2.1.330 answers to initialize, as the node passes it on.
  const list = [
    { id: 'default', name: 'Default (recommended)', resolved: 'claude-opus-5-5', default: true, description: 'Use the default model (currently Opus 5.5) · $4/$20 per Mtok' },
    { id: 'opus', name: 'Opus', resolved: 'claude-opus-5-5', description: 'Opus 5.5 · Best for everyday, complex tasks · $4/$20 per Mtok' },
    { id: 'fable', name: 'Fable', resolved: 'claude-fable-5-2', description: 'Fable 5.2 · Most capable for your hardest and longest-running tasks · $8/$40 per Mtok' },
    { id: 'sonnet', name: 'Sonnet', resolved: 'claude-sonnet-5-5', description: 'Sonnet 5.5 · Efficient for routine tasks · $2/$10 per Mtok' },
    { id: 'haiku', name: 'Haiku', resolved: 'claude-haiku-5-5', description: 'Haiku 5.5 · Fastest for quick answers · $0.10/$0.50 per Mtok' },
  ];
  assert.deepEqual(list.map(launcherModelLabel),
    ['Default (recommended) · Opus 5.5', 'Opus 5.5', 'Fable 5.2', 'Sonnet 5.5', 'Haiku 5.5']);
});

test('without a versioned description the resolved Claude ID names it; anything else keeps its own name', () => {
  assert.equal(launcherModelLabel({ id: 'opus', name: 'Opus', resolved: 'claude-opus-5-5' }), 'Opus 5.5');
  assert.equal(launcherModelLabel({ id: 'opus', name: 'Opus', resolved: 'claude-opus-5-5[1m]' }), 'Opus 5.5');
  assert.equal(launcherModelLabel({ id: 'gpt-6.2', name: 'GPT-6.2', description: 'Frontier model' }), 'GPT-6.2');
  assert.equal(launcherModelLabel({ id: 'custom-id' }), 'custom-id');
  assert.equal(launcherModelLabel({ id: 'opus', name: 'Opus', resolved: 'some-gateway-model' }), 'Opus');
  assert.equal(launcherModelLabel({ id: 'mine', name: 'Mine', description: 'Mine but with a very long explanation that is not a version name at all' }), 'Mine');
  assert.equal(launcherModelLabel({ id: 'default', name: 'Default' }), 'Default');
  assert.equal(claudeModelName('claude-sonnet-4-5-20250929'), 'Sonnet 4.5');
  assert.equal(claudeModelName('gpt-6.2'), '');
});

test('the New session picker labels its options and its field this way', async () => {
  const { readFileSync } = await import('node:fs');
  const choices = readFileSync(new URL('../entry/src/main/ets/models/AgentLaunchChoices.ets', import.meta.url), 'utf8');
  assert.ok(choices.includes('launchModelName(id,launcherModelLabel(v))'));
  assert.ok(choices.includes('model ? launcherModelLabel(model) : picked'));
});

test('New session offers the picked model\'s efforts, the default row\'s when none is picked, none for a typed ID', async () => {
  const list = [
    { id: 'default', name: 'Default (recommended)', default: true, efforts: ['low', 'medium', 'high', 'xhigh', 'max'] },
    { id: 'haiku', name: 'Haiku', efforts: ['low', 'high', 'high', ' '] },
    { id: 'gateway', name: 'Gateway' },
  ];
  assert.deepEqual(startEfforts(list, ''), ['low', 'medium', 'high', 'xhigh', 'max']);
  assert.deepEqual(startEfforts(list, 'haiku'), ['low', 'high']);
  assert.deepEqual(startEfforts(list, 'gateway'), []);
  assert.deepEqual(startEfforts(list, 'my-model', true), []);
  assert.deepEqual(startEfforts([{ id: 'gpt-6.2', default: true, efforts: ['minimal', 'low'] }], ''), ['minimal', 'low']);
  const { readFileSync } = await import('node:fs');
  const page = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  assert.ok(page.includes('const effort = this.startAgentEffort();'));
  assert.ok(page.includes('this.agentEffort = this.startAgentEffort();'));
});
