#!/usr/bin/env node
// Restore rotation only after instrumentation and its UiAutomation connection have ended.
import { spawnSync, spawn as spawnChildProcess } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { withPreservedRotationAsync } from './rotation-guard.mjs';

const TEST_PACKAGE = 'dev.persona.watch.test';
const TARGET_PACKAGE = 'dev.persona.watch';
const CHECK_MODES = new Set(['fixture', 'multihub', 'surfaces', 'pet_expiry', 'pet_expiry_restore',
  'complication_parcel', 'real_surfaces', 'live_ping']);
const pollingCell = new Int32Array(new SharedArrayBuffer(4));

/** Keep Node responsive to interruption while the host ADB child owns instrumentation. */
export function runInstrumentation(file, args, { timeout, maxBuffer }, { spawnChild = spawnChildProcess, signals = process } = {}) {
  return new Promise(resolve => {
    const output = [], errors = [];
    let child, failure, timer, killTimer, settled = false, bytes = 0;
    const stop = error => {
      failure ??= error;
      child?.kill('SIGTERM');
      killTimer ??= setTimeout(() => child?.kill('SIGKILL'), 1000);
    };
    const interrupt = () => stop(Object.assign(new Error('Instrumentation interrupted'), { code: 'EINTR' }));
    const finish = (status, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer); clearTimeout(killTimer);
      signals.off('SIGINT', interrupt); signals.off('SIGTERM', interrupt);
      resolve({ status, signal, error: failure, stdout: Buffer.concat(output).toString('utf8'), stderr: Buffer.concat(errors).toString('utf8') });
    };
    const collect = target => data => {
      bytes += data.length;
      if (bytes > maxBuffer) stop(Object.assign(new Error('Instrumentation output limit exceeded'), { code: 'ENOBUFS' }));
      else target.push(Buffer.from(data));
    };
    try {
      child = spawnChild(file, args, { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
      signals.on('SIGINT', interrupt); signals.on('SIGTERM', interrupt);
      child.stdout.on('data', collect(output)); child.stderr.on('data', collect(errors));
      child.once('error', error => { failure ??= error; finish(null, null); });
      child.once('close', finish);
      timer = setTimeout(() => stop(Object.assign(new Error('Instrumentation timed out'), { code: 'ETIMEDOUT' })), timeout);
    } catch (error) { failure = error; finish(null, null); }
  });
}

/** Unknown/failed dumps cannot establish teardown. Never print their potentially private contents. */
export function automationActive(processes, accessibility) {
  if (!/^ACTIVITY MANAGER RUNNING PROCESSES\b/m.test(processes) ||
      !/^ACCESSIBILITY MANAGER\b/m.test(accessibility)) throw new Error('Unrecognized automation status');
  const hasInstrumentation = /Active instrumentation:|Instrumentation #/i.test(processes);
  // AOSP emits this record only for an active service. Vendor dumps may instead expose its field.
  if (/(?:^|[\s,{])Ui Automation\[eventTypes=/m.test(accessibility)) return true;
  const fields = [...accessibility.matchAll(/\bmUiAutomationService\s*=\s*([^\s,;\]}]+)/g)];
  if (fields.some(([, value]) => value !== 'null')) return true;
  const unparsed = accessibility.split(/\r?\n/).map(line => {
    if (!/\bmUiAutomationService\s*=\s*null(?=$|[\s,;\]}])/.test(line)) return line;
    // Ignore only a labeled class name on this null-service line, never a separate instance record.
    return line.replace(/\bmUiAutomationService\s*=\s*null\b/g, '')
      .replace(/\b(?:serviceClass|class)\s*=\s*AppUiAutomationConnection(?=$|[\s,;\]}])/g, '');
  }).join('\n');
  if (/Ui\s*Automation/i.test(unparsed)) throw new Error('Unrecognized UiAutomation status');
  return hasInstrumentation;
}

