import assert from 'node:assert/strict';
import { spawnSync, spawn } from 'node:child_process';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { automationActive, hasOwnedInstrumentation, instrumentationPassed, runInstrumentation, runWatchCheck } from './run-watch-check.mjs';

const processes = 'ACTIVITY MANAGER RUNNING PROCESSES (dumpsys activity processes)\n';
const accessibility = 'ACCESSIBILITY MANAGER (dumpsys accessibility)\ncurrentUserId=0\n';
const own = `${processes}  Active instrumentation:\n  Instrumentation #0: ActiveInstrumentation{sample}\n`
  + '    mClass=ComponentInfo{dev.persona.watch.test/dev.persona.watch.WatchSmokeRunner}\n'
  + '    mTargetProcesses=[dev.persona.watch]\n';
const unrelated = own.replaceAll('dev.persona.watch', 'example.other');
const pass = { status: 0, stdout: 'INSTRUMENTATION_RESULT: stream=PASS: fixture\nINSTRUMENTATION_CODE: -1\n', stderr: '' };

async function mock({ result = pass, needsTargetStop = false, unrelatedSession = false, neverEnds = false, unknown = false, failedProbe = false, automationDump, mode } = {}) {
  const events = [];
  const errors = [];
  let clock = 0;
  let rotation = '0';
  let instrumented = false;
  let instrumentCommand;
  let stopped = false;
  let probes = 0;
  const spawn = (executable, args) => {
    assert.equal(executable, 'FAKE_ADB');
    assert.deepEqual(args.slice(0, 2), ['-s', 'TESTWATCH']);
    const cmd = args.slice(2);
    const ok = text => ({ status: 0, stdout: Buffer.from(text), stderr: Buffer.alloc(0) });
    if (cmd[1] === 'wm') {
      if (cmd.length === 3) { events.push('read rotation'); return ok(`lock ${rotation}`); }
      events.push('restore rotation');
      rotation = cmd[4];
      return ok('');
    }
    if (cmd[1] === 'settings') return ok(cmd[4] === 'user_rotation' ? rotation : '0');
    if (cmd[1] === 'am' && cmd[2] === 'instrument') {
      instrumentCommand = cmd;
      events.push('instrument'); instrumented = true; rotation = '1'; return result;
    }
    if (cmd[1] === 'am' && cmd[2] === 'force-stop') {
      events.push(`stop ${cmd[3]}`);
      if (cmd[3] === 'dev.persona.watch' || !needsTargetStop) stopped = true;
      return ok('');
    }
    if (cmd[1] === 'dumpsys' && cmd[2] === 'activity') {
      events.push('probe instrumentation');
      if (failedProbe) return { status: 1, stdout: Buffer.alloc(0) };
      if (unknown) return ok('Unknown command');
      return ok(unrelatedSession ? unrelated : instrumented && (neverEnds || needsTargetStop && !stopped) ? own : processes);
    }
    if (cmd[1] === 'dumpsys' && cmd[2] === 'accessibility') {
      probes++;
      const active = neverEnds || unrelatedSession || automationDump != null && probes < 3 || result !== pass && probes < 3;
      events.push(active ? 'UiAutomation active' : 'UiAutomation gone');
      return ok(accessibility + (automationDump ? automationDump(active) : active ? 'Ui Automation[eventTypes=TYPE_VIEW_CLICKED]' : ''));
    }
    throw new Error(`Unexpected mock command ${JSON.stringify(cmd)}`);
  };
  const args = ['--adb', 'FAKE_ADB', '--device', 'TESTWATCH', ...(mode ? ['--mode', mode] : [])];
  const status = await runWatchCheck(args, {
    spawn, stdout: () => {}, stderr: text => errors.push(text),
    cleanup: { now: () => clock, wait: delay => { clock += delay; }, timeoutMs: 400 },
  });
  return { status, events, errors, rotation, instrumentCommand };
}

test('successful instrumentation waits for teardown before restoring rotation', async () => {
  const result = await mock();
  assert.equal(result.status, 0);
  assert.equal(result.rotation, '0');
  assert.ok(result.events.indexOf('UiAutomation gone') < result.events.indexOf('restore rotation'));
  assert.equal(result.events.some(event => event.startsWith('stop ')), false);
  assert.equal(result.instrumentCommand.includes('mode'), false);
});

