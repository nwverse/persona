import test from 'node:test';
import assert from 'node:assert/strict';
import {sessionComposerIdentity} from '../entry/src/main/ets/core/ComposerIdentity.ts';

test('the strip shows the agent brand, model, effort and acknowledged speed', () => {
  const view = sessionComposerIdentity({agent:'opencode', model:'gateway/gpt-6.2-astra', effort:'ultra', service_tier:'priority'});
  assert.deepEqual({...view}, {agent:'OpenCode',model:'gateway/gpt-6.2-astra',brand:'opencode',effort:'ultra',speed:'fast'});
});

test('unknown speed stays empty, explicit named tiers use their recorded display label', () => {
  const session={agent:'codex', service_tiers:[{id:'batch',name:'Batch'}]};
  assert.equal(sessionComposerIdentity(session).speed,'');
  assert.equal(sessionComposerIdentity({...session, service_tier:'batch'}).speed,'Batch');
  assert.equal(sessionComposerIdentity({service_tier:'batch', service_tiers:[{id:'batch',name:'批量'}]}).speed,'batch');
});

test('every accelerated Codex tier reads fast and standard stays hidden', () => {
  for (const tier of ['default','standard','']) assert.equal(sessionComposerIdentity({agent:'codex',service_tier:tier}).speed,'');
  for (const tier of ['fast','priority','ultrafast']) assert.equal(sessionComposerIdentity({agent:'codex',service_tier:tier}).speed,'fast');
});
