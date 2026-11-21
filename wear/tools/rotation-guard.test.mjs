import assert from 'node:assert/strict';
import test from 'node:test';
import { withPreservedRotation, withPreservedRotationAsync } from './rotation-guard.mjs';

function watch(mode = 'lock 0', rotation = '0', accelerometer = '0') {
  const state = { mode, rotation, accelerometer, writes: [] };
  const run = args => {
    if (args[1] === 'settings') return Buffer.from(state[args[4] === 'user_rotation' ? 'rotation' : 'accelerometer']);
    if (args.length === 3) return Buffer.from(state.mode);
    state.writes.push(args.slice(3));
    if (args[3] === 'lock') { state.rotation = args[4]; state.mode = `lock ${args[4]}`; state.accelerometer = '0'; }
    else { state.mode = 'free'; state.accelerometer = '1'; }
    return Buffer.alloc(0);
  };
  const leak = () => { state.mode = 'lock 1'; state.rotation = '1'; state.accelerometer = '0'; };
  return { state, run, leak };
}

test('restores locked preference after a temporary charging orientation leaks', () => {
  const { state, run, leak } = watch();
  assert.equal(withPreservedRotation(run, () => { leak(); return 'captured'; }), 'captured');
  assert.equal(state.mode, 'lock 0');
  assert.deepEqual(state.writes, [['lock', '0']]);
});
test('restores free rotation and its original fallback', () => {
  const { state, run, leak } = watch('free', '0', '1');
  withPreservedRotation(run, leak);
  assert.equal(state.mode, 'free');
  assert.equal(state.rotation, '0');
  assert.deepEqual(state.writes, [['lock', '0'], ['free']]);
});
for (const failure of ['capture failed', 'remote automation timed out']) {
  test(`restores after ${failure}`, () => {
    const { state, run, leak } = watch('free', '0', '1');
    assert.throws(() => withPreservedRotation(run, () => { leak(); throw new Error(failure); }), new RegExp(failure));
    assert.equal(state.mode, 'free');
    assert.equal(state.rotation, '0');
  });
}
test('does not write unchanged preferences', () => {
  const { state, run } = watch();
  withPreservedRotation(run, () => {});
  assert.deepEqual(state.writes, []);
});
test('missing or malformed preferences never start automation', () => {
  for (const value of ['null', '', '4', 'not-a-number']) {
    const { state, run } = watch('free', value, '1');
    let started = false;
    assert.throws(() => withPreservedRotation(run, () => { started = true; }), /Cannot read/);
    assert.equal(started, false);
    assert.deepEqual(state.writes, []);
  }
});
test('failed restoration is reported instead of claiming a successful capture', () => {
  const { state, run, leak } = watch();
  assert.throws(() => withPreservedRotation(args => {
    if (args.length > 3 && args[1] === 'wm') throw new Error('device disconnected');
    return run(args);
  }, leak), /Rotation restoration failed/);
  assert.equal(state.mode, 'lock 1');
});

test('failed automation teardown prevents rotation reads and writes after the operation', () => {
  const { state, run, leak } = watch();
  assert.throws(() => withPreservedRotation(run, leak, () => { throw new Error('UiAutomation still active'); }),
    /shutdown could not be confirmed/);
  assert.equal(state.mode, 'lock 1');
  assert.deepEqual(state.writes, []);
});

test('async rotation guard waits for the operation and teardown gate before restoration', async () => {
  const { state, run, leak } = watch();
  let release;
  let gated = false;
  const pending = withPreservedRotationAsync(run, () => new Promise(resolve => { leak(); release = resolve; }),
    () => { gated = true; assert.equal(state.mode, 'lock 1'); });
  await Promise.resolve();
  assert.equal(gated, false);
  assert.deepEqual(state.writes, []);
  release('completed');
  assert.equal(await pending, 'completed');
  assert.equal(gated, true);
  assert.equal(state.mode, 'lock 0');
});
