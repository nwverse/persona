import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { stripTypeScriptTypes } from 'node:module';
import { projectToolTurn } from '../entry/src/main/ets/core/Domain.ts';
import { stepsToItems } from '../../integrations/antigravity/lib/map.js';

const source = readFileSync(new URL('../entry/src/main/ets/models/ClientModel.ets', import.meta.url), 'utf8');

function method(name, file = source) {
  const match = new RegExp('  private ' + name + '\\(').exec(file);
  assert.ok(match, name);
  const start = match.index, body = file.indexOf('{', start);
  let depth = 1, quote = '', escape = false, index = body + 1;
  for (; index < file.length && depth; index += 1) {
    const character = file[index];
    if (quote) {
      if (escape) escape = false;
      else if (character === '\\') escape = true;
      else if (character === quote) quote = '';
    } else if (character === '"' || character === "'" || character === '`') quote = character;
    else if (character === '{') depth += 1;
    else if (character === '}') depth -= 1;
  }
  return file.slice(start, index);
}

class ChatMessage {
  constructor(id, role, text = '') { Object.assign(this, { id, role, text }); }
}

const ProjectionHarness = new Function(
  'projectToolTurn', 'ChatMessage', 'messageDisplay', 'parseMessageContent',
  'return ' + stripTypeScriptTypes('class ProjectionHarness {' + method('projectMessages') + '}'),
)(
  projectToolTurn,
  ChatMessage,
  text => ({ text, sources: [] }),
  text => ({ text, images: [], files: [] }),
);

const configs = [
  { reading_case: true },
  { reading_case: true, reading_prepend: true },
  { reading_case: true, reading_prepend: true, reading_append: true },
];
const fixturePath = fileURLToPath(new URL('./native-ui-fixture.py', import.meta.url));
const python = [
  'import importlib.util, json, sys',
  "spec = importlib.util.spec_from_file_location('native_ui_fixture', sys.argv[1])",
  'fixture = importlib.util.module_from_spec(spec)',
  'spec.loader.exec_module(fixture)',
  'controls = json.loads(sys.argv[2])',
  'print(json.dumps([fixture.transcript(control=control)["items"] for control in controls]))',
].join('; ');
const transcripts = JSON.parse(execFileSync(
  'python3', ['-c', python, fixturePath, JSON.stringify(configs)], { encoding: 'utf8' },
));

function project(items) {
  const model = Object.assign(new ProjectionHarness(), {
    target: () => 'fixture-reading-target',
    itemTime: () => 0,
    workText: step => step.item?.text ?? '',
  });
  return model.projectMessages(items, false);
}

test('native transcript projection keeps a middle stable ID across prepend and append phases', () => {
  const middleId = 'visible-fixture-reading-50#0';
  const initial = project(transcripts[0]);
  assert.equal(initial.length, 20);
  assert.ok(initial.some(message => message.id === middleId));

  const prepended = project(transcripts[1]);
  assert.equal(prepended.length, 40);
  assert.equal(prepended[31].id, middleId);

  const appended = project(transcripts[2]);
  assert.equal(appended.length, 45);
  assert.equal(appended[31].id, middleId);
  assert.equal(appended[31].itemId, 'fixture-reading-51#0');
});

test('an Antigravity wake keeps the finished reply and its attachment in the conversation', () => {
  const raw = readFileSync(new URL('../../integrations/antigravity/test/fixtures/autonomous-wake.jsonl', import.meta.url), 'utf8');
  const steps = raw.trim().split('\n').map(line => JSON.parse(line));
  const prior = project(stepsToItems(steps.slice(0, 3)).items);
  const history = stepsToItems(steps).items;
  const after = project(history);
  const replies = after.filter(row => row.role === 'assistant');
  assert.deepEqual(replies.map(row => row.text), [steps[2].content, steps[5].content, steps[8].content]);
  assert.deepEqual(replies[0], prior.find(row => row.role === 'assistant'));
  assert.match(replies[0].text, /file: \/synthetic\/chart\.png/);
  assert.equal(after.filter(row => row.role === 'user').length, 2);
  assert.ok(after.every(row => !row.work?.includes(steps[2].content)));

  const wake = history.findIndex(item => item.wake === true);
  assert.ok(wake >= 0);
  const recap = { id: 'before-wake-recap', role: 'system', kind: 'recap', text: 'The chart is ready.' };
  const withRecap = project([...history.slice(0, wake), recap, ...history.slice(wake)]);
  assert.deepEqual(withRecap.filter(row => row.role === 'assistant').map(row => row.text), replies.map(row => row.text));
  assert.equal(withRecap.find(row => row.role === 'recap').terminalRecap, false);
});

