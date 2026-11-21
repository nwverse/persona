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
  ['hasComposerAction', 'trailingActionWidth', 'leftActionWidth', 'expandedComposer', 'composerColumns', 'sendColumns', 'leftColumns'].map(method).join('\n') + '}'))(Tokens);
function fixture(density = 3, width = 372) {
  return Object.assign(new Composer(), {
    composerWidth: width, fieldFocused: false, primaryStop: () => false, sending: false, model: { demo: false, draft: '' },
    attachments: { attachments: [] }, environment: { fontScale: 1 }, showContextMenu: true,
    getUIContext: () => ({ vp2px: value => value * density })
  });
}

test('compact Persona and Agent editors get the remaining width after fixed actions and equal gaps', () => {
  const v = fixture();
  assert.equal(v.expandedComposer(), false);
  assert.equal(v.composerColumns(), 1116);
  assert.equal(v.leftColumns(), 336);
  assert.equal(v.sendColumns(), 168);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 612);
  v.showContextMenu = false;
  assert.equal(v.leftColumns(), 168);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 780);
});

test('compact action gaps stay within one physical pixel at each native device density', () => {
  for (const density of [3, 2.75, 3.125]) for (const width of [372, 587.27, 647.68]) {
    const v = fixture(density, width), unit = width * density / v.composerColumns();
    assert.equal(v.expandedComposer(), false);
    for (const gap of [v.leftColumns() * unit - v.leftActionWidth() * density,
      v.sendColumns() * unit - Tokens.touch * density]) {
      assert.ok(Math.abs(gap - Tokens.gap * density) <= 1.01, { density, width, gap });
    }
  }
});

test('focus, a restored draft, a quote or an attachment opens the editing card independently', () => {
  for (const change of [
    v => { v.fieldFocused = true; },
    v => { v.model.draft = '你好'; },
    v => { v.model.sourceContext = { title: '引用' }; },
    v => { v.attachments.attachments.push({ id: 'file' }); }]) {
    const v = fixture();
    change(v);
    assert.equal(v.expandedComposer(), true);
    assert.equal(v.composerColumns(), 12);
  }
});

test('a blurred empty composer returns to one line, with enough space for the keyboard and both toolbars', () => {
  const v = fixture();
  v.fieldFocused = true;
  assert.equal(v.expandedComposer(), true);
  v.fieldFocused = false;
  assert.equal(v.expandedComposer(), false);

  for (const change of [
    v => { v.environment.fontScale = 1.5; },
    v => { v.composerWidth = 300; }]) {
    const narrow = fixture();
    change(narrow);
    assert.equal(narrow.expandedComposer(), true);
    assert.equal(narrow.composerColumns(), 12);
  }
});

test('a draft can expand above the keyboard, while a short unfocused draft keeps its quiet shape', () => {
  const Editor = new Function('Tokens','return '+stripTypeScriptTypes('class Editor {'+
    ['offersDraftEditor','rememberSelection','parkSelection','restoreSelection','draftEditorCurrent'].map(method).join('\n')+'}'))(Tokens);
  const v = Object.assign(new Editor(), {fieldFocused:false, model:{draft:'Review PR 42'}, editorHeight:48,
    environment:{fontScale:1}, draftEditorOwner:'hubA/review', sendCurrent:()=>true,composerAvailable:()=>true,
    composerOwnerKey:()=>'hubA/review'});
  assert.equal(v.offersDraftEditor(),false);v.fieldFocused=true;assert.equal(v.offersDraftEditor(),true);
  v.fieldFocused=false;v.model.draft='a\nb';assert.equal(v.offersDraftEditor(),true);
  v.model.draft='wrapped';v.editorHeight=167;assert.equal(v.offersDraftEditor(),true);
  v.rememberSelection(1,6);v.parkSelection();v.rememberSelection(0,0);
  v.model.draft='abc';let restored;
  v.restoreSelection({setTextSelection:(...range)=>{restored=range;}});
  assert.deepEqual(restored,[1,3]);assert.equal(v.draftEditorCurrent(),true);
  v.composerOwnerKey=()=>'hubB/review';assert.equal(v.draftEditorCurrent(),false);
});