test('unknown requested modes fail before any ADB call and cannot accept a default smoke result', async () => {
  for (const mode of ['unknown', 'smoke', 'real_surface', 'fixture_extra', '__proto__', 'constructor', 'FIXTURE', 'fixture ']) {
    const calls = [], errors = [];
    const unexpected = (...args) => { calls.push(args); return pass; };
    const status = await runWatchCheck(['--device', 'TESTWATCH', '--mode', mode], {
      spawn: unexpected, instrument: unexpected, stdout: () => {}, stderr: value => errors.push(value),
    });
    assert.equal(status, 1, mode);
    assert.deepEqual(calls, [], mode);
    assert.match(errors.join(''), /Unsupported check mode/);
    assert.equal(instrumentationPassed(pass.stdout, mode), false);
  }
});

test('every supported PASS mode is forwarded exactly instead of falling back to smoke', async () => {
  for (const mode of ['fixture', 'multihub', 'complication_parcel', 'live_ping']) {
    const result = await mock({ mode });
    assert.equal(result.status, 0, mode);
    const index = result.instrumentCommand.indexOf('mode');
    assert.deepEqual(result.instrumentCommand.slice(index - 1, index + 2), ['-e', 'mode', mode]);
    assert.equal(result.events.filter(event => event === 'instrument').length, 1);
  }
});

test('a passing stream may quote failure-looking payload lines without triggering package cleanup', async () => {
  const stdout = 'INSTRUMENTATION_STATUS: stream=Checking expected failure cases\nFAIL: quoted status detail\n'
    + 'INSTRUMENTATION_STATUS_CODE: 0\n'
    + 'INSTRUMENTATION_RESULT: fixture=true\nINSTRUMENTATION_RESULT: stream=PASS: fixture\n'
    + 'FAIL: this is a quoted failure case in the passing result\n'
    + 'INSTRUMENTATION_RESULT: elapsed_ms=12\nINSTRUMENTATION_CODE: -1\n';
  assert.equal(instrumentationPassed(stdout), true);
  assert.equal(instrumentationPassed(stdout.replaceAll('\n', '\r\n')), true);
  const result = await mock({ result: { status: 0, stdout } });
  assert.equal(result.status, 0);
  assert.equal(result.events.some(event => event.startsWith('stop ')), false);
  assert.ok(result.events.indexOf('UiAutomation gone') < result.events.indexOf('restore rotation'));
});

test('result fields, not progress or a terminal code alone, must explicitly report success', () => {
  for (const stdout of [
    'INSTRUMENTATION_CODE: -1\n',
    'INSTRUMENTATION_RESULT: stream=\nINSTRUMENTATION_CODE: -1\n',
    'INSTRUMENTATION_RESULT: stream=No outcome\nPASS: payload only\nINSTRUMENTATION_CODE: -1\n',
    'INSTRUMENTATION_RESULT: detail=PASS: fixture\nINSTRUMENTATION_CODE: -1\n',
    'INSTRUMENTATION_STATUS: stream=PASS: progress\nINSTRUMENTATION_STATUS_CODE: 0\nINSTRUMENTATION_CODE: -1\n',
    'INSTRUMENTATION_RESULT: stream=FAIL: fixture\nPASS: quoted detail\nINSTRUMENTATION_CODE: -1\n',
  ]) assert.equal(instrumentationPassed(stdout), false, stdout);
});

