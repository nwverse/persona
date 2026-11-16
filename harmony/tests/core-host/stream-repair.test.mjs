// Replays the Apple StreamRepair assertions through the real Rust core's ArkTS host projection command, the same
// cases the Kotlin JNI replay already states. Expectations come from the Apple Swift test source, never from the
// Rust output. This is host evidence only: no NAPI addon, production ArkTS adapter, HAP or device run.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { startCore } from './host.mjs';

// 21 fence-repair expectations: StreamRepairTests.closesAnOpenCodeFenceWithItsOwnMarker and
// holdsAFencesOpeningLineBack. Same inputs and expected strings as StreamRepairBindingFixtureTest.
const fence = [
  ['text\n```ts\nconst a', 'text\n```ts\nconst a\n```'],
  ['~~~~\nx\n', '~~~~\nx\n~~~~'],
  ['```\na\n```\ndone', '```\na\n```\ndone'],
  ['看这段：\n```swi', '看这段：\n'],
  ['```mermaid', ''],
  ['看这段：\n```swift\n', '看这段：\n```swift\n```'],
  ['```\nlet a = 1\n``', '```\nlet a = 1\n```'],
  ['````\nx\n```', '````\nx\n````'],
  ['````\nx\n```\n', '````\nx\n```\n````'],
  ['prefix\n````\nx\n```\n', 'prefix\n````\nx\n```\n````'],
  ['```\n**x [a](b', '```\n**x [a](b\n```'],
  ['```\n-', '```\n-\n```'],
  ['1. 安装依赖：\n\n   ```bash\n   npm install', '1. 安装依赖：\n\n   ```bash\n   npm install\n   ```'],
  ['1. 安装依赖：\n   ```bash\n   npm install\n', '1. 安装依赖：\n   ```bash\n   npm install\n   ```'],
  ['- 运行：\n  ~~~\n  make', '- 运行：\n  ~~~\n  make\n  ~~~'],
  ['- 运行：\n  ```\n  make\n  ``', '- 运行：\n  ```\n  make\n  ```'],
  ['```\na\n  ', '```\na\n```'],
  ['这是 **重要', '这是 **重要'],
  ['```swift\nlet a\n```', '```swift\nlet a\n```'],
  ['```swift\nlet a\n```\n', '```swift\nlet a\n```\n'],
  ['', ''],
];
// 6 fence/text end expectations: tellsWhereTheTextEnds.
const fenceEnd = [
  ['', 'text'],
  ['这是 **重要', 'text'],
  ['看这段：\n```swi', 'code'],
  ['```swift\nlet a', 'code'],
  ['```swift\nlet a\n```', 'code'],
  ['```swift\nlet a\n```\n', 'text'],
];
// 14 unfinished-marker expectations: holdsALastLineOfOnlyMarkersBack.
const marker = [
  ['- 第一项\n-', '- 第一项\n'],
  ['- 第一项\n- ', '- 第一项\n'],
  ['1. 第一步\n2.', '1. 第一步\n'],
  ['1. 第一步\n12', '1. 第一步\n'],
  ['正文\n##', '正文\n'],
  ['正文\n>', '正文\n'],
  ['标题\n---', '标题\n'],
  ['- [ ]', ''],
  ['**', ''],
  ['- 第', '- 第'],
  ['2026年', '2026年'],
  ['1.5 倍', '1.5 倍'],
  ['-\n', '-\n'],
  ['正文\n---\n', '正文\n---\n'],
];
// 22 Apple table-repair expectations from holdsATablesHalfWrittenLastRowBack,
// givesATableHeaderItsDelimiterRowEarly, findsATableRightUnderAParagraphLine, keepsATableInTheListItemItIsIn
// and findsNoTableInALazyLine.
const head = '| 文件 | 改动 |\n| --- | --- |\n| a.swift | 刷新 |\n';
const table = [
  [head + '| b.swi', head],
  [head + '| b.swift | 超时 |\n', head + '| b.swift | 超时 |\n'],
  ['| 文件 | 改', '| 文件 | 改'],
  ['Use a | b\n| not a table', 'Use a | b\n| not a table'],
  ['| 文件 | 改动 |\n', '| 文件 | 改动 |\n| --- | --- |'],
  ['| a | b |\n| --', '| a | b |\n| --- | --- |'],
  ['| a | b |\n|', '| a | b |\n| --- | --- |'],
  ['## 结果\n| a | b | c |\n', '## 结果\n| a | b | c |\n| --- | --- | --- |'],
  ['| a | b |\n| :-- | --: |', '| a | b |\n| :-- | --: |'],
  ['| 不是表格\n', '| 不是表格\n'],
  ['| x |\n| a | b |\n', '| x |\n| a | b |\n'],
  ['**结果：**\n| a | b |\n', '**结果：**\n| a | b |\n| --- | --- |'],
  ['**结果：**\n| a | b |\n| --', '**结果：**\n| a | b |\n| --- | --- |'],
  ['**结果：**\n| a | b |\n| --- | --- |\n| 1', '**结果：**\n| a | b |\n| --- | --- |\n'],
  ['- 项\n\n  | a | b |\n', '- 项\n\n  | a | b |\n  | --- | --- |'],
  ['1. 项\n   | a | b |\n   | -', '1. 项\n   | a | b |\n   | --- | --- |'],
  ['- 项\n  | a | b |\n  | --- | --- |\n  | 1', '- 项\n  | a | b |\n  | --- | --- |\n'],
  ['- 项\n\n  | a | b |\n  | --- | --- |\n  ', '- 项\n\n  | a | b |\n  | --- | --- |\n'],
  ['- 项\n| a | b |\n', '- 项\n| a | b |\n'],
  ['- a\n  - b\n  | x |\n', '- a\n  - b\n  | x |\n'],
  ['> 引用\n| a | b |\n', '> 引用\n| a | b |\n'],
  ['- 项\n| a | b |\n| --- | --- |\n| 1', '- 项\n| a | b |\n| --- | --- |\n| 1'],
];
// 15 table/text end expectations: same five Apple table tests plus the table cases in tellsWhereTheTextEnds.
const tableEnd = [
  ['**结果：**\n| a | b |\n| --- | --- |\n| 1', 'table'],
  ['**结果：**\n| a | b |\n| --- | --- |\n| 1 | 2 |\n', 'table'],
  ['| a | b |\n| --- | --- |\nfoo', 'table'],
  ['| a | b |\n| --- | --- |\n- 项', 'text'],
  ['- 项\n  | a | b |\n  | --- | --- |\n  | 1', 'table'],
  ['- 项\n\n  | a | b |\n  | --- | --- |\n  ', 'table'],
  ['- 项\n\n  | a | b |\n', 'table'],
  ['- 项\n\n  | a | b |\n  ', 'table'],
  ['- 项\n| a | b |\n| --- | --- |\n| 1', 'text'],
  ['| a | b |\n| --- | --- |\n| 1', 'table'],
  ['| a | b |\n| --- | --- |\n| 1 | 2 |\n', 'table'],
  ['| a | b |\n', 'table'],
  ['| a | b |\n| -', 'table'],
  ['| a | b', 'text'],
  ['| a | b |\n| --- | --- |\n\n后文', 'text'],
];

