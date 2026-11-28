#!/usr/bin/env node
// Resolve a previously paired watch without pinning its changing TLS debug port.
import { spawnSync } from 'node:child_process';
import { isIP } from 'node:net';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const usage = 'Usage: node connect-watch.mjs --serial <hardware-serial> [--adb <adb-executable>]';
const tlsSuffix = '._adb-tls-connect._tcp';

function validSerial(serial) {
  return typeof serial === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(serial);
}

function matchesInstance(instance, serial) {
  const escaped = serial.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Bonjour can retain an old advertisement and rename a new one with " (2)".
  return new RegExp(`^adb-${escaped}-[A-Za-z0-9_-]+(?: \\([1-9][0-9]*\\))?$`).test(instance);
}

function instanceForTransport(transport) {
  const name = transport.replace(/\.local\.?$/, '');
  return name.endsWith(tlsSuffix) ? name.slice(0, -tlsSuffix.length) : null;
}

function validEndpoint(endpoint) {
  const match = /^(?:([0-9.]+)|\[([0-9A-Fa-f:]+(?:%[A-Za-z0-9_.-]+)?)\]):([0-9]{1,5})$/.exec(endpoint);
  return Boolean(match && isIP(match[1] || match[2]) && Number(match[3]) > 0 && Number(match[3]) <= 65535);
}

export function parseDevices(output) {
  return output.split(/\r?\n/).flatMap(line => {
    const match = /^(.+?)\s+(device|offline|unauthorized)(?:\s|$)/.exec(line.trim());
    return match ? [{ transport: match[1], state: match[2] }] : [];
  });
}

export function parseTlsServices(output, serial) {
  if (!validSerial(serial)) throw new Error('A valid hardware serial is required.');
  return output.split(/\r?\n/).flatMap(line => {
    // Service instance names can contain spaces; do not split into fixed columns.
    const match = /^(.+?)\s+_adb-tls-connect\._tcp\.?\s+(\S+)\s*$/.exec(line.trim());
    return match && matchesInstance(match[1], serial) && validEndpoint(match[2])
      ? [{ instance: match[1], endpoint: match[2] }] : [];
  });
}

/** run is injectable so discovery and identity checks can be tested without a device. */
export function connectWatch({ serial, run }) {
  if (!validSerial(serial)) throw new Error('A valid hardware serial is required.');
  const devices = parseDevices(run(['devices', '-l']));
  const online = devices.filter(device => device.state === 'device');
  const verified = (transport, claimedIdentity = true) => {
    let actual;
    try { actual = run(['-s', transport, 'shell', 'getprop', 'ro.serialno']).trim(); }
    catch { return false; }
    if (actual !== serial) {
      if (claimedIdentity) throw new Error('A discovered connection belongs to a different device; refusing to use it.');
      return false;
    }
    return true;
  };

  // An existing named TLS transport is preferable to another connection.
  const named = online.filter(device => {
    const instance = instanceForTransport(device.transport);
    return instance != null && matchesInstance(instance, serial);
  });
  if (named.length > 4) throw new Error('Too many matching watch transports; resolve stale discovery entries first.');
  for (const device of named) if (verified(device.transport)) return device.transport;

  // Online IP transports need no current advertisement. Read identity only; never select another device.
  for (const device of online) {
    if (validEndpoint(device.transport) && verified(device.transport, false)) return device.transport;
  }

  let services;
  try { services = parseTlsServices(run(['mdns', 'services']), serial); }
  catch (error) { throw new Error('ADB mDNS discovery failed; check wireless debugging and the local network.', { cause: error }); }
  const endpoints = [...new Set(services.map(service => service.endpoint))];
  if (endpoints.length > 4) throw new Error('Too many matching watch advertisements; resolve stale discovery entries first.');

  // A newly discovered endpoint must still belong to this watch before it can be used.
  for (const endpoint of endpoints) {
    if (online.some(device => device.transport === endpoint) && verified(endpoint)) return endpoint;
  }
  for (const endpoint of endpoints) {
    try { run(['connect', endpoint]); }
    catch { continue; }
    if (verified(endpoint)) return endpoint;
  }
  throw new Error('No reachable paired watch was found. Wake it, enable wireless debugging on the same Wi-Fi, and retry. If mDNS is unavailable, read the current debug address on the watch.');
}

export function parseOptions(args) {
  const options = new Map();
  for (let i = 0; i < args.length; i += 2) {
    if (!['--serial', '--adb'].includes(args[i]) || !args[i + 1] || options.has(args[i])) throw new Error(usage);
    options.set(args[i], args[i + 1]);
  }
  if (!validSerial(options.get('--serial'))) throw new Error(usage);
  return { serial: options.get('--serial'), adb: options.get('--adb') || 'adb' };
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    if (process.argv.length === 3 && process.argv[2] === '--help') {
      console.log(usage);
    } else {
      const { adb, serial } = parseOptions(process.argv.slice(2));
      const run = args => {
        const result = spawnSync(adb, args, { windowsHide: true, timeout: 3000, maxBuffer: 1024 * 1024, encoding: 'utf8' });
        if (result.error || result.status !== 0) throw new Error('ADB discovery or connection command failed.');
        return result.stdout;
      };
      // Standard output contains only the verified -s target, suitable for shell assignment.
      console.log(connectWatch({ serial, run }));
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
