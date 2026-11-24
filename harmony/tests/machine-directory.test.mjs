import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === './Wire' && context.parentURL?.endsWith('/ScreenConnectionInfo.ets'))
      return { url: new URL('Wire.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.endsWith('/Wire.ets') || url.endsWith('/ScreenConnectionInfo.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', shortCircuit: true, source: stripTypeScriptTypes(source) };
    }
    return next(url, context);
  }
});
const { ResourceRow, Wire } = await import('../entry/src/main/ets/models/Wire.ets');
const { screenConnectionInfo } = await import('../entry/src/main/ets/models/ScreenConnectionInfo.ets');
const source=readFileSync(new URL('../entry/src/main/ets/components/SettingsPage.ets',import.meta.url),'utf8');
function method(name){const start=source.search(new RegExp('^  private (?:async )?'+name+'\\(','m'));assert.ok(start>=0,name);const body=source.indexOf('{',start);let depth=1,quote='',escape=false,index=body+1;for(;index<source.length&&depth;index++){const c=source[index];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}return source.slice(start,index);}
const Page=new Function('Wire','ResourceRow','screenConnectionInfo','return '+stripTypeScriptTypes('class Page{'+['chooseMachine','machineRow','screenMachine','supportsScreen','canOpenScreen'].map(method).join('\n')+'}'))(Wire,ResourceRow,screenConnectionInfo);
function deferred(){let resolve;return{promise:new Promise(r=>resolve=r),resolve:value=>resolve(value)};}
function fixture(){const page=new Page(),calls=[];let generation=1;
 const machine=(group)=>{const row=ResourceRow.from({id:'same-mac',manager:'work',online:true,trust:'confirmed',os:'mac'},'machine');row.group=group;return row;};
 const a=machine('hub-a'),b=machine('hub-b');const owner=row=>({profile:{id:row.group,manager:'work'},connected:true,demo:false,machines:[row],scope:()=>row.group,connectionGeneration:()=>generation,pairingRevision:()=>0});const owners={a:owner(a),b:owner(b)};
 page.model={demo:false,connectionGeneration:()=>generation,scope:()=> 'root',owns:(g,s)=>g===generation&&s==='root',viewFor:async row=>row.group==='hub-b'?owners.b:owners.a};
 page.management={loading:false,rows:[],setAdmission(admit){this.admit=admit;},async load(_page,owner){calls.push(owner);this.rows=owner.machines.map(row=>({id:row.id,raw:row.data()}));}};page.mounted=true;page.active=true;page.machineRevision=0;
 return{page,calls,a,b,owners,bump:()=>generation++};}
test('machine details and screen eligibility use the selected Hub despite duplicate machine ids',async()=>{const f=fixture();await f.page.chooseMachine(f.b);assert.equal(f.page.machineOwner,f.owners.b);assert.equal(f.calls[0],f.owners.b);assert.equal(f.page.screenMachine().group,'hub-b');assert.equal(f.page.canOpenScreen(),true);f.owners.b.connected=false;assert.equal(f.page.canOpenScreen(),false);});
test('a late owner lookup cannot replace the current machine or revive a hidden page',async()=>{const f=fixture(),gate=deferred();f.page.model.viewFor=row=>row.group==='hub-a'?gate.promise:Promise.resolve(f.owners.b);const old=f.page.chooseMachine(f.a);await f.page.chooseMachine(f.b);gate.resolve(f.owners.a);await old;assert.equal(f.page.machineOwner,f.owners.b);assert.deepEqual(f.calls,[f.owners.b]);const hidden=deferred();f.page.model.viewFor=()=>hidden.promise;const next=f.page.chooseMachine(f.a);f.page.active=false;hidden.resolve(f.owners.a);await next;assert.deepEqual(f.calls,[f.owners.b]);});

test('choosing an unavailable Hub clears the former machine and its pair code before lookup',async()=>{const f=fixture();await f.page.chooseMachine(f.a);f.page.management.pairing={pair_url:'synthetic-owner-a-code',machine:f.a.id};f.page.pairingOwnerScope='hub-a';f.page.computerName='Old draft';f.page.model.viewFor=async()=>{throw Error('offline');};await f.page.chooseMachine(f.b);assert.equal(f.page.machineOwner,undefined);assert.equal(f.page.screenMachine(),undefined);assert.equal(f.page.canOpenScreen(),false);assert.equal(f.page.management.selectedId,'');assert.deepEqual(f.page.management.pairing,{});assert.equal(f.page.computerName,'');assert.equal(f.page.pairingOwnerScope,'');});

test('an unqualified legacy row can open only the selected owner’s screen', async () => {
 const f = fixture();
 f.owners.b.profile = { id: 'hub-b', manager: 'work' };
 const local = ResourceRow.from(f.b.data(), 'machine');
 f.owners.b.machines = [local];
 await f.page.chooseMachine(f.b);
 assert.equal(f.page.screenMachine().group, 'hub-b');
 assert.equal(local.group, '');
 f.owners.b.profile = { id: 'hub-a', manager: 'work' };
 assert.equal(f.page.canOpenScreen(), false);
});

test('returning from a conversation reuses the same confirmed machine and renews admission for this visit', async () => {
  const { page, b, calls, owners, bump } = fixture();
  await page.chooseMachine(b);
  const originalRows = page.management.rows, previousAdmission = page.management.admit;
  page.detailOwner = owners.b;
  page.machineRevision++;
  assert.equal(previousAdmission(), false);
  await page.chooseMachine(b, true);
  assert.equal(calls.length, 1);
  assert.equal(page.management.rows, originalRows);
  assert.equal(page.management.admit(), true);
  bump();
  assert.equal(page.management.admit(), false);
  await page.chooseMachine(b, true);
  assert.equal(calls.length, 2, 'a new connection must verify management actions again');
});

test('an unavailable screen status lookup does not block loading the machine detail', async () => {
  const f = fixture();
  let attempted = 0;
  f.owners.b.repo = () => { attempted++; throw new Error('connection changed'); };
  await f.page.chooseMachine(f.b);
  assert.equal(attempted, 1);
  assert.deepEqual(f.calls, [f.owners.b]);
  assert.equal(f.page.management.selectedId, f.b.id);
});
