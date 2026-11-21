import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks, stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.endsWith('/Wire') && context.parentURL?.endsWith('/MachineDashboard.ets')) {
      return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
    }
    return next(specifier,context);
  },
  load(url, context, next) {
    if (!url.endsWith('/MachineDashboard.ets') && !url.endsWith('/Wire.ets')) return next(url,context);
    const raw=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(raw),shortCircuit:true};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {MachineReading,machineKey,machineAgents,machineAlerts,machineConcern,machineTrace,
  machineBytes,machinePercent,machineTokens,machineTokenTotal,osFamily}=await import('../entry/src/main/ets/models/MachineDashboard.ets');

test('the dashboard orders concerns exactly as the iPhone and keeps the Hub order for equal priorities',()=>{
  const readings=new MachineReading({system:{cores:4}, latest:{cpu:90,mem_used:80,mem_total:100,disk_used:95,disk_total:100,load:4}});
  const alerts=machineAlerts(readings,readings.system);
  assert.deepEqual(alerts.map(a=>[a.key,a.severity]),[
    ['machine_high_cpu',2],['machine_full_disk',2],['machine_low_memory',1],['machine_high_load',1]
  ]);
  const normal=new MachineReading({latest:{cpu:74.9,mem_total:0,mem_used:999,load:90}});
  assert.deepEqual(machineAlerts(normal,{}),[]);
  const order=[
    ['normal',machineConcern({},true,[],0)],['warn-a',machineConcern({},true,[{severity:1}],0)],
    ['mismatch',machineConcern({node_mismatch:true},true,[],0)],['offline',machineConcern({},false,alerts,1)],
    ['asking',machineConcern({},true,[],1)],['pending',machineConcern({trust:'pending'},false,[],0)],
    ['critical',machineConcern({},true,alerts,1)],['warn-b',machineConcern({},true,[{severity:1}],0)]
  ];
  assert.deepEqual(order.sort((a,b)=>a[1]-b[1]).map(a=>a[0]),
    ['pending','offline','critical','warn-a','warn-b','asking','mismatch','normal']);
});

test('agent totals and detail rows fold only matching parents, preserve waiting subtasks, and demote background-only activity for counts',()=>{
  function row(id,state,more={}) { return ResourceRow.from({session_id:id,machine_id:'mac',status:'idle',state,...more},'session'); }
  const agents=machineAgents([
    row('idle','idle'),row('inflight','working'),row('p','idle',{children:{needs_you:1}}),
    row('folded','working',{folded_under:'p'}),row('local-bg','working',{background_waiting:true}),
    row('other-child','working',{source_kind:'runtime',is_subagent:true,parent_session_id:'else'}),
    row('needs-help','needs_you',{source_kind:'runtime',is_subagent:true,parent_session_id:'else'}),
    row('orphan','working',{folded_under:'missing'}),row('own','working',{assistant:true}),
    row('gone','idle',{status:'offline'}),row('unread','unread',{children:{working:2}}),
    row('with-working-child','idle',{children:{working:1}})
  ]);
  assert.deepEqual(agents.map(a=>a.row.id),['p','needs-help','inflight','local-bg','orphan','with-working-child','idle','unread']);
  assert.equal(agents.find(a=>a.row.id==='local-bg').counted,'idle');
  assert.equal(agents.find(a=>a.row.id==='with-working-child').counted,'working');
  assert.equal(agents.find(a=>a.row.id==='unread').counted,'unread');
});

test('plots use bucket timestamps and memory percentages, retain gaps, separate upload and download and never draw missing values as zero',()=>{
  const sample=new MachineReading({system:{memory:1000},latest:{mem_total:2000}, points:[
    {t:1,mem:2000,rx:8},{t:95,cpu:0,mem:1000,rx:43,tx:17},{t:98,gap:true,mem:1500},
    {t:99,rx:42},{t:100,cpu:37,mem:0,rx:0,tx:0},{t:103,cpu:99}
  ],usage:{recent:[0,7,11]}});
  assert.deepEqual(machineTrace(sample,'mem',7,100).map(v=>[v.time,v.value]),[[95,50],[98,-1],[99,-1],[100,0]]);
  assert.deepEqual(machineTrace(sample,'network',7,100).map(v=>[v.value,v.alternate]),[[43,17],[-1,-1],[42,-1],[0,0]]);
  assert.deepEqual(machineTrace(sample,'cpu',7,100).map(v=>v.value),[0,-1,-1,37]);
  assert.deepEqual(machineTrace(sample,'tokens',3600,661).map(v=>[v.time,v.value]),[[540,0],[600,7],[660,11]]);
});

