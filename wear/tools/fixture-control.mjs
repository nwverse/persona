#!/usr/bin/env node
// Controls only the loopback endpoint of the synthetic fixture server.
const args = process.argv.slice(2);
const action = args[0] || 'status';
const index = args.indexOf('--port');
const port = Number(index < 0 ? 18765 : args[index + 1]);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid port');
const controls = {
  reset: { reset: true, clearEvents: true },
  online: { mode: 'online', delayMs: 0 },
  offline: { mode: 'offline' },
  error: { mode: 'error' },
  stale: { mode: 'stale' },
  slow: { mode: 'online', delayMs: 4000 },
};
if (action !== 'status' && !controls[action]) throw new Error('Use status, reset, online, offline, error, stale, or slow');
const response = await fetch(`http://127.0.0.1:${port}/__test`, {
  method: action === 'status' ? 'GET' : 'POST',
  headers: { Authorization: 'Bearer persona-wear-fixture-only', 'Content-Type': 'application/json' },
  body: action === 'status' ? undefined : JSON.stringify(controls[action]),
  signal: AbortSignal.timeout(5000),
});
if (!response.ok) throw new Error(`Fixture control failed: HTTP ${response.status}`);
console.log(JSON.stringify(await response.json(), null, 2));