async function withHost(context, run) {
  const host = startCore();
  context.after(() => host.kill(), {timeout: 1_000});
  try {
    await run(host);
  } finally {
    try {
      await host.end();
    } finally {
      host.kill();
    }
  }
}
const options = {timeout: 10_000};

test('ArkTS host replays every fence-repair and end expectation', options, async (context) => {
  await withHost(context, async (host) => {
    let count = 0;
    for (const [text, expected] of fence) {
      const actual = await host.projection('stream_repair', {text});
      assert.equal(actual.repaired, expected, `fence: ${JSON.stringify(text)}`);
      count++;
    }
    for (const [text, expected] of fenceEnd) {
      const actual = await host.projection('stream_repair', {text});
      assert.equal(actual.end, expected, `fence end: ${JSON.stringify(text)}`);
    }
    console.log(`  stream_repair fence host replay: ${count} repaired + ${fenceEnd.length} end expectations`);
  });
});

test('ArkTS host replays every unfinished-marker expectation', options, async (context) => {
  await withHost(context, async (host) => {
    let count = 0;
    for (const [text, expected] of marker) {
      const actual = await host.projection('stream_repair', {text});
      assert.equal(actual.repaired, expected, `marker: ${JSON.stringify(text)}`);
      count++;
    }
    console.log(`  stream_repair marker host replay: ${count} expectations`);
  });
});

test('ArkTS host replays every table repair and table/text end expectation', options, async (context) => {
  await withHost(context, async (host) => {
    let count = 0;
    for (const [text, expected] of table) {
      const actual = await host.projection('stream_repair', {text});
      assert.equal(actual.repaired, expected, `table: ${JSON.stringify(text)}`);
      count++;
    }
    for (const [text, expected] of tableEnd) {
      const actual = await host.projection('stream_repair', {text});
      assert.equal(actual.end, expected, `table end: ${JSON.stringify(text)}`);
    }
    console.log(`  stream_repair table host replay: ${count} repaired + ${tableEnd.length} end expectations`);
  });
});

test('ArkTS host rejects malformed stream_repair inputs and wrong expectations', options, async (context) => {
  await withHost(context, async (host) => {
    await assert.rejects(() => host.projection('stream_repair', {}), /stream_repair: input does not decode/);
    await assert.rejects(() => host.projection('stream_repair', {text: 1}), /stream_repair: input does not decode/);
    // A deliberately wrong complete expectation must not compare equal to the real projection.
    const actual = await host.projection('stream_repair', {text: 'text\n```ts\nconst a'});
    const wrong = {repaired: 'text\n```ts\nconst a\n~~~', end: 'text'};
    assert.notDeepEqual(actual, wrong, 'the wrong complete expectation must differ');
    assert.throws(() => assert.deepEqual(actual, wrong, 'stream_repair fence negative'),
      (error) => error instanceof assert.AssertionError && error.message.includes('stream_repair fence negative'));
    const actualTable = await host.projection('stream_repair', {text: '| a | b |\n| -'});
    const wrongTable = {repaired: '| a | b |\n| --- | --- |', end: 'text'};
    assert.notDeepEqual(actualTable, wrongTable, 'the wrong table expectation must differ');
  });
});