test('system identity and aggregates match the iOS OS-family and token wire formats',()=>{
  assert.equal(osFamily({distro:'raspbian',like:'debian',os:'linux'},''),'debian');
  assert.equal(osFamily({distro:'unknown',like:'linuxmint debian',os:'linux'},''),'ubuntu');
  assert.equal(osFamily({distro:'nixos',os:'linux'},''),'nixos');
  assert.equal(osFamily({},'darwin'),'macos');
  assert.equal(osFamily({},'unknown'),'');
  assert.equal(machineTokenTotal({in:100,out:50,cache_write:6,cache_read:40,req:4}),196);
  assert.equal(machineTokens(10000),'10K');
  assert.equal(machineBytes(48*2**30),'48.0 GB');
  assert.equal(machinePercent(4.48),'4.5%');
  assert.equal(machinePercent(-1),'—');
  assert.notEqual(machineKey('a','b|c','mac'),machineKey('a|b','c','mac'));
});

const directorySource=readFileSync(new URL('../entry/src/main/ets/components/MachineDirectory.ets',import.meta.url),'utf8');
const loader=directorySource.slice(directorySource.indexOf('  private async load('),directorySource.indexOf('  private chooseRange('));
const {Wire}=await import('../entry/src/main/ets/models/Wire.ets');
const Directory=new Function('Wire','MachineReading','machineKey','RANGES','RANGE_SECONDS','setTimeout',
  'return '+stripTypeScriptTypes('class Directory {'+loader+'}'))(Wire,MachineReading,machineKey,['1h','6h','24h'],[3600,21600,86400],()=>7);

test('a machine detail never installs a late or differently owned measurement when switching chart ranges',async()=>{
  const directory=new Directory(),calls=[],pending=[];
  const machine=ResourceRow.from({id:'mac/one',manager:'assistant',online:true},'machine');machine.group='hub';
  let epoch=2;
  const owner={connected:true,scope:()=> 'hub/assistant',connectionGeneration:()=>epoch,owns:(e,s)=>e===epoch&&s==='hub/assistant',
    repo:()=>({call:url=>new Promise(r=>{calls.push(url);pending.push(r)})})};
  Object.assign(directory,{model:{scope:()=> 'root',connectionGeneration:()=>1,owns:(e,s)=>e===1&&s==='root',directoryOwnerFor:()=>owner},
    revision:1,active:true,selected:machine,range:0});
  const old=directory.load(1,false);directory.range=1;directory.revision=2;const newer=directory.load(2,false);
  pending[1]({metrics:{machine_id:machine.id,latest:{cpu:61}}});await newer;
  pending[0]({metrics:{machine_id:machine.id,latest:{cpu:99}}});await old;
  assert.equal(directory.detailSample.latest.cpu,61);assert.deepEqual(calls,[
    '/api/v1/machines/mac%2Fone/metrics?range=1h','/api/v1/machines/mac%2Fone/metrics?range=6h']);
  const replaced=directory.load(2,false);epoch++;pending[2]({metrics:{machine_id:machine.id,latest:{cpu:10}}});await replaced;
  assert.equal(directory.detailSample.latest.cpu,61);
  const wrong=directory.load(2,false);pending[3]({metrics:{machine_id:'another',latest:{cpu:18}}});await wrong;
  assert.equal(directory.failed,true);assert.equal(directory.detailSample.latest.cpu,61);
});

test('overview keeps a failed manager’s last sample while removing vanished or unlisted machine totals',async()=>{
  const d=new Directory(),row=(group,id)=>{const v=ResourceRow.from({id,manager:'default'},'machine');v.group=group;return v};
  const owner=(group,answer)=>({connected:true,profile:{id:group,manager:'default'},scope:()=>group,connectionGeneration:()=>2,owns:()=>true,repo:()=>({call:answer})});
  const a=owner('home',async()=>({machines:[{machine_id:'mac',latest:{cpu:20}},{machine_id:'unlisted',latest:{cpu:80}}]}));
  const b=owner('travel',async()=>{throw Error('temporarily offline')});
  Object.assign(d,{model:{scope:()=> 'global',connectionGeneration:()=>1,owns:()=>true,directoryOwners:()=>[a,b]},
    visibleRows:[row('home','mac'),row('travel','mac')],active:true,revision:1,range:0,
    readings:new Map([[machineKey('travel','default','mac'),new MachineReading({latest:{cpu:13}})],
      [machineKey('gone','default','mac'),new MachineReading({latest:{cpu:88}})]])});
  await d.load(1,false);
  assert.deepEqual([...d.readings.keys()].sort(),[machineKey('home','default','mac'),machineKey('travel','default','mac')].sort());
  assert.equal(d.readings.get(machineKey('home','default','mac')).latest.cpu,20);
  assert.equal(d.readings.get(machineKey('travel','default','mac')).latest.cpu,13);
});

const chartSource=readFileSync(new URL('../entry/src/main/ets/components/MachineChart.ets',import.meta.url),'utf8');
const scrub=chartSource.slice(chartSource.indexOf('  private scrub('),chartSource.indexOf('  private drag('));
const HistoryChart=new Function('return '+stripTypeScriptTypes('class HistoryChart {'+scrub+'}'))();
const history=directorySource.slice(directorySource.indexOf('  private historyValue('),directorySource.indexOf('  private liveMetrics('));
const HistoryReadout=new Function('Wire','return '+stripTypeScriptTypes('class HistoryReadout {'+history+'}'))(Wire);

