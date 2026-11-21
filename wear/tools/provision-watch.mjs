#!/usr/bin/env node
// Debug-only import: credentials travel over the already authenticated ADB connection on stdin.
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const args = process.argv.slice(2);
const option = name => {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
};
const adb = option('--adb') || 'adb';
const device = option('--device');
const configPath = option('--config');
if (!device || !configPath) throw new Error('Usage: node provision-watch.mjs --device <ADB serial> --config <private JSON file> [--adb <adb path>]');
let config;
try { config = JSON.parse(readFileSync(configPath, 'utf8').replace(/^\uFEFF/, '')); }
catch { throw new Error('Cannot read the private JSON configuration'); }
if (!config || typeof config.baseUrl !== 'string' || typeof config.token !== 'string' || !config.token.trim()) {
  throw new Error('Configuration requires baseUrl and token');
}
const payload = { baseUrl: config.baseUrl, token: config.token };
if (typeof config.fingerprint === 'string' && config.fingerprint) payload.fingerprint = config.fingerprint;
if (typeof config.deviceId === 'string' && config.deviceId) payload.deviceId = config.deviceId;
function run(command, input) {
  const result = spawnSync(adb, ['-s', device, ...command], { input, encoding: 'utf8', windowsHide: true, timeout: 20000 });
  if (result.error || result.status !== 0) throw new Error('ADB operation failed; check the target device and installed debug application');
  return result.stdout || '';
}
run(['shell', 'am', 'force-stop', 'dev.persona.watch']);
run(['shell', '-T', "run-as dev.persona.watch sh -c 'umask 077; mkdir -p files; cat > files/debug-pairing.json'"], JSON.stringify(payload));
const start = run(['shell', 'am', 'start', '-n', 'dev.persona.watch/.MainActivity', '--ez', 'import_pairing', 'true']);
if (/Error:|Exception/i.test(start)) throw new Error('Android could not start the debug import');
console.log('Private pairing file delivered. Check the watch for a successful connection; this command alone does not verify pairing.');