/** Android can host instrumentation in the target process. Only this exact runner/target may be stopped. */
export function hasOwnedInstrumentation(processes) {
  return processes.split(/^\s*Instrumentation #\d+:/m).slice(1).some(block =>
    /mClass=ComponentInfo\{dev\.persona\.watch\.test\/dev\.persona\.watch\.WatchSmokeRunner\}/.test(block) &&
    /^\s*mTargetProcesses=\[dev\.persona\.watch\]\s*$/m.test(block));
}

export function waitForAutomationExit(run, {
  now = () => performance.now(),
  wait = milliseconds => Atomics.wait(pollingCell, 0, 0, milliseconds),
  timeoutMs = 15000,
  stopOwnedTarget = false,
} = {}) {
  const deadline = now() + timeoutMs;
  let targetStopped = false;
  for (;;) {
    const remaining = deadline - now();
    if (remaining <= 0) throw new Error('Instrumentation or UiAutomation did not finish');
    const processes = run(['shell', 'dumpsys', 'activity', 'processes'], Math.min(remaining, 2000)).toString('utf8');
    const accessibility = run(['shell', 'dumpsys', 'accessibility'], Math.min(Math.max(deadline - now(), 1), 2000)).toString('utf8');
    if (!automationActive(processes, accessibility)) return;
    if (stopOwnedTarget && !targetStopped && hasOwnedInstrumentation(processes)) {
      run(['shell', 'am', 'force-stop', TARGET_PACKAGE], Math.min(Math.max(deadline - now(), 1), 2000));
      targetStopped = true;
    }
    const delay = Math.min(deadline - now(), 100);
    if (delay > 0) wait(delay);
  }
}

/** Parse raw am instrument records; continuation lines belong to their current bundle value. */
export function instrumentationPassed(output, mode) {
  if (mode !== undefined && !CHECK_MODES.has(mode)) return false;
  const result = new Map(), progress = new Map();
  const failed = fields => /^\s*FAIL:/.test(fields.get('stream') || '') ||
    ['shortMsg', 'longMsg', 'Error', 'stack'].some(key => fields.get(key)?.trim());
  let current = null, statusOpen = false, resultStarted = false, code = null;
  for (const line of output.toString('utf8').split(/\r?\n/)) {
    const field = /^INSTRUMENTATION_(RESULT|STATUS): ([A-Za-z0-9_]+)=(.*)$/.exec(line);
    if (field) {
      if (code !== null || field[1] === 'STATUS' && resultStarted || field[1] === 'RESULT' && statusOpen) return false;
      if (field[1] === 'RESULT') {
        resultStarted = true;
        if (result.has(field[2])) return false;
        result.set(field[2], field[3]);
      } else {
        if (progress.has(field[2])) return false;
        progress.set(field[2], field[3]); statusOpen = true;
      }
      current = { type: field[1], key: field[2] };
      continue;
    }
    const status = /^INSTRUMENTATION_STATUS_CODE: (-?[0-9]+)$/.exec(line);
    if (status) {
      if (code !== null || resultStarted || !statusOpen || Number(status[1]) < 0 || failed(progress)) return false;
      statusOpen = false; current = null; progress.clear();
      continue;
    }
    const final = /^INSTRUMENTATION_CODE: (-?[0-9]+)$/.exec(line);
    if (final) {
      if (code !== null || statusOpen) return false;
      code = Number(final[1]); current = null;
      continue;
    }
    // Framework failures, unknown records and malformed terminators cannot be payload continuations.
    if (/^INSTRUMENTATION_/.test(line)) return false;
    if (current?.type === 'RESULT') result.set(current.key, `${result.get(current.key)}\n${line}`);
    else if (current?.type === 'STATUS') progress.set(current.key, `${progress.get(current.key)}\n${line}`);
    else if (current == null && line.trim() !== '') return false;
  }
  if (code !== -1 || statusOpen || failed(result)) return false;
  // These existing modes report a bounded observation/setup outcome, not a completed face test.
  const outcome = new Map([
    ['surfaces', ['surfaces_passed', 'Synthetic app surfaces passed:']],
    ['real_surfaces', ['real_surfaces_probed', 'OBSERVED:']],
    ['pet_expiry', ['pet_expiry_seeded', 'SEEDED:']],
    ['pet_expiry_restore', ['pet_expiry_restored', 'RESTORED:']],
  ]).get(mode);
  const stream = (result.get('stream') || '').trimStart();
  return outcome ? result.get(outcome[0])?.trim() === 'true' && stream.startsWith(outcome[1]) : stream.startsWith('PASS:');
}