test('scrubbing a monitoring gap preserves the requested time instead of presenting a distant measurement',()=>{
  const chart=new HistoryChart(),picked=[];
  Object.assign(chart,{interactive:true,plotWidth:1002,end:2000,seconds:1000,step:10,selectedTime:0,
    points:[{time:1000,value:9,alternate:-1},{time:1400,value:-1,alternate:-1},
      {time:1700,value:-1,alternate:40}],pick:t=>picked.push(t)});
  chart.scrub(11); chart.scrub(401); chart.scrub(685);
  assert.deepEqual(picked,[1000,1400,1700]);
  chart.interactive=false; chart.scrub(11); assert.equal(picked.length,3);
});

test('offline histories retain genuine past readings without advertising the previous connection as live',()=>{
  const history=new HistoryReadout();
  history.latest=()=>12;
  const m=new MachineReading({latest:{net_rx:12,net_tx:14}});
  const points=[{time:700,value:100,alternate:150},{time:800,value:-1,alternate:-1}];
  history.inspected=0;
  assert.equal(history.historyValue(m,'network',points,false),-1);
  assert.equal(history.historyValue(m,'network',points,false,true),-1);
  assert.equal(history.historyValue(m,'network',points,true,true),14);
  history.inspected=700; assert.equal(history.historyValue(m,'network',points,false,true),150);
  history.inspected=800; assert.equal(history.historyValue(m,'network',points,true),-1);
  history.inspected=910; assert.equal(history.historyValue(m,'network',points,true,true),-1);
  assert.equal(history.chartCeiling('cpu',points),100);
  assert.ok(history.chartCeiling('network',points)>150);
});

test('a mounted plot and its value read the accepted sample again after a poll or range switch',()=>{
  const getter=directorySource.slice(directorySource.indexOf('  @Computed get historySeries()'),directorySource.indexOf('  private historyValue('))
    .replace('@Computed ', '');
  const Series=class { constructor(points,ceiling) { this.points=points; this.ceiling=ceiling; } };
  const Mounted=new Function('HISTORY_FIELDS','RANGE_SECONDS','HistorySeries','EMPTY_SERIES','machineTrace','Wire',
    'return '+stripTypeScriptTypes('class Mounted {'+getter+history+'}'))(
    ['cpu','mem','network','load'],[3600,21600,86400],Series,new Series([],1),machineTrace,Wire);
  const view=Object.assign(new Mounted(),{shownRange:0,chartEnd:30000,inspected:0,cards:[{online:true}],
    latest:(m,key)=>m.latest[key],detailSample:new MachineReading({points:[{t:29000,cpu:40}],latest:{cpu:40}})});
  // Evaluate the plot's real points expression through a retained key. An array bound when
  // ForEach first creates its row cannot see the later range or a replacement sample.
  const plot=directorySource.slice(directorySource.indexOf('  @Builder historyPlot('),directorySource.indexOf('  @Builder chartRow('));
  const pointsExpression=/MachineChart\(\{points:([^,]+),/.exec(plot)[1];
  const readPoints=new Function('key','return '+pointsExpression).bind(view,'cpu');
  const readCPU=view.currentChartValue.bind(view,'cpu',false);
  assert.deepEqual(readPoints().map(p=>p.value),[40]);
  assert.equal(readCPU(),40);
  view.shownRange=1;
  view.detailSample=new MachineReading({points:[{t:10000,cpu:35},{t:29000,cpu:83}],latest:{cpu:83}});
  assert.deepEqual(readPoints().map(p=>p.value),[35,83]);
  assert.equal(readCPU(),83);
  view.inspected=10000; assert.equal(readCPU(),35);
  view.detailSample=undefined;
  assert.deepEqual(readPoints(),[]); assert.equal(readCPU(),-1);
});

test('the monitor keeps its six-hour clock grid aligned as a new CPU sample arrives', async () => {
  const {dashboardTimeTicks} = await import('../entry/src/main/ets/models/MachineDashboard.ets');
  const end = new Date(2026, 10, 21, 20, 37).getTime() / 1000;
  const ticks = dashboardTimeTicks(end, 21600);
  assert.equal(ticks.length, 6);
  assert.deepEqual(ticks.map(t => new Date(t*1000).getHours()), [15,16,17,18,19,20]);
  assert.ok(ticks.every(t => new Date(t*1000).getMinutes() === 0));
  assert.deepEqual(dashboardTimeTicks(end+15,21600),ticks);
  assert.deepEqual(dashboardTimeTicks(end,0),[]);
  for (const seconds of [3600,21600,86400]) {
    assert.ok(dashboardTimeTicks(end,seconds).every(t => t>=end-seconds && t<=end));
  }
});
