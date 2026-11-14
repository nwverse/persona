// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
import { initI18n } from "../i18n";
import { credentialOwner, setToken } from "../lib/api";
import { createTodoWithRepositories, repositoryRequestContext, type RepositoryCatalog, type TaskRepositorySet } from "../lib/task-repositories";
import { NewTodoRepositoryBindings, TodoRepositoryBindings } from "./TaskRepositoryBindingsEditor";
import { Toasts } from "./atoms";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Call = { path: string; init: RequestInit; answer: (body: unknown, status?: number) => void };
let root: Root, host: HTMLDivElement, calls: Call[];
const identity = { provider: "github" as const, instance_url: "https://github.com", path: "example/web", canonical_url: "https://github.com/example/web" };
const catalog: RepositoryCatalog = { manager: "assistant", catalog_revision: 4, repositories: [{ ...identity, id: "web", verified_at: "2026-01-01T00:00:00Z" }] };
const set: TaskRepositorySet = { schema_version: 1, manager: "assistant", todo_id: "todo_example", revision: 1, repositories: [{ repo_id: "web", requested_ref: { kind: "default" }, source_snapshot: { identity, clone_url: identity.canonical_url, catalog_revision: 4, transport_origin: "canonical_default" }, links: [{ kind: "pull_request", number: "7", canonical_url: identity.canonical_url + "/pull/7" }], catalog_state: "current" }] };
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label)!;
const click = async (label: string) => act(async () => { expect(button(label), label).toBeTruthy(); button(label).click(); });
const answer = async (call: Call, body: unknown, status = 200) => act(async () => call.answer(body, status));
const render = async (manager = "assistant", todoId = "todo_example") => act(async () => root.render(<><TodoRepositoryBindings manager={manager} todoId={todoId} owner={credentialOwner()} /><Toasts /></>));
const typeLinks = async (value: string) => act(async () => {
 const node=document.querySelector("textarea")!;
 Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,"value")!.set!.call(node,value);
 node.dispatchEvent(new Event("input",{bubbles:true}));
});

beforeEach(async () => {
 await initI18n("en"); setToken("synthetic-task-repository-session");calls=[];
 host=document.createElement("div");document.body.append(host);root=createRoot(host);
 vi.stubGlobal("fetch",vi.fn((url: RequestInfo | URL,init: RequestInit={})=>new Promise<Response>((resolve)=>calls.push({path:String(url).split("/api/v1")[1],init,answer:(body,status=200)=>resolve(new Response(JSON.stringify(body),{status,headers:{"Content-Type":"application/json"}}))}))));
});
afterEach(async()=>{act(()=>root.unmount());host.remove();document.body.innerHTML="";vi.unstubAllGlobals();setToken("");await initI18n("zh-Hans");});

it("loads only on request, saves two PRs atomically, and preserves drafts after conflicts", async()=>{
 await render();expect(calls).toHaveLength(0);
 await click("Repositories and links (optional)");
 expect(calls.map(c=>c.path).sort()).toEqual(["/assistant/repositories","/todos/todo_example/repositories"]);
 await answer(calls.find(c=>c.path==="/assistant/repositories")!,catalog);
 await answer(calls.find(c=>c.path!=="/assistant/repositories")!,set);
 await typeLinks(identity.canonical_url+"/pull/7\n"+identity.canonical_url+"/pull/8");
 await click("Save");
 const save=calls.at(-1)!;expect(save.init.method).toBe("PUT");
 expect(new Headers(save.init.headers).get("X-Persona-Manager")).toBe("assistant");
 expect(JSON.parse(save.init.body as string)).toMatchObject({expected_revision:1,expected_catalog_revision:4,repositories:[{repo_id:"web",requested_ref:{kind:"default"},links:[{url:identity.canonical_url+"/pull/7"},{url:identity.canonical_url+"/pull/8"}]}]});
 await answer(save,{code:"task_repositories_revision_conflict",error:"changed"},409);
 expect(document.querySelector("textarea")!.value).toContain("/pull/8");
 expect(document.querySelector('[role="alert"]')!.textContent).toContain("Your edits are kept");
});

it("ignores late results after manager, todo or credential changes",async()=>{
 await render();await click("Repositories and links (optional)");const old=[...calls];
 await render("other","todo_other");
 await answer(old[0],catalog);await answer(old[1],set);
 expect(document.querySelector("textarea")).toBeNull();
 await click("Repositories and links (optional)");const pending=calls.slice(2);
 setToken("replacement-task-repository-session");
 await answer(pending[0],{...catalog,manager:"other"});await answer(pending[1],{...set,manager:"other",todo_id:"todo_other"});
 expect(document.querySelector("textarea")).toBeNull();
});

it("keeps an empty alias, default ref and multiline creation draft without requests per edit",async()=>{
 const context=repositoryRequestContext("assistant",credentialOwner(),()=>true);
 const { useState }=await import("react");
 function Draft(){const [value,onChange]=useState<import("../lib/task-repositories").CreateRepositoryBindings>();return <NewTodoRepositoryBindings context={context} value={value} onChange={onChange} pending={false}/>;}
 await act(async()=>root.render(<Draft/>));await click("Repositories and links (optional)");await answer(calls[0],catalog);
 await act(async()=>{const node=document.querySelector("select")!;node.value="web";node.dispatchEvent(new Event("change",{bubbles:true}));});
 await typeLinks(identity.canonical_url+"/pull/7\n");
 expect(document.querySelector("textarea")!.value.endsWith("\n")).toBe(true);
 await typeLinks(identity.canonical_url+"/pull/7\n"+identity.canonical_url+"/pull/8");
 expect(document.querySelector("input")!.value).toBe("");expect(calls).toHaveLength(1);
});

it("creates the Todo and association draft in one request, preserving old defaults", async () => {
 const context=repositoryRequestContext("assistant",credentialOwner(),()=>true);
 const pending=createTodoWithRepositories(context," New work "," Details ",undefined,{catalog_revision:4,repositories:[{repo_id:"web",requested_ref:{kind:"default"},links:[{url:identity.canonical_url+"/pull/7"},{url:identity.canonical_url+"/pull/8"},{url:""}]}]});
 expect(calls).toHaveLength(1);
 expect(calls[0].path).toBe("/todos");
 const body=JSON.parse(calls[0].init.body as string);
 expect(body).toMatchObject({title:"New work",notes:"Details",repository_bindings:{catalog_revision:4,repositories:[{repo_id:"web",requested_ref:{kind:"default"},links:[{url:identity.canonical_url+"/pull/7"},{url:identity.canonical_url+"/pull/8"}]}]}});
 expect(body.workspace).toBeUndefined();
 await answer(calls[0],{todo:{id:"new_todo",title:"New work"}});
 expect((await pending).id).toBe("new_todo");
});