test('observation and setup modes retain their own scoped outcome fields without claiming a completed face test', async () => {
  for (const [mode, key, heading] of [
    ['surfaces', 'surfaces_passed', 'Synthetic app surfaces passed:'],
    ['real_surfaces', 'real_surfaces_probed', 'OBSERVED:'],
    ['pet_expiry', 'pet_expiry_seeded', 'SEEDED:'],
    ['pet_expiry_restore', 'pet_expiry_restored', 'RESTORED:'],
  ]) {
    const stdout = `INSTRUMENTATION_RESULT: ${key}=true\nINSTRUMENTATION_RESULT: stream=${heading} sample outcome\nINSTRUMENTATION_CODE: -1\n`;
    assert.equal(instrumentationPassed(stdout, mode), true);
    assert.equal(instrumentationPassed(stdout), false);
    assert.equal(instrumentationPassed(stdout, 'fixture'), false);
    assert.equal(instrumentationPassed(stdout.replace(`${key}=true`, `${key}=false`), mode), false);
    assert.equal(instrumentationPassed(stdout.replace(`${key}=true`, 'another_result=true'), mode), false);
    assert.equal(instrumentationPassed(stdout.replace(heading, 'FAIL:'), mode), false);
    assert.equal(instrumentationPassed(pass.stdout, mode), false);
    const result = await mock({ mode, result: { status: 0, stdout } });
    assert.equal(result.status, 0);
    const index = result.instrumentCommand.indexOf('mode');
    assert.deepEqual(result.instrumentCommand.slice(index - 1, index + 2), ['-e', 'mode', mode]);
    assert.equal(result.events.some(event => event.startsWith('stop ')), false);
  }
});

test('actual failure records and malformed result framing fail and retain the cleanup ordering', async () => {
  for (const stdout of [
    'FAIL: unframed failure\n' + pass.stdout,
    'INSTRUMENTATION_FAILED: test crashed\n' + pass.stdout,
    'INSTRUMENTATION_ABORTED: test cancelled\n' + pass.stdout,
    'INSTRUMENTATION_RESULT: shortMsg=Process crashed\n' + pass.stdout,
    'INSTRUMENTATION_STATUS: Error=Process crashed\nINSTRUMENTATION_STATUS_CODE: 0\n' + pass.stdout,
    'INSTRUMENTATION_STATUS: stream=FAIL: fixture\nINSTRUMENTATION_STATUS_CODE: 0\n' + pass.stdout,
    'INSTRUMENTATION_STATUS: stream=Test error\nINSTRUMENTATION_STATUS_CODE: -2\n' + pass.stdout,
    'INSTRUMENTATION_STATUS: stream=Unterminated status\n' + pass.stdout,
    pass.stdout + 'INSTRUMENTATION_CODE: -1\n',
    pass.stdout + 'INSTRUMENTATION_RESULT: extra=true\n',
    pass.stdout + 'Unexpected trailing output\n',
    pass.stdout.replace('INSTRUMENTATION_CODE: -1', 'INSTRUMENTATION_CODE: -1junk'),
    pass.stdout.replace('INSTRUMENTATION_CODE: -1', 'INSTRUMENTATION_RESULT: stream=PASS: duplicate\nINSTRUMENTATION_CODE: -1'),
    'INSTRUMENTATION_RESULT: stream=PASS: fixture\n',
  ]) {
    assert.equal(instrumentationPassed(stdout), false, stdout);
    const result = await mock({ result: { status: 0, stdout } });
    assert.equal(result.status, 1, stdout);
    assert.ok(result.events.indexOf('stop dev.persona.watch.test') < result.events.indexOf('probe instrumentation'));
    assert.ok(result.events.indexOf('UiAutomation gone') < result.events.indexOf('restore rotation'));
  }
});

test('ETIMEDOUT with an already exited child stops test, then identified target, and waits before restore', async () => {
  const result = await mock({ result: { status: 0, error: Object.assign(new Error('timeout'), { code: 'ETIMEDOUT' }), stdout: '' }, needsTargetStop: true });
  assert.equal(result.status, 1);
  assert.equal(result.rotation, '0');
  const events = result.events;
  assert.ok(events.indexOf('stop dev.persona.watch.test') < events.indexOf('probe instrumentation'));
  assert.ok(events.indexOf('probe instrumentation') < events.indexOf('stop dev.persona.watch'));
  assert.ok(events.indexOf('stop dev.persona.watch') < events.indexOf('UiAutomation gone'));
  assert.ok(events.indexOf('UiAutomation gone') < events.indexOf('restore rotation'));
  assert.match(result.errors.join(''), /interrupted or timed out/);
});

test('a signalled instrumentation result also cleans up and fails', async () => {
  const result = await mock({ result: { status: null, signal: 'SIGTERM', stdout: '' } });
  assert.equal(result.status, 1);
  assert.ok(result.events.includes('stop dev.persona.watch.test'));
  assert.equal(result.rotation, '0');
});

