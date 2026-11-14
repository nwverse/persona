// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
import { TaskEditor } from "./TaskEditor";
import { blankTask, type Task } from "../../lib/routines";
import { getState } from "../../lib/store";
import { loadPages } from "../../lib/lazy-page";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => loadPages(),60_000);
let host:HTMLDivElement, root:Root;
let saved:Task, writes:Record<string,unknown>[], reject:boolean;
const onSaved=vi.fn(), onClose=vi.fn();
const flush=async()=>{for(let i=0;i<12;i++) await act(async()=>{await Promise.resolve();});};
const field=(label:string)=>[...document.querySelectorAll('label')].find((el)=>el.textContent?.startsWith(label))!.querySelector<HTMLInputElement|HTMLSelectElement>('input,select')!;
const change=async(el:HTMLInputElement|HTMLSelectElement,value:string)=>{
 await act(async()=>{
  const prototype=el instanceof HTMLSelectElement?HTMLSelectElement.prototype:HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(prototype,'value')!.set!.call(el,value);
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement?'change':'input',{bubbles:true}));
 });await flush();
};
const click=async(label:string)=>{
 const button=[...document.querySelectorAll('button')].find((b)=>b.textContent?.trim()===label)!;
 expect(button).toBeTruthy();await act(async()=>button.click());await flush();
};
beforeEach(()=>{
 writes=[];reject=false;onSaved.mockClear();onClose.mockClear();
 saved={...blankTask(),id:'job',title:'Check files',prompt:'Summarize files',model_revision:0};
 Object.assign(getState(),{features:['task_execution'],machines:[{id:'box',name:'Computer',online:true}]});
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
  const path=String(input);
  const reply=(body:unknown,status=200)=>new Response(JSON.stringify(body),{status});
  if(path.includes('/model-config/purposes/assistant'))return reply({purpose:{id:'assistant',machine_id:'box',effective:{provider:'launcher.claude',model:'main-model',effort:'high'},override:{}},providers:[]});
  if(path.includes('/models'))return reply({agent:path.includes('codex')?'codex':'claude',models:[{id:'agent-default',name:'Agent default',default:true,efforts:['low','high'],default_effort:'high'},{id:'test-model',name:'Test model',efforts:['low','high']}]});
  if(path.includes('/launchers'))return reply({launchers:[{name:'claude',display_name:'Assistant',agent:'claude'},{name:'codex',display_name:'agents',agent:'codex'}]});
  if(path.includes('/runs'))return reply({runs:[]});
  if(path.includes('/assistant/tasks')){
   const body=JSON.parse(String(init?.body));writes.push(body);
   if(reject)return reply({error:'Directory unavailable'},400);
   saved={...saved,...body};return reply({task:saved});
  }
  throw new Error(`unexpected request ${path}`);
 }));
 host=document.createElement('div');document.body.append(host);root=createRoot(host);
});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.unstubAllGlobals();});
const expand=async(open=true)=>{await act(async()=>{document.querySelector<HTMLDetailsElement>('details')!.open=open;document.querySelector('details')!.dispatchEvent(new Event('toggle'));});await flush();};
const render=async(task=saved,key='initial',expanded=true)=>{await act(async()=>root.render(<TaskEditor key={key} task={task} onSaved={onSaved} onClose={onClose} onDeleted={()=>{}}/>));await flush();if(expanded)await expand();};

it('previews inherited execution above the prompt while collapsed and keeps defaults inherited on save',async()=>{
 await render(saved,'defaults',false);
 const details=document.querySelector<HTMLDetailsElement>('details')!;
 expect(details.open).toBe(false);
 expect(details.querySelector('summary')!.textContent).toContain('Assistant · claude · main-model · high');
 expect(details.compareDocumentPosition(document.querySelector('textarea')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
 await expand();
 for(const label of ['execution_computer','execution_agent','execution_directory']) expect(field(t(`assistant-taskeditor.${label}`)).value).toBe('');
 expect(field(t('settings-modelconfiguration.effort')).value).toBe('');
 expect(details.textContent).toContain(t('assistant-taskeditor.follow_assistant'));
 await change(field(t('assistant-taskeditor.title_label')),'Another title');
 await click(t('common.save'));
 for(const key of ['launcher','model','effort','cwd','execution_machine_id']) expect(writes[0]).not.toHaveProperty(key);
});

it('saves an agent, a model and a directory without changing Main chat; reopens the saved choices',async()=>{
 await render();
 await change(field(t('assistant-taskeditor.execution_agent')),'codex');
 await expand(false);
 expect(document.querySelector('summary')!.textContent).toContain('agents · codex · agent-default · high');
 await expand();
 const modelButton=[...document.querySelectorAll('button')].find((b)=>b.getAttribute('aria-expanded')==='false'&&b.querySelector('span'))!;
 await act(async()=>modelButton.click());await flush();
 const modelChoice=[...document.querySelectorAll('button')].find((b)=>b.textContent?.includes('Test model'))!;
 expect(modelChoice).toBeTruthy();await act(async()=>modelChoice.click());await flush();
 await change(field(t('settings-modelconfiguration.effort')),'high');
 await expand(false);
 expect(document.querySelector('summary')!.textContent).toContain('agents · codex · test-model · high');
 await expand();
 await change(field(t('assistant-taskeditor.execution_directory')),'/work/project');
 await click(t('common.save'));
 expect(writes).toEqual([expect.objectContaining({launcher:'codex',model:'test-model',effort:'high',cwd:'/work/project',model_revision:0})]);
 expect(onSaved).toHaveBeenCalledOnce();expect(onClose).toHaveBeenCalledOnce();
 await render(saved,'reopen');
 expect(field(t('assistant-taskeditor.execution_agent')).value).toBe('codex');
 expect(field(t('assistant-taskeditor.execution_directory')).value).toBe('/work/project');
 expect(document.body.textContent).toContain('Test model');
});

it('keeps a directory draft after a refused save',async()=>{
 await render();await change(field(t('assistant-taskeditor.execution_directory')),'/work/missing');reject=true;
 await click(t('common.save'));
 expect(onSaved).not.toHaveBeenCalled();expect(onClose).not.toHaveBeenCalled();
 expect(field(t('assistant-taskeditor.execution_directory')).value).toBe('/work/missing');
});
