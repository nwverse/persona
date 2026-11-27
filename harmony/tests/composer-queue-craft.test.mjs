import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';

const stubs = new Map([
  ['craft-stub:transport', `
    export class EndpointConfig {
      constructor(baseUrl, hubId, token, pin, managerId = '') {
        Object.assign(this, { baseUrl, hubId, token, pin, managerId });
      }
    }
    export class HttpFailure extends Error {
      constructor(status, code, resultUnknown = false, resultPending = false) {
        super(code || 'request_failed'); this.status = status; this.code = code;
        this.resultUnknown = resultUnknown; this.resultPending = resultPending;
      }
    }
    export class HubTransport {
      constructor(config) { this.config = config; }
      async request() { throw new Error('transport_must_not_be_used'); }
      async requestBytes() { throw new Error('transport_must_not_be_used'); }
      send() { throw new Error('transport_must_not_be_used'); }
      close() {}
    }
  `],
  ['craft-stub:local-store', 'export class LocalStore {}'],
  ['craft-stub:mutation-store', 'export class MutationRecord {} export class MutationStore { static shared() { throw new Error("demo_mutation_store_must_not_be_used"); } }'],
  ['craft-stub:arkts', 'export const util = { generateRandomUUID: () => crypto.randomUUID(), TextDecoder: { create: () => ({ decodeToString: () => "" }) } };'],
  ['craft-stub:client', 'export class ClientModel {}']
]);

