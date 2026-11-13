// Scheduled execution form with synthetic HTTP/WS. Never connects to a user's hub or a model.
// node web/e2e/task-execution.mjs <loopback web URL> --shots <directory> --chromium <executable>
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";
const args=process.argv.slice(2);
const flag=(name)=>args[args.indexOf(name)+1];
const base=args[0], shots=flag("--shots");
assert(["localhost","127.0.0.1"].includes(new URL(base).hostname));
mkdirSync(shots,{recursive:true});
const watchdog=setTimeout(()=>{console.error("TIMEOUT task execution verification");process.exit(124);},180_000);
const browser=await chromium.launch({executablePath:flag("--chromium"),headless:true,args:["--no-sandbox"]});
try {
 for(const width of [1280,390]) for(const theme of ["light","dark"]) {
  const context=await browser.newContext({viewport:{width,height:900},colorScheme:theme,reducedMotion:"reduce"});
  await context.addInitScript(({theme})=>{
   localStorage.setItem("persona.token","synthetic-task-token");localStorage.setItem("persona.language","en");localStorage.setItem("persona.theme",theme);
  },{theme});
  let task={id:"job",title:"Review project files",prompt:"Summarize the project files.",repeat:"interval",every_minutes:30,enabled:true,model_revision:0};
  const viewTask=()=>({...task,execution:{machine_id:"box",launcher:task.launcher||"assistant",agent:task.launcher==="codex"?"codex":"claude",model:task.model||"main-model",effort:task.effort||"high"}});
  const writes=[], reads=[];
  await context.route("**/api/v1/**",async(route)=>{
   const req=route.request(),url=new URL(req.url()),path=url.pathname.replace(/^\/api\/v1/,"");
   reads.push(`${req.method()} ${path}`);
   let body={};
   if(path==="/machines")body={machines:[{id:"box",name:"Example computer",online:true}]};
   else if(path==="/sessions")body={sessions:[]};
   else if(path==="/assistant")body={machine_id:"box",main:"",conversations:[],tasks:[],memory:[]};
   else if(path==="/assistant/tasks")body={tasks:[viewTask()]};
   else if(path==="/assistant/runs")body={runs:[]};
   else if(path==="/model-config")body={providers:[{id:"launcher.assistant",name:"Assistant",agent:"claude"},{id:"launcher.codex",name:"agents",agent:"codex"}]};
   else if(path==="/model-config/purposes/assistant")body={purpose:{id:"assistant",machine_id:"box",effective:{provider:"launcher.assistant",model:"main-model",effort:"high"},override:{}},providers:[]};
   else if(path==="/machines/box/launchers")body={launchers:[{name:"assistant",display_name:"Assistant",agent:"claude"},{name:"codex",display_name:"agents",agent:"codex"}]};
   else if(path.startsWith("/model-config/providers/"))body={agent:path.includes("codex")?"codex":"claude",models:[{id:"test-model",name:"Example model",efforts:["low","high"],default_effort:"high"}]};
   else if(path==="/assistant/tasks/job"&&req.method()==="PUT") {const patch=req.postDataJSON();writes.push({path,patch});task={...task,...patch,model_revision:1};body={task:viewTask()};}
   else if(path==="/extensions/ui")body={slots:[]};
   else if(path==="/extensions")body={extensions:[]};
   else if(path==="/managers")body={managers:[]};
   if(!["GET","HEAD"].includes(req.method())&&!path.startsWith("/assistant/tasks/job"))throw new Error(`unexpected write ${req.method()} ${path}`);
   await route.fulfill({contentType:"application/json",body:JSON.stringify(body)});
  });
  await context.routeWebSocket("**/api/v1/ws**",ws=>{ws.send(JSON.stringify({type:"hello",server_version:"synthetic",features:["task_execution","task_cron"]}));ws.onMessage(raw=>{if(JSON.parse(String(raw)).type==="app_ping")ws.send(JSON.stringify({type:"pong"}));});});
  const page=await context.newPage();const errors=[];page.on("pageerror",e=>errors.push(String(e)));page.setDefaultTimeout(10_000);
  await page.goto(`${base}/routines`);
  await page.getByRole("button",{name:/Review project files/}).first().getByText("Assistant · claude · main-model · high",{exact:true}).waitFor();
  await page.getByRole("button",{name:/Review project files/}).first().click();
  const disclosure=page.locator("summary").filter({hasText:"Execution options"});
  await page.getByRole("radio",{name:"Cron",exact:true}).waitFor();
  await disclosure.getByText("Assistant · claude · main-model · high",{exact:true}).waitFor();
  assert(await disclosure.evaluate((el)=>!!(el.compareDocumentPosition(document.querySelector('textarea'))&Node.DOCUMENT_POSITION_FOLLOWING)));
  await page.getByText("Execution options",{exact:true}).click();
  await page.getByRole("combobox",{name:"Agent",exact:true}).selectOption("codex");
  const dialog=page.getByRole("dialog");
  // Inspect the actual rendered model picker and choose the advertised model.
  await dialog.getByRole("button",{expanded:false}).filter({hasText:/Default|model/i}).first().click();
  await dialog.getByRole("option",{name:/Example model/}).click();
  await page.getByRole("combobox",{name:"Reasoning effort",exact:true}).selectOption("high");
  await page.getByLabel("Working directory",{exact:false}).fill("/work/project");
  await dialog.getByRole("button",{name:"Save",exact:true}).click();
  assert.equal(writes.length,1);assert.deepEqual({launcher:writes[0].patch.launcher,model:writes[0].patch.model,effort:writes[0].patch.effort,cwd:writes[0].patch.cwd},{launcher:"codex",model:"test-model",effort:"high",cwd:"/work/project"});
  await page.getByRole("button",{name:/Review project files/}).first().getByText("agents · codex · test-model · high",{exact:true}).waitFor();
  await page.getByText("Saved",{exact:true}).waitFor({state:"hidden"});
  await page.screenshot({path:join(shots,`task-list-${width}-${theme}.png`)});
  await page.getByRole("button",{name:/Review project files/}).first().click();
  await page.getByText("Execution options",{exact:true}).click();
  assert.equal(await page.getByRole("combobox",{name:"Agent",exact:true}).inputValue(),"codex");
  assert.equal(await page.getByLabel("Working directory",{exact:false}).inputValue(),"/work/project");
  await page.getByLabel("Working directory",{exact:false}).scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>document.documentElement.clientWidth),false);
  await page.getByText("Saved",{exact:true}).waitFor({state:"hidden"});
  await page.getByText("Execution options",{exact:true}).click();
  await disclosure.getByText("agents · codex · test-model · high",{exact:true}).waitFor();
  const oneLine=await disclosure.evaluate(el=>{
   const [label,detail]=el.querySelectorAll('span');
   const a=label.getBoundingClientRect(),b=detail.getBoundingClientRect();
   return Math.abs((a.top+a.bottom-b.top-b.bottom)/2)<1 && el.scrollWidth<=el.clientWidth;
  });
  assert(oneLine,"execution title and summary share one line");
  await disclosure.scrollIntoViewIfNeeded();
  await page.screenshot({path:join(shots,`task-execution-${width}-${theme}.png`)});
  await page.getByText("Execution options",{exact:true}).click();
  // Stress the form at twice the standard text size; controls remain reachable in its own scroller.
  await page.evaluate(()=>document.documentElement.style.setProperty("--text-scale","2"));
  await page.getByLabel("Working directory",{exact:false}).scrollIntoViewIfNeeded();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>document.documentElement.clientWidth),false);
  const overflow=await dialog.evaluate((el)=>{
   const box=el.getBoundingClientRect();
   return [...el.querySelectorAll('input,select,fieldset,button[aria-expanded]')].filter((n)=>{const r=n.getBoundingClientRect();return r.width>0&&(r.left<box.left-1||r.right>box.right+1);}).map(n=>n.outerHTML);
  });
  assert.deepEqual(overflow,[],"form controls stay inside the dialog at twice the text size");
  await page.screenshot({path:join(shots,`task-execution-${width}-${theme}-large.png`)});
  assert.equal(reads.some(s=>s.includes("launcher.codex/models")),true);
  assert.deepEqual(errors,[]);
  console.log(`PASS ${width} ${theme}: select, save, reopen, directory, model discovery and text scaling`);
  await context.close();
 }
} finally {await browser.close();clearTimeout(watchdog);}