test('real recap entries retain original answers, time ordering and all consecutive source texts', () => {
  const inputs = [
    { id: 'ask', seq: 1, role: 'user', kind: 'text', text: '看一下 PR #42' },
    { id: 'answer', seq: 2, role: 'assistant', kind: 'text', text: '两条命名建议' },
    { id: 'recap1', seq: 3, role: 'system', kind: 'recap', text: '旧回顾' },
    { id: 'recap2', seq: 4, role: 'system', kind: 'recap', text: '最新回顾：`fetchAll`' }
  ];
  const legacy = project(inputs.slice(0, 2));
  const withRecap = project(inputs);
  assert.deepEqual(withRecap.slice(0, 2), legacy);
  const last = withRecap.at(-1);
  assert.equal(last.role, 'recap');
  assert.equal(last.terminalRecap, true);
  assert.equal(last.ownerTarget, 'fixture-reading-target');
  assert.deepEqual(last.recaps.map(item => [item.id, item.text]), [['recap1', '旧回顾'], ['recap2', '最新回顾：`fetchAll`']]);
  const next = project(inputs.concat({ id: 'next', seq: 5, role: 'assistant', kind: 'text', text: '已补测试' }));
  assert.equal(next.find(item => item.role === 'recap').terminalRecap, false);
  assert.equal(next.at(-1).text, '已补测试');
  assert.ok(project([{ id: 'pretend', seq: 1, role: 'user', kind: 'recap', text: '文档' }]).every(item => item.role !== 'recap'));
});

test('a recap dismissal belongs to its hub and session and an older storage read cannot close a new recap', async () => {
  const recapSource = readFileSync(new URL('../entry/src/main/ets/components/ConversationRecap.ets', import.meta.url), 'utf8');
  const Recap = new Function('return ' + stripTypeScriptTypes('class Recap {' +
    ['current', 'newest', 'syncOwner', 'open', 'dismiss'].map(name => method(name, recapSource)).join('\n') + '}'))();
  const reads = new Map(), writes = [], stored = new Map();
  let scope = 'hub-1', target = 'macbook/review';
  const v = Object.assign(new Recap(), {
    active: true, closed: '', ready: false, ownerKey: '', shown: false,
    model: { selected: {kind:'session'}, target: () => target, scope: () => scope, connectionGeneration: () => 1,
      captureView: key => {
        const address = scope + ':' + key, read = Promise.withResolvers(); reads.set(address, read);
        return { valid: () => true, read: () => read.promise, write: async value => { writes.push([address,value]);stored.set(address,value); } };
      }},
    message: {id:'recap:a', ownerTarget:target, terminalRecap:true, recaps:[{id:'a',text:'A'}]}
  });
  v.syncOwner();
  const oldAddress = 'hub-1:closed-recap:macbook/review';
  scope='hub-2';v.syncOwner();
  const address = 'hub-2:closed-recap:macbook/review';
  reads.get(address).resolve('');await Promise.resolve();
  assert.equal(v.open(),true);
  v.dismiss();
  assert.equal(v.open(),false);
  assert.deepEqual(writes,[[address,'a']]);
  reads.get(oldAddress).resolve('foreign-id');await Promise.resolve();
  assert.equal(v.closed,'a');
  target='macbook/other';v.message={id:'recap:b',ownerTarget:target,terminalRecap:true,recaps:[{id:'b'}]};v.syncOwner();
  reads.get('hub-2:closed-recap:macbook/other').resolve('');await Promise.resolve();assert.equal(v.open(),true);
  target='macbook/review';v.message={id:'recap:a',ownerTarget:target,terminalRecap:true,recaps:[{id:'a'}]};v.syncOwner();
  reads.get(address).resolve(stored.get(address));await Promise.resolve();assert.equal(v.open(),false);
  v.message={...v.message,recaps:[{id:'a'},{id:'new'}]};v.syncOwner();assert.equal(v.open(),true);
  v.active=false;v.syncOwner();v.dismiss();assert.equal(writes.length,1);
});
