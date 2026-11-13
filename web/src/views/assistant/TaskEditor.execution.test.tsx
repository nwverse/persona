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
  if(path.includes('/model-config/purposes/assistant'))return reply({purpose:{id:'assistant',machine_id:'box',effective:{provider:'launcher.claude',model:'main-model'},override:{}},providers:[]});
  if(path.includes('/models'))return reply({agent:'codex',models:[{id:'test-model',name:'Test model',efforts:['low','high']}]});
  if(path.includes('/launchers'))return reply({launchers:[{name:'claude',agent:'claude'},{name:'codex',agent:'codex'}]});
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
const render=async(task=saved,key='initial')=>{await act(async()=>root.render(<TaskEditor key={key} task={task} onSaved={onSaved} onClose={onClose} onDeleted={()=>{}}/>));await flush();await act(async()=>{document.querySelector<HTMLDetailsElement>("details")!.open=true;document.querySelector("details")!.dispatchEvent(new Event("toggle"));});await flush();};

it('saves an agent, a model and a directory without changing Main chat; reopens the saved choices',async()=>{
 await render();
 await change(field(t('assistant-taskeditor.execution_agent')),'codex');
 const modelButton=[...document.querySelectorAll('button')].find((b)=>b.getAttribute('aria-expanded')==='false'&&b.querySelector('span'))!;
 await act(async()=>modelButton.click());await flush();
 const modelChoice=[...document.querySelectorAll('button')].find((b)=>b.textContent?.includes('Test model'))!;
 expect(modelChoice).toBeTruthy();await act(async()=>modelChoice.click());await flush();
 await change(field(t('assistant-taskeditor.execution_directory')),'/work/project');
 await click(t('common.save'));
 expect(writes).toEqual([expect.objectContaining({launcher:'codex',model:'test-model',cwd:'/work/project',model_revision:0})]);
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
