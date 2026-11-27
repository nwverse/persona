import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = path => readFileSync(new URL('../entry/src/main/ets/' + path, import.meta.url), 'utf8');

test('first run opens the demo tour, whose button starts the demo', () => {
  const index = source('pages/Index.ets');
  assert.ok(index.includes("onDemo:()=>this.openSheet('tour')"), 'the phone first run');
  assert.ok(index.includes(".onClick(() => this.openSheet('tour')).id('welcome.demo')"), 'the wide welcome');
  assert.ok(index.includes("if (this.sheetKind === 'tour') HubDemoVideo({ onDemo: () => { this.showSheet = false; this.model.useDemo(); } })"));
});

test('the Hubs page shows each Hub by its name on this phone, its status, and a way to pair again', () => {
  const page = source('components/PhoneSettingsDevicePages.ets');
  assert.ok(page.includes('this.info(this.model.workspaceName(profile),tr(this.model.workspaceStatus(profile)))'));
  assert.ok(page.includes("this.model.workspaceStatus(profile)==='workspace_pair_again'"));
  assert.ok(page.includes('await this.model.renameWorkspace(profile,this.hubDraft)'));
  assert.ok(page.includes('.bindContextMenu(this.hubMenu(profile),ResponseType.LongPress)'));
});

test('a computer can be renamed from its detail where the Hub allows it', () => {
  const page = source('components/SettingsPage.ets');
  assert.ok(page.includes('this.management.canRenameMachine(this.machineOwner, this.machineRow()!)'));
  assert.ok(page.includes('await this.management.renameMachine(owner, row, this.nameDraft) && this.namingCurrent()'));
});