for (const output of ['INSTRUMENTATION_CODE: 0\n', 'INSTRUMENTATION_RESULT: stream=FAIL: fixture\nINSTRUMENTATION_CODE: -1\n']) {
  test(`failure output is nonzero even if adb exits zero: ${output.split('\n')[0]}`, async () => {
    const result = await mock({ result: { status: 0, stdout: output } });
    assert.equal(result.status, 1);
    assert.ok(result.events.includes('stop dev.persona.watch.test'));
    assert.equal(result.rotation, '0');
  });
}

test('persistent automation prevents every rotation restoration write', async () => {
  const result = await mock({ result: { status: 1, stdout: '' }, neverEnds: true });
  assert.equal(result.status, 1);
  assert.equal(result.rotation, '1');
  assert.equal(result.events.includes('restore rotation'), false);
  assert.equal(result.events.filter(event => event === 'read rotation').length, 1);
  assert.match(result.errors.join(''), /shutdown could not be confirmed/);
});

for (const options of [{ unknown: true }, { failedProbe: true }]) {
  test(`unverifiable teardown fails closed: ${JSON.stringify(options)}`, async () => {
    const result = await mock(options);
    assert.equal(result.status, 1);
    assert.equal(result.events.includes('restore rotation'), false);
  });
}

test('unrelated active instrumentation is never force-stopped or overwritten by restoration', async () => {
  const result = await mock({ result: { status: 1, stdout: '' }, unrelatedSession: true });
  assert.equal(result.status, 1);
  assert.equal(result.events.includes('stop dev.persona.watch'), false);
  assert.equal(result.events.some(event => event.includes('example.other')), false);
  assert.equal(result.events.includes('restore rotation'), false);
});

test('target cleanup requires the exact runner and target in the same instrumentation record', async () => {
  assert.equal(hasOwnedInstrumentation(own), true);
  assert.equal(hasOwnedInstrumentation(unrelated), false);
  const mismatched = own.replace('[dev.persona.watch]', '[example.other]')
    + unrelated.replace('Instrumentation #0:', 'Instrumentation #1:').replace('[example.other]', '[dev.persona.watch]');
  assert.equal(hasOwnedInstrumentation(mismatched), false);
  assert.equal(hasOwnedInstrumentation(own.replace('WatchSmokeRunner', 'OtherRunner')), false);
});

test('status probes require recognized dumps and both lifecycle surfaces to be idle', async () => {
  assert.equal(automationActive(processes, accessibility), false);
  assert.equal(automationActive(own, accessibility), true);
  assert.equal(automationActive(processes, accessibility + 'Ui Automation[eventTypes=0]'), true);
  assert.equal(automationActive(processes, accessibility + 'mUiAutomationService=null'), false);
  assert.throws(() => automationActive('', ''), /Unrecognized/);
});

test('explicit service assignment distinguishes null metadata from a live AppUiAutomationConnection', () => {
  const idle = 'serviceClass=AppUiAutomationConnection mUiAutomationService=null';
  assert.equal(automationActive(processes, accessibility + idle), false);
  assert.equal(automationActive(processes, accessibility + 'mUiAutomationService=AppUiAutomationConnection[owner=example.other]'), true);
  assert.equal(automationActive(processes, accessibility + `${idle}\nUi Automation[eventTypes=0]`), true);
  assert.equal(automationActive(processes, accessibility + `${idle}\nmUiAutomationService=OtherConnection[active]`), true);
});

test('unknown or incomplete automation representations cannot establish idle teardown', () => {
  for (const dump of ['mUiAutomationService=', 'UiAutomationState: unknown', 'AppUiAutomationConnection[owner=example.other]',
    'mUiAutomationService=null\nUiAutomationState: unknown',
    'mUiAutomationService=null\nAppUiAutomationConnection[owner=example.other]',
    'mUiAutomationService=null AppUiAutomationConnection[owner=example.other]',
    'mUiAutomationService=null serviceClass=AppUiAutomationConnection[owner=example.other]']) {
    assert.throws(() => automationActive(processes, accessibility + dump), /Unrecognized/);
  }
  assert.equal(automationActive(processes, accessibility + 'mUiAutomationService=nullish'), true);
});

