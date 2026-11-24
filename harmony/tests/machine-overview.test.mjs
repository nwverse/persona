import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(spec, ctx, next) {
    if (ctx.parentURL?.endsWith('.ets') && spec.startsWith('.'))
      return { url: new URL(spec + '.ets', ctx.parentURL).href, shortCircuit: true };
    return next(spec, ctx);
  },
  load(url, ctx, next) {
    if (!url.endsWith('.ets')) return next(url, ctx);
    const src = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*|@Trace\s+/g, '');
    return { format: 'module', source: stripTypeScriptTypes(src), shortCircuit: true };
  }
});
const { machineCards, MachineReadings, MachineReading } = await import('../entry/src/main/ets/models/MachineDirectory.ets');
const { ResourceRow } = await import('../entry/src/main/ets/models/Wire.ets');
function row(kind, group, fields) {
  const value = ResourceRow.from(fields, kind); value.group = group; return value;
}

test('the roster keeps Hub and manager boundaries, attention order and live top-level agents', () => {
  const mac = row('machine','a',{ id:'shared', manager:'work', name:'Mac', online:true });
  const remote = row('machine','b',{ id:'shared', manager:'work', online:false });
  const pending = row('machine','a',{ id:'pending', trust:'pending', online:true });
  const agent = fields => row('session','a',{ id:'j', machine_id:'shared', manager:'work', state:'working', ...fields });
  const cards = machineCards([mac,remote,pending], [
    agent({}), agent({ manager:'other' }), agent({ status:'offline' }), agent({ assistant:true }),
    agent({ source_kind:'runtime',parent_session_id:'parent' }), agent({ background_waiting:true }),
    agent({ source_kind:'runtime',parent_session_id:'parent',state:'needs_you' })
  ],'a','work');
  assert.deepEqual(cards.map(c => c.row),[pending,remote,mac]);
  assert.equal(cards[1].agents.length,0);
  assert.equal(cards[2].agents.length,3); assert.equal(cards[2].working,1); assert.equal(cards[2].needs,1);
});

test('missing metrics and zero load remain distinct; independent Hubs keep their own samples', async () => {
  assert.equal(new MachineReading({}).hasData(),false);
  const zero=new MachineReading({ cpu:0, mem_total:10, mem_used:0, net_rx:0 });
  assert.equal(zero.hasData(),true); assert.equal(zero.memory,0); assert.equal(zero.tx,-1);
  const a=row('machine','a',{ id:'same' }), b=row('machine','b',{ id:'same' });
  const view = (id,percent) => ({ profile:{ id }, connected:true, scope:()=>id, connectionGeneration:()=>1,
    pairingRevision:()=>2, owns:(gen,scope)=>gen===1&&scope===id,
    repo:()=>({ call:async path => {assert.equal(path,'/api/v1/metrics');return { machines:[{machine_id:'same',online:true,latest:{cpu:percent}}] };} }) });
  const root=view('a',10), other=view('b',90);
  root.viewFor=async machine=>machine.group==='b'?other:root;
  const metrics=new MachineReadings();
  try {
    metrics.start(root,()=>[a,b]); await setImmediate();
    assert.equal(metrics.values.get(a.key()).cpu,10);assert.equal(metrics.values.get(b.key()).cpu,90);
    const delayed=Promise.withResolvers();root.repo=()=>({call:()=>delayed.promise});
    metrics.start(root,()=>[a]); await setImmediate();metrics.stop();
    delayed.resolve({machines:[{machine_id:'same',online:true,latest:{cpu:85}}]});await setImmediate();
    assert.equal(metrics.values.size,0);
  } finally { metrics.stop(); }
});

test('returning to Machines preserves the visible reading but rejects old requests and pairings', async () => {
  const machine = row('machine', 'first', { id: 'workstation' });
  let pairing = 1;
  const reading = cpu => ({ machines: [{ machine_id: machine.id, online: true, latest: { cpu } }] });
  const owner = {
    profile: { id: 'first' }, connected: true, scope: () => 'first', connectionGeneration: () => 1,
    owns: () => true, pairingRevision: () => pairing, viewFor: async () => owner,
    repo: () => ({ call: async () => reading(24) })
  };
  const monitor = new MachineReadings();
  const pending = Promise.withResolvers();
  try {
    monitor.start(owner, () => [machine]);
    await setImmediate();
    owner.repo = () => ({ call: () => pending.promise });
    monitor.start(owner, () => [machine]);
    await setImmediate();
    monitor.pause();
    pending.resolve(reading(91));
    await setImmediate();
    assert.equal(monitor.sample(owner, machine).cpu, 24);
    assert.equal(monitor.sample({ ...owner }, machine), undefined);
    pairing++;
    assert.equal(monitor.sample(owner, machine), undefined);
    monitor.stop();
    assert.equal(monitor.values.size, 0);
  } finally {
    monitor.stop();
  }
});
