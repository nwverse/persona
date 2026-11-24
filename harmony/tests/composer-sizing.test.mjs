import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';

const source = readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets', import.meta.url), 'utf8');
const tokens = readFileSync(new URL('../entry/src/main/ets/design/Tokens.ets', import.meta.url), 'utf8');
const Tokens = new Function('return (' + stripTypeScriptTypes(tokens.replace('export class', 'class')) + ')')();
function method(name) {
  const match = new RegExp('  private ' + name + '\\(').exec(source);
  assert.ok(match, name);
  const start = match.index, body = source.indexOf('{', start);
  let depth = 1, i = body + 1;
  for (; i < source.length && depth; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') depth--;
  }
  assert.equal(depth, 0, name);
  return source.slice(start, i);
}
const Composer = new Function('Tokens', 'return ' + stripTypeScriptTypes('class Composer {' +
  ['leftActionWidth', 'expandedComposer', 'composerColumns', 'sendColumns', 'leftColumns'].map(method).join('\n') + '}'))(Tokens);
function fixture(density = 3, width = 372) {
  return Object.assign(new Composer(), {
    composerWidth: width, fieldFocused: false, model: { demo: false, draft: '' },
    attachments: { attachments: [] }, environment: { fontScale: 1 }, showContextMenu: true,
    getUIContext: () => ({ vp2px: value => value * density })
  });
}

test('compact Persona and Agent editors get the remaining width after fixed actions and equal gaps', () => {
  const v = fixture();
  assert.equal(v.expandedComposer(), false);
  assert.equal(v.composerColumns(), 1116);
  assert.equal(v.leftColumns(), 318);
  assert.equal(v.sendColumns(), 150);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 648);
  v.showContextMenu = false;
  assert.equal(v.leftColumns(), 150);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 816);
});

test('compact action gaps stay within one physical pixel at each native device density', () => {
  for (const density of [3, 2.75, 3.125]) for (const width of [372, 587.27, 647.68]) {
    const v = fixture(density, width), unit = width * density / v.composerColumns();
    assert.equal(v.expandedComposer(), false);
    for (const gap of [v.leftColumns() * unit - v.leftActionWidth() * density,
      v.sendColumns() * unit - Tokens.touch * density]) {
      assert.ok(Math.abs(gap - Tokens.composerGutter * density) <= 1.01, { density, width, gap });
    }
  }
});

test('focus, a retained draft, attachments and accessibility sizes keep the existing expanded layout', () => {
  for (const change of [v => v.fieldFocused = true, v => v.model.draft = '你',
    v => v.attachments.attachments.push({}), v => v.environment.fontScale = 1.5,
    v => v.composerWidth = 300]) {
    const v = fixture(); change(v);
    assert.equal(v.expandedComposer(), true);
    assert.equal(v.composerColumns(), 12);
  }
});

test('dismissed input fits the compact row without losing the draft', () => {
  const v = fixture();
  v.model.draft = '保留草稿'; v.editorDismissed = true;
  assert.equal(v.expandedComposer(), false);
  assert.equal(v.model.draft, '保留草稿');
});