/** Dependency injection is for host-side tests; importing this module never invokes ADB. */
export async function runWatchCheck(args, {
  spawn = spawnSync,
  instrument = spawn === spawnSync ? runInstrumentation : spawn,
  stdout = value => process.stdout.write(value),
  stderr = value => process.stderr.write(value),
  cleanup = {},
} = {}) {
  try {
    const supported = new Set(['--adb', '--device', '--mode', '--scenario', '--secondary-url', '--session-id', '--timeout-ms']);
    const options = new Map();
    for (let i = 0; i < args.length; i += 2) {
      if (!supported.has(args[i]) || !args[i + 1] || options.has(args[i])) throw new Error('Invalid or repeated check option');
      options.set(args[i], args[i + 1]);
    }
    const mode = options.get('--mode');
    if (mode !== undefined && !CHECK_MODES.has(mode)) throw new Error('Unsupported check mode');
    const adb = options.get('--adb') || 'adb';
    const device = options.get('--device');
    const timeout = Number(options.get('--timeout-ms') || 180000);
    if (!device || !Number.isInteger(timeout) || timeout < 1000 || timeout > 900000) {
      throw new Error('A device and a timeout from 1000 to 900000 milliseconds are required');
    }
    function run(command, commandTimeout = 10000) {
      const result = spawn(adb, ['-s', device, ...command], { windowsHide: true, timeout: commandTimeout, maxBuffer: 1024 * 1024 });
      if (result.error || result.signal || result.status !== 0) throw new Error('ADB preference or cleanup operation failed');
      return result.stdout;
    }
    const command = ['shell', 'am', 'instrument', '-w', '-r'];
    for (const [option, key, valid] of [
      ['--mode', 'mode', /^[a-z_]+$/],
      ['--scenario', 'scenario', /^[a-z_,]+$/],
      ['--secondary-url', 'secondary_url', /^[A-Za-z0-9._:/\[\]-]+$/],
      ['--session-id', 'session_id', /^[A-Za-z0-9._:-]+$/],
    ]) {
      const value = options.get(option);
      if (value != null) {
        if (!valid.test(value)) throw new Error(`Invalid ${option} value`);
        command.push('-e', key, value);
      }
    }
    command.push(`${TEST_PACKAGE}/dev.persona.watch.WatchSmokeRunner`);
    let passed = false;
    await withPreservedRotationAsync(run, async () => {
      try {
        const result = await instrument(adb, ['-s', device, ...command], { windowsHide: true, timeout, maxBuffer: 4 * 1024 * 1024, encoding: 'utf8' });
        if (result.stdout) stdout(result.stdout);
        if (result.stderr) stderr(result.stderr);
        if (result.error || result.signal || result.status !== 0) throw new Error('Instrumentation interrupted or timed out');
        if (!instrumentationPassed(result.stdout || '', mode)) {
          throw new Error('Instrumentation did not report a passing result');
        }
        passed = true;
      } finally {
        // Stopping this package alone is not proof that Android has disconnected UiAutomation.
        if (!passed) run(['shell', 'am', 'force-stop', TEST_PACKAGE]);
      }
    }, () => waitForAutomationExit(run, { ...cleanup, stopOwnedTarget: !passed }));
    return 0;
  } catch (error) {
    stderr(`${error.message}\n`);
    return 1;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = await runWatchCheck(process.argv.slice(2));
}
