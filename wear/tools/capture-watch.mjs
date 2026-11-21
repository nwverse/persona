#!/usr/bin/env node
// Captures a physical watch screen without changing the current app or its data.
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { withPreservedRotation } from './rotation-guard.mjs';

const args = process.argv.slice(2);
const option = name => {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
};
const adb = option('--adb') || 'adb';
const device = option('--device');
const output = option('--out');
const screenshotOnly = args.includes('--screenshot-only');
if (!device || !output) throw new Error('Usage: node capture-watch.mjs --device <ADB serial> --out <output basename> [--adb <adb path>] [--screenshot-only]');
const base = resolve(output.replace(/\.(png|xml)$/i, ''));
mkdirSync(dirname(base), { recursive: true });
function run(command, timeout = 15000) {
  const result = spawnSync(adb, ['-s', device, ...command], { windowsHide: true, timeout, maxBuffer: 12 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error('ADB capture failed or timed out; check that the watch is connected and awake');
  return result.stdout;
}
const png = run(['exec-out', 'screencap', '-p']);
if (!png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) throw new Error('ADB did not return a PNG screenshot');
writeFileSync(`${base}.png`, png);
console.log(`Screenshot saved: ${base}.png`);
if (!screenshotOnly) {
  const remotePath = `/data/local/tmp/persona-watch-window-${process.pid}.xml`;
  try {
    withPreservedRotation(run, () => {
      // Terminate the remote UI automation before the host timeout and preference restore.
      run(['shell', 'toybox', 'timeout', '-s', 'KILL', '18', 'uiautomator', 'dump', remotePath], 25000);
      const xml = run(['exec-out', 'cat', remotePath], 10000);
      if (!xml.toString('utf8').includes('<hierarchy')) throw new Error('No accessibility hierarchy was returned');
      writeFileSync(`${base}.xml`, xml);
      console.log(`Accessibility tree saved: ${base}.xml`);
    });
  } catch (error) {
    console.error(`${error.message}. The screenshot above is still available.`);
    process.exitCode = 1;
  } finally {
    spawnSync(adb, ['-s', device, 'shell', 'rm', '-f', remotePath], { windowsHide: true, timeout: 5000, stdio: 'ignore' });
  }
}