registerHooks({
  resolve(specifier, context, next) {
    if (stubs.has(specifier)) return { url: specifier, shortCircuit: true };
    if (specifier === '../platform/HubTransport') return { url: 'craft-stub:transport', shortCircuit: true };
    if (specifier === '../platform/LocalStore') return { url: 'craft-stub:local-store', shortCircuit: true };
    if (specifier === './MutationStore') return { url: 'craft-stub:mutation-store', shortCircuit: true };
    if (specifier === '@kit.ArkTS') return { url: 'craft-stub:arkts', shortCircuit: true };
    if (specifier === './ClientModel' && context.parentURL?.endsWith('/TodoWorkspace.ets')) {
      return { url: 'craft-stub:client', shortCircuit: true };
    }
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.')) {
      return { url: new URL(specifier.endsWith('.ets') ? specifier : specifier + '.ets', context.parentURL).href, shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (stubs.has(url)) return { format: 'module', source: stubs.get(url), shortCircuit: true };
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { extractComposerQueue, formatTurnElapsed } = await import('../entry/src/main/ets/core/ComposerQueue.ts');
const { extractTurnChangesFromRaw, extractWorkStepsFromRaw } = await import('../entry/src/main/ets/core/WorkPresentation.ts');
const { projectToolTurn } = await import('../entry/src/main/ets/core/Domain.ts');
const { parseMarkdown } = await import('../entry/src/main/ets/core/Markdown.ts');
const { DemoRepository } = await import('../entry/src/main/ets/models/DemoRepository.ets');
const { assistantTrayRows, TrayRequest } = await import('../entry/src/main/ets/models/AssistantTrays.ets');

const read = (rel) => readFileSync(new URL('../entry/src/main/ets/' + rel, import.meta.url), 'utf8');
const readResource = (locale) => JSON.parse(readFileSync(new URL('../entry/src/main/resources/' + locale + '/element/string.json', import.meta.url), 'utf8'));

test('extractComposerQueue parses string and object queues, counts own vs relayed, and detects steersQueue', () => {
  assert.deepEqual(extractComposerQueue({}), {
    items: [],
    ownCount: 0,
    relayedCount: 0,
    preview: '',
    steersQueue: false
  });

  const fromStrings = extractComposerQueue({
    queued: ['  Include Mate XT 2\n  tri-fold notes  ', '', 'Double-check zh_CN strings', 'Relayed from Release room'],
    own_queued_count: 2,
    steers_queue: true
  });
  assert.equal(fromStrings.items.length, 3);
  assert.equal(fromStrings.ownCount, 2);
  assert.equal(fromStrings.relayedCount, 1);
  assert.equal(fromStrings.items[0].relayed, false);
  assert.equal(fromStrings.items[1].relayed, false);
  assert.equal(fromStrings.items[2].relayed, true);
  assert.equal(fromStrings.preview, 'Include Mate XT 2 tri-fold notes');
  assert.equal(fromStrings.steersQueue, true);

  const fromObjects = extractComposerQueue({
    queued: [
      { id: 'q-1', body: '  First follow-up  ', relayed: false },
      { id: 'q-2', text: 'Forwarded note', sender: 'Release room' }
    ],
    assistant: true,
    can_send: true
  });
  assert.equal(fromObjects.items.length, 2);
  assert.equal(fromObjects.ownCount, 1);
  assert.equal(fromObjects.relayedCount, 1);
  assert.deepEqual(fromObjects.items[0], { id: 'q-1', text: 'First follow-up', relayed: false, sender: '' });
  assert.deepEqual(fromObjects.items[1], { id: 'q-2', text: 'Forwarded note', relayed: true, sender: 'Release room' });
  assert.equal(fromObjects.preview, 'First follow-up');
  assert.equal(fromObjects.steersQueue, true);
});

test('formatTurnElapsed formats seconds under 60s and Mm SSs at 60s and above', () => {
  assert.equal(formatTurnElapsed(0, 5000), '0s');
  assert.equal(formatTurnElapsed(10000, 10000), '0s');
  assert.equal(formatTurnElapsed(10000, 17400), '7s');
  assert.equal(formatTurnElapsed(10000, 69999), '59s');
  assert.equal(formatTurnElapsed(10000, 70000), '1m 00s');
  assert.equal(formatTurnElapsed(10000, 75000), '1m 05s');
  assert.equal(formatTurnElapsed(10000, 764000), '12m 34s');
});

test('DemoRepository exposes live main counts (Needs You 1, In Progress 1), changelog queue, work steps, code block, interrupt, and dequeue', async () => {
  const repo = new DemoRepository();
  const roster = repo.roster();

  // 1. demo-main status bar counts via assistantTrayRows
  const feed = await repo.call('/api/v1/assistant/conversations/demo-main/feed?paginate=1&manager=demo');
  assert.ok(Array.isArray(feed.task_cards));
  const todosResp = await repo.call('/api/v1/todos?manager=demo');
  const interactionsResp = await repo.call('/api/v1/interactions');
  const requests = interactionsResp.interactions.map((item) => {
    const inner = item.interaction || {};
    return new TrayRequest(item.machine_id, item.session_id, item.id, inner.plan || '', 0, false, false);
  });
  const trayRows = assistantTrayRows(
    todosResp.todos,
    roster.sessions,
    feed.items || [],
    feed.plans || {},
    requests,
    'demo',
    'demo',
    'demo-main',
    'demo-main',
    Date.parse('2026-11-27T09:05:00Z')
  );
  assert.equal(trayRows.filter((row) => row.bucket === 'needs').length, 1);
  assert.equal(trayRows.filter((row) => row.bucket === 'progress').length, 1);

  // 2. demo-agent-changelog queue state
  const changelog = roster.sessions.find((row) => row.id === 'demo-agent-changelog');
  assert.ok(changelog);
  const queue = extractComposerQueue(changelog.data());
  assert.deepEqual(queue.items.map((item) => item.text), [
    'Include Mate XT 2 tri-fold notes',
    'Double-check zh_CN strings'
  ]);
  assert.equal(queue.ownCount, 2);
  assert.equal(queue.steersQueue, true);

  // 3. demo-agent-changelog transcript has work steps, changed files, and fenced TypeScript code block
  const changelogItems = repo.historyItems(changelog);
  const turn = projectToolTurn(changelogItems[0].id, changelogItems, false);
  assert.equal(turn.summaryVisible, true);
  assert.ok(turn.toolCount >= 3);
  assert.ok(turn.thoughtCount >= 1);
  const steps = extractWorkStepsFromRaw(JSON.stringify(changelogItems), false);
  assert.ok(steps.length >= 4);
  const changes = extractTurnChangesFromRaw(JSON.stringify(changelogItems));
  assert.ok(changes && changes.files.length >= 1);
  const blocks = parseMarkdown(turn.visibleAssistantText?.text || '');
  assert.ok(blocks.some((b) => b.kind === 'code' && b.language === 'typescript'));

  // 4. Interrupt transitions demo-agent-changelog to idle/interrupted with updated last_text
  await repo.call('/api/v1/sessions/demo-mac/demo-agent-changelog/interrupt', 'POST', {});
  const afterInterrupt = (await repo.sessions()).find((row) => row.id === 'demo-agent-changelog');
  assert.equal(afterInterrupt.state, 'idle');
  assert.equal(afterInterrupt.data().status, 'interrupted');
  assert.equal(afterInterrupt.data().last_text, 'Interrupted in local demo.');

  // 5. Send now dequeues queued items cleanly into transcript
  await repo.call('/api/v1/sessions/demo-mac/demo-agent-changelog/queue/send-now', 'POST', {});
  const afterSendNow = (await repo.sessions()).find((row) => row.id === 'demo-agent-changelog');
  assert.equal(extractComposerQueue(afterSendNow.data()).items.length, 0);
  const transcriptAfterSendNow = repo.historyItems(afterSendNow);
  assert.ok(transcriptAfterSendNow.some((item) => item.text === 'Include Mate XT 2 tri-fold notes'));
  assert.ok(transcriptAfterSendNow.some((item) => item.text === 'Double-check zh_CN strings'));

  // 6. Posting a message also dequeues remaining queued items cleanly
  repo.seed();
  await repo.call('/api/v1/sessions/demo-mac/demo-agent-changelog/messages', 'POST', {
    text: 'Ship the changelog draft.',
    client_msg_id: 'demo-msg-dequeue'
  });
  const afterPost = (await repo.sessions()).find((row) => row.id === 'demo-agent-changelog');
  assert.equal(extractComposerQueue(afterPost.data()).items.length, 0);
  assert.ok(repo.historyItems(afterPost).some((item) => item.text === 'Ship the changelog draft.'));
});

test('ComposerTopStrip, Conversation, MarkdownText, WorkStepsCard, and localized strings satisfy Stage 1B craft', () => {
  const strip = read('components/ComposerTopStrip.ets');
  const conversation = read('components/Conversation.ets');
  const markdown = read('components/MarkdownText.ets');
  const workSteps = read('components/WorkStepsCard.ets');

  assert.ok(strip.includes('@ComponentV2'));
  assert.ok(strip.includes("id('composer.queue.strip')"));
  assert.ok(strip.includes("id('composer.queue.toggle')"));
  assert.ok(strip.includes("id('composer.queue.send_now')"));
  assert.ok(strip.includes("id('composer.queue.list')"));
  assert.ok(strip.includes('Colors.divider'));
  assert.ok(strip.includes('Motion.state'));
  assert.ok(strip.includes('Haptics.selection()'));
  assert.ok(strip.includes("toastCenter.show(tr('copied'))"));
  assert.ok(strip.includes("tr('composer_queue_relayed')"));

  assert.ok(conversation.includes('ComposerTopStrip'));
  assert.ok(conversation.includes('extractComposerQueue'));
  assert.ok(conversation.includes('composerQueueState.items.length > 0'));
  assert.ok(conversation.includes('/queue/send-now'));

  assert.ok(markdown.includes('wrappedCodeKeys'));
  assert.ok(markdown.includes("tr(this.isCodeWrapped(blockKey) ? 'code_unwrap_lines' : 'code_wrap_lines')"));
  assert.ok(markdown.includes("id('markdown.code.wrap')"));
  assert.ok(markdown.includes("id('markdown.code.copy')"));
  assert.ok(markdown.includes('Haptics.selection()'));
  assert.ok(markdown.includes("toastCenter.show(tr('copied'))"));
  assert.ok(markdown.includes("tr('copied')"));

  assert.ok(workSteps.includes('Motion.state'));
  assert.ok(workSteps.includes("id('chat.work.timeline.count')"));
  assert.ok(workSteps.includes('Haptics.selection()'));
  assert.ok(workSteps.includes("toastCenter.show(tr('copied'))"));

  const requiredKeys = [
    'composer_queued_count',
    'composer_queue_send_now',
    'composer_queue_relayed',
    'code_wrap_lines',
    'code_unwrap_lines',
    'copied'
  ];
  for (const locale of ['base', 'en_US', 'zh_CN', 'ja_JP']) {
    const names = new Set(readResource(locale).string.map((entry) => entry.name));
    for (const key of requiredKeys) {
      assert.ok(names.has(key), `${locale} missing string key: ${key}`);
    }
  }
});