test('a different application automation service must exit before rotation can be restored', async () => {
  const result = await mock({ automationDump: active => active
    ? 'mUiAutomationService=AppUiAutomationConnection[owner=example.other]'
    : 'serviceClass=AppUiAutomationConnection mUiAutomationService=null' });
  assert.equal(result.status, 0);
  assert.equal(result.events.filter(event => event === 'UiAutomation active').length, 2);
  assert.ok(result.events.indexOf('UiAutomation gone') < result.events.indexOf('restore rotation'));
  assert.equal(result.events.some(event => event.startsWith('stop ')), false);
});

test('an unrelated persistent explicit automation service times out without restoration', async () => {
  const result = await mock({ neverEnds: true, automationDump: () => 'mUiAutomationService=AppUiAutomationConnection[owner=example.other]' });
  assert.equal(result.status, 1);
  assert.equal(result.events.includes('restore rotation'), false);
  assert.equal(result.events.some(event => event.startsWith('stop ')), false);
});

test('invalid options fail before any ADB operation', async () => {
  const errors = [];
  assert.equal(await runWatchCheck(['--device', 'TESTWATCH', '--timeout-ms', '0'], {
    spawn: () => assert.fail('No command should run'), stderr: value => errors.push(value),
  }), 1);
  assert.match(errors.join(''), /timeout/);
});

test('ETIMEDOUT plus child status zero produces nonzero process exit with no live handles', async () => {
  const module = new URL('./run-watch-check.mjs', import.meta.url).href;
  const source = `import { runWatchCheck } from ${JSON.stringify(module)};
    const spawn = (_file, args) => {
      const command = args.slice(2);
      const ok = text => ({ status: 0, stdout: Buffer.from(text) });
      if (command[1] === 'wm') return ok('lock 0');
      if (command[1] === 'settings') return ok('0');
      if (command[2] === 'instrument') return { status: 0, stdout: '', error: Object.assign(new Error('timeout'), { code: 'ETIMEDOUT' }) };
      if (command[2] === 'force-stop') return ok('');
      if (command[2] === 'activity') return ok(${JSON.stringify(processes)});
      if (command[2] === 'accessibility') return ok(${JSON.stringify(accessibility)});
      throw new Error('Unexpected fake command');
    };
    process.exitCode = await runWatchCheck(['--device', 'TESTWATCH'], { spawn, stdout: () => {} });`;
  const child = spawnSync(process.execPath, ['--input-type=module', '-e', source], { encoding: 'utf8', windowsHide: true });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /interrupted or timed out/);
});

test('the former exitCode catch also exits nonzero without remaining handles', async () => {
  const child = spawnSync(process.execPath, ['-e', "try { throw new Error('ETIMEDOUT'); } catch (error) { console.error(error.message); process.exitCode = 1; }"],
    { encoding: 'utf8', windowsHide: true });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /ETIMEDOUT/);
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  test(`${signal} interrupts a real mock host child before cleanup can begin`, async () => {
    const signals = new EventEmitter();
    let childClosed = false;
    const result = await runInstrumentation(process.execPath,
      ['-e', "process.stdout.write('READY'); setInterval(() => {}, 1000);"],
      { timeout: 5000, maxBuffer: 1024 }, {
        signals,
        spawnChild: (file, args, options) => {
          const child = spawn(file, args, options);
          child.stdout.once('data', () => signals.emit(signal));
          child.once('close', () => { childClosed = true; });
          return child;
        },
      });
    assert.equal(result.error.code, 'EINTR');
    assert.equal(childClosed, true);
    assert.equal(signals.listenerCount('SIGINT'), 0);
    assert.equal(signals.listenerCount('SIGTERM'), 0);
  });
}

test('an async timeout remains a failure when the mock child subsequently exits zero', async () => {
  const signals = new EventEmitter();
  const child = new EventEmitter();
  child.stdout = new EventEmitter(); child.stderr = new EventEmitter();
  child.kill = () => { queueMicrotask(() => child.emit('close', 0, null)); return true; };
  const result = await runInstrumentation('FAKE_ADB', [], { timeout: 1, maxBuffer: 1024 },
    { signals, spawnChild: () => child });
  assert.equal(result.status, 0);
  assert.equal(result.error.code, 'ETIMEDOUT');
});
