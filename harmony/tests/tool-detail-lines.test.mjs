import test from 'node:test';
import assert from 'node:assert/strict';
import { diffDetailLines, searchDetailLines, sourceDetailLines, toolDetailPage } from '../entry/src/main/ets/core/ToolDetailLines.ts';

test('source and search gutters use recorded positions and unfamiliar output keeps its original text', () => {
  const numbered = sourceDetailLines(' 14\t  call()\r\n 37→next\n', 100);
  assert.deepEqual(numbered.map(l => [l.number, l.text]), [['14', '  call()'], ['37', 'next']]);
  assert.deepEqual(sourceDetailLines('a\n\nb', 17).map(l => l.number), ['17', '18', '19']);
  assert.deepEqual(sourceDetailLines('14\tline\nsummary').map(l => [l.number, l.text]), [['', '14\tline'], ['', 'summary']]);
  const hits = searchDetailLines('a.ts:4:  one\na.ts:9:two\nC:\\src\\b.ts:8:three:four\na.ts:10:five');
  assert.deepEqual(hits.filter(l => l.kind === 'file').map(l => l.text), ['a.ts', 'C:\\src\\b.ts', 'a.ts']);
  assert.deepEqual(hits.filter(l => l.number).map(l => l.text), ['  one', 'two', 'three:four', 'five']);
  for (const text of ['No hits', 'a.ts:4:x\n2 files searched', 'https://host:80:path', '12:23:14']) assert.deepEqual(searchDetailLines(text), []);
});

test('hunks, file boundaries, empty ranges and header-like content keep their correct gutter and sign', () => {
  const diff = ['--- a/a', '+++ b/a', '@@ -3,2 +10,3 @@', '--- old', '+++ new', '+second', ' tail',
    'diff --git a/b b/b', '--- a/b', '+++ b/b', '@@ -0,0 +1 @@', '+new file', '@@ -4 +9,0 @@', '-gone'].join('\n');
  assert.deepEqual(diffDetailLines(diff).map(l => [l.kind, l.number]), [
    ['file', ''], ['file', ''], ['hunk', ''], ['remove', '3'], ['add', '10'], ['add', '11'], ['code', '12'],
    ['file', ''], ['file', ''], ['file', ''], ['hunk', ''], ['add', '1'], ['hunk', ''], ['remove', '4'],
  ]);
  assert.equal(diffDetailLines('-older\n+newer')[0].number, '');
  assert.equal(diffDetailLines('@@ -2,0 +2,2 @@\n-malformed\n+real')[1].number, '');
});

test('large logs appear progressively with complete source and without splitting a Unicode pair', () => {
  const lines = Array.from({ length: 126 }, (_, i) => 'line ' + i + '\r\n').join('');
  assert.equal(toolDetailPage(lines, 1).split('\n').length - 1, 50);
  assert.ok(toolDetailPage(lines, 2).startsWith(toolDetailPage(lines, 1)));
  assert.equal(toolDetailPage(lines, 3), lines);
  const long = 'x'.repeat(5999) + '📱' + 'y'.repeat(8700);
  assert.equal(toolDetailPage(long, 1), long.slice(0, 5999));
  assert.equal(toolDetailPage(long, 3), long);
});
