import { pasteboard } from '@kit.BasicServicesKit';
import { i18n } from '@kit.LocalizationKit';
import { FileFindHit, FolderCrumb, commitFilePatch, findWorkspaceFiles, indexWorkspaceFiles, workspaceAgeMinutes, joinedFilePath, parentFolder } from '../models/WorkspaceBrowser';
import { WorkspacePatch } from './WorkspacePatch';
import { Symbols } from '../design/Symbols';
import { IconActions } from '../design/IconActions';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { common } from '@kit.AbilityKit';
import { fileIo, fileUri, picker } from '@kit.CoreFileKit';
import { filePreview } from '@kit.PreviewKit';
import { util } from '@kit.ArkTS';
import { ComposeListItemV2, ContentItemV2, LengthMetrics } from '@kit.ArkUI';
import { image } from '@kit.ImageKit';
import { ClientModel } from '../models/ClientModel';
import { SessionWorkspace, WorkspaceEntry, WorkspaceGitFile, WorkspacePreviewServer, WorkspaceBranch, WorkspaceTrashItem } from '../models/SessionWorkspace';
import { FileTransferMachine, FileTransferRecord, FileTransferSetting } from '../models/FileTransfer';
import { Wire } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { Tokens } from '../design/Tokens';
import { tr, trError } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { readerLink } from '../platform/ReaderLink';

const TRANSFER_LIMIT: number = 32 * 1024 * 1024;

class PickedFile {
  name: string = '';
  data: ArrayBuffer = new ArrayBuffer(0);
}

class FilePreviewCopy {
  name: string = '';
  path: string = '';
}

@ComponentV2
export struct SessionTools {
  @Require @Param model: ClientModel;
  @Param initialFile: string = '';
  @Param initialTab: number = 0;
  @Local workspace: SessionWorkspace = new SessionWorkspace();
  @Local fileRowBindings: BoundRow<WorkspaceEntry>[] = [];
  @Local gitRowBindings: BoundRow<WorkspaceGitFile>[] = [];
  @Local stashRowBindings: BoundRow<Record<string, Object>>[] = [];
  @Local previewRowBindings: BoundRow<WorkspacePreviewServer>[] = [];
  private rowBindingOwner: Object | undefined;
  private rowBindingScope: string = '';
  private fileBindingScope: string = '';
  @Local pathDraft: string = '';
  @Local editingPath: boolean = false;
  @Local fileQuery: string = '';
  @Local showHidden: boolean = false;
  @Local showCommitPatch: boolean = false;
  @Local collapseChanges: boolean = false;
  @Local gitFilter: string = 'all';
  @Local terminalInput: string = '';
  @Local transferError: string = '';
  @Local fileOpen: boolean = false;
  @Local transferOpen: boolean = false;
  @Local fileEditSheetOpen: boolean = false;
  @Local fileEditMode: string = '';
  @Local fileActionEntry: WorkspaceEntry | undefined = undefined;
  @Local renameDraft: string = '';
  @Local moveTarget: string = '';
  @Local moveConflictChoice: number = 0;
  @Local fileEditBusy: boolean = false;
  @Local pickerBusy: boolean = false;
  @Local previewBusy: boolean = false;
  @Local previewPixels: image.PixelMap | undefined = undefined;
  @Local previewRatio: number = 1;
  @Local previewServerIndex: number = 0;
  @Local previewStartConfirmName: string = '';
  @Local screenInput: string = '';
  @Local screenControlConfirmOpen: boolean = false;
  @Local terminalControlConfirmOpen: boolean = false;
  @Local gitSheetOpen: boolean = false;
  @Local gitSheetMode: string = '';
  @Local commitFilePath: string = '';
  @Local gitDraftMessage: string = '';
  @Local gitStashMessage: string = '';
  @Local gitStashUntracked: boolean = false;
  @Local gitSelectedBranch: number = 0;
  @Local gitTargetBranch: string = '';
  @Local gitNewBranchName: string = '';
  @Local previewPathDraft: string = '/';
  @Local previewInput: string = '';
  private previewCopies: FilePreviewCopy[] = [];
  @Event onWorkspaceReady: (workspace: SessionWorkspace | undefined) => void = (_workspace: SessionWorkspace | undefined) => {};
  @Event onOpenLink: (address: string) => void = (_address: string) => {};

  aboutToAppear(): void {
    this.bindWorkspace();
    if (this.initialFile) { this.fileOpen = true; this.workspace.openFile(this.initialFile, this.initialFile.split('/').pop() || this.initialFile); }
  }

  private bindWorkspace(): void {
    this.workspace.bind(this.model);
    this.workspace.selectTab(this.initialFile ? 0 : this.initialTab);
    this.syncRowBindings();
    this.pathDraft = this.workspace.currentPath;
    this.workspace.show();
    this.fileOpen = false; this.fileEditSheetOpen = false; this.gitSheetOpen=false; this.gitSheetMode=''; this.commitFilePath='';
    this.editingPath=false; this.fileQuery=''; this.showHidden=false; this.showCommitPatch=false; this.collapseChanges=false; this.gitFilter='all';
    this.fileActionEntry = undefined; this.fileEditMode = '';
    this.transferOpen = false;
    this.fileEditBusy = false; this.pickerBusy = false; this.previewBusy = false; this.transferError = '';
    this.previewPathDraft = '/'; this.previewInput = '';
    this.previewPixels?.release(); this.previewPixels = undefined;
    this.onWorkspaceReady(this.workspace);
  }

  aboutToDisappear(): void {
    this.workspace.hide();
    this.previewPixels?.release(); this.previewPixels = undefined;
    this.onWorkspaceReady(undefined);
  }

  @Monitor('model.selected') selectedChanged(): void {
    const cwd = this.model.selected.path || Wire.text(this.model.selected.data(), 'cwd');
    if (this.workspace.isCurrent() && this.workspace.cwd === cwd) return;
    this.bindWorkspace();
  }

  @Monitor('model.profile') profileChanged(): void {
    this.bindWorkspace();
  }

  @Monitor('model.connected') connectionChanged(): void {
    if (this.model.connected) this.workspace.onConnected();
    else this.workspace.onDisconnected();
  }

  @Monitor('fileOpen') fileSheetChanged(): void {
    if (!this.fileOpen) this.workspace.closeFile();
  }

  @Monitor('gitSheetOpen') gitSheetChanged(): void {
    if (!this.gitSheetOpen) { this.workspace.closeGitDiff(); this.workspace.closeGitShow(); this.showCommitPatch=false; this.commitFilePath=''; }
  }

  @Monitor('workspace.entries', 'workspace.gitFiles', 'workspace.gitStashes', 'workspace.previewServers',
    'workspace.currentPath', 'workspace.cwd', 'model.profile', 'model.selected', 'model.machines')
  rowsChanged(): void { this.syncRowBindings(); this.workspace.fileTransfer.refreshTargets(); }

  private syncRowBindings(): void {
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager,
      this.model.selected.key(), this.workspace.cwd]);
    const fileScope = JSON.stringify([scope, this.workspace.currentPath]);
    this.fileRowBindings = reconcileBoundRows(this.fileRowBindings, this.workspace.entries,
      (row: WorkspaceEntry) => row.path, this.rowBindingOwner, this.model, this.fileBindingScope, fileScope);
    this.gitRowBindings = reconcileBoundRows(this.gitRowBindings, this.workspace.gitFiles,
      (row: WorkspaceGitFile) => row.path, this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.stashRowBindings = reconcileBoundRows(this.stashRowBindings, this.workspace.gitStashes,
      (row: Record<string, Object>) => Wire.text(row, 'oid'), this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.previewRowBindings = reconcileBoundRows(this.previewRowBindings, this.workspace.previewServers,
      (row: WorkspacePreviewServer) => row.name, this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.rowBindingOwner = this.model; this.rowBindingScope = scope; this.fileBindingScope = fileScope;
  }

  @Monitor('workspace.previewFrame') previewFrameChanged(): void { this.decodePreviewFrame(); }

  private uiContext(): common.UIAbilityContext {
    return this.getUIContext().getHostContext() as common.UIAbilityContext;
  }

  private errorText(error: Error): string {
    if (error.message === 'transfer_limit' || error.message === 'response_too_large') return tr('session_tools_transfer_limit');
    if (error.message === 'transfer_unavailable' || error.message === 'transfer_active' || error.message === 'target_unavailable') return tr('file_transfer_unavailable');
    if (error.message === 'journal_unavailable') return tr('file_transfer_journal_unavailable');
    if (error.message === 'transfer_unknown_requires_acknowledgement') return tr('file_transfer_check_unknown_first');
    if (error.message === 'duplicate_confirmation_required') return tr('file_transfer_confirm_duplicate');
    if (error.message === 'scope_changed' || error.message === 'request_updated') return tr('request_updated');
    if (error.message === 'file_read_failed') return tr('session_tools_file_read_failed');
    if (error.message === 'preview_unavailable') return tr('session_tools_preview_unavailable');
    if (error.message === 'write_unavailable' || error.message === 'pending_action') return tr('session_tools_write_unavailable');
    return trError(error.message || 'failed');
  }

  private openEntry(entry: WorkspaceEntry): void {
    if (entry.directory) { this.fileQuery=''; this.editingPath=false; }
    if (!entry.directory) this.fileOpen = true;
    this.workspace.openEntry(entry).then(() => this.pathDraft = this.workspace.currentPath)
      .catch((error: Error) => this.transferError = this.errorText(error));
  }

  private openFileTransfer(entry: WorkspaceEntry): void {
    this.transferError = '';
    this.transferOpen = true;
    this.workspace.openFileTransfer(entry).catch((error: Error) => this.transferError = this.errorText(error));
  }

  private selectTransferTarget(machineId: string): void {
    if (this.workspace.fileTransfer.targetSelectionFrozen()) return;
    this.workspace.fileTransfer.selectTarget(machineId).catch((error: Error) => this.transferError = this.errorText(error));
  }

  private currentTransfer(): FileTransferRecord | undefined { return this.workspace.fileTransfer.latestForSource(); }

  private confirmFileTransfer(): void {
    const transfer = this.workspace.fileTransfer;
    const machineId = transfer.targetId;
    if (!transfer.canStart(machineId)) return;
    const duplicate = transfer.requiresDuplicateConfirmation(machineId);
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    this.getUIContext().showAlertDialog({ title: tr('file_transfer_confirm_title'),
      message: this.transferConfirmation(duplicate),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('file_transfer_confirm_send'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget() ||
          machineId !== transfer.targetId) return;
        transfer.start(machineId, duplicate).then((record: FileTransferRecord) => {
          if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
          this.transferError = '';
          if (record.state === 'failed' && record.code) this.transferError = tr('file_transfer_error') + ': ' + record.code;
        }).catch((error: Error) => {
          if (this.workspace.isCurrent() && revision === this.workspace.currentRevision() && target === this.workspace.currentTarget())
            this.transferError = this.errorText(error);
        });
      } }
    });
  }

  private transferConfirmation(duplicate: boolean): string {
    const transfer = this.workspace.fileTransfer;
    const lines = [tr('file_transfer_source') + ': ' + transfer.sourceMachineName(), transfer.sourceFilePath(),
      tr('file_transfer_target') + ': ' + transfer.targetMachineName(),
      tr('file_transfer_inbox') + ': ' + transfer.settingForTarget().inbox];
    if (duplicate) lines.push(tr('file_transfer_confirm_duplicate'));
    return lines.filter((line: string) => !!line).join('\n');
  }

  private currentTransferProgress(): number {
    const record = this.currentTransfer();
    return record ? this.workspace.fileTransfer.progress(record) : 0;
  }

  private currentTransferStatus(): string {
    const record = this.currentTransfer();
    if (!record) return '';
    let status = tr('file_transfer_status_' + record.state);
    if (record.total > 0) status += ' · ' + String(record.bytes) + ' / ' + String(record.total);
    return status;
  }

  private currentTransferCancelKey(): string { return this.currentTransfer()?.key || ''; }

  private checkTransfer(key: string): void {
    this.workspace.fileTransfer.refresh(key).catch((error: Error) => this.transferError = this.errorText(error));
  }

  private acknowledgeTransfer(key: string): void {
    this.workspace.fileTransfer.acknowledgeUnknown(key).then((saved: boolean) => {
      if (!saved) this.transferError = tr('file_transfer_journal_unavailable');
    });
  }

  private cancelTransfer(key: string): void {
    this.workspace.fileTransfer.cancel(key).then(() => {});
  }

  private openPath(): void {
    const next = this.pathDraft.trim();
    if (!next) return;
    this.editingPath=false; this.fileQuery='';
    this.workspace.openPath(next).catch((error: Error) => this.transferError = this.errorText(error));
  }

  @Computed get pathCrumbs():FolderCrumb[] { return this.workspace.crumbs(); }
  @Computed get shownFileRows():BoundRow<WorkspaceEntry>[] {
    return this.fileRowBindings.filter((binding:BoundRow<WorkspaceEntry>) => this.showHidden || !binding.value.name.startsWith('.'));
  }
  @Computed get findIndex():FileFindHit[] {
    const names=this.workspace.findError ? this.workspace.entries.map((v:WorkspaceEntry) => v.name+(v.directory ? '/' : '')) : this.workspace.findNames;
    return indexWorkspaceFiles(names,this.showHidden,/^[A-Za-z]:[\\/]/.test(this.workspace.currentPath) || this.workspace.currentPath.startsWith('\\\\'));
  }
  @Computed get findRows():FileFindHit[] { return findWorkspaceFiles(this.findIndex,this.fileQuery); }
  private goFolder(path:string):void {
    this.pathDraft=path; this.editingPath=false; this.fileQuery='';
    this.workspace.openPath(path).then(() => this.pathDraft=this.workspace.currentPath).catch((error:Error) => this.transferError=this.errorText(error));
  }
  private folderHistory(forward:boolean):void {
    this.editingPath=false; this.fileQuery='';
    this.workspace.historyFolder(forward).then(() => this.pathDraft=this.workspace.currentPath).catch((error:Error) => this.transferError=this.errorText(error));
  }
  private findHit(hit:FileFindHit):void {
    const path=joinedFilePath(this.workspace.currentPath,hit.relative);
    if (hit.directory) this.goFolder(path);
    else { this.fileOpen=true; this.workspace.openFile(path,hit.relative.slice(hit.relative.lastIndexOf('/')+1)); }
  }
  private editFolder():void { this.pathDraft=this.workspace.currentPath; this.editingPath=true; this.fileQuery=''; }
  private refreshFolder():void {
    this.workspace.loadFiles();
    if (this.fileQuery.trim()) this.workspace.loadFileIndex(true);
  }
  private selectedGitFile(): WorkspaceGitFile | undefined {
    return this.workspace.gitFiles.find((file: WorkspaceGitFile) => file.path === this.workspace.gitDiffPath);
  }

  private gitDiffHasBoth(): boolean {
    const file = this.selectedGitFile();
    return !!file && file.staged && file.unstaged;
  }

  private openGitFile(file: WorkspaceGitFile, staged?:boolean): void {
    this.gitSheetMode='diff'; this.gitSheetOpen=true;
    this.workspace.openGitDiff(file,staged);
  }
  private inspectCommit(commit:Record<string,Object>):void {
    const hash=Wire.text(commit,'hash');
    if (!hash) return;
    this.gitSheetMode='history'; this.gitSheetOpen=true; this.showCommitPatch=false; this.commitFilePath='';
    this.workspace.showGitCommit(hash);
  }

  private openGitSheet(mode: string): void {
    if (!this.workspace.canWriteGit()) return;
    this.gitSheetMode = mode;
    if (mode === 'branch') {
      this.gitSelectedBranch = Math.max(0, this.workspace.gitBranches.findIndex((branch: WorkspaceBranch) => !branch.current));
      this.gitNewBranchName = '';
      this.workspace.loadGitBranches();
    } else if (mode === 'commit') this.gitDraftMessage = '';
    else if (mode === 'stash_push') { this.gitStashMessage = ''; this.gitStashUntracked = false; }
    this.gitSheetOpen = true;
  }

  private branchLabel(branch: WorkspaceBranch): string {
    return branch.name + (branch.current ? ' · ' + tr('session_tools_current_branch') : '') + (branch.remote ? ' · ' + tr('session_tools_remote_branch') : '');
  }

  private confirmGitWrite(kind: string, value: string = '', extra: string = ''): void {
    if (!this.workspace.canWriteGit()) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    let payloadValue = value, payloadExtra = extra;
    if (kind === 'commit') payloadValue = this.gitDraftMessage.trim();
    else if (kind === 'switch') payloadValue = this.workspace.gitBranches[this.gitSelectedBranch]?.name || '';
    else if (kind === 'create') {
      payloadValue = this.gitNewBranchName.trim();
      payloadExtra = this.workspace.gitBranches[this.gitSelectedBranch]?.name || Wire.text(this.workspace.gitStatus, 'branch');
    } else if (kind === 'stash_push') {
      payloadValue = this.gitStashMessage.trim(); payloadExtra = this.gitStashUntracked ? '1' : '';
    }
    if (kind === 'commit' && !payloadValue || (kind === 'switch' || kind === 'create') && !payloadValue) return;
    const branch = Wire.text(this.workspace.gitStatus, 'branch', tr('branch'));
    const machine = this.model.machineName(this.workspace.currentMachineId());
    let detail = tr('session_tools_git_' + kind);
    if (payloadValue) detail += '\n' + payloadValue;
    if (kind === 'push') detail += '\n' + Wire.text(this.workspace.gitStatus, 'upstream', tr('session_tools_no_upstream'));
    if (kind === 'publish') detail += '\n' + branch;
    if (kind === 'create' && payloadExtra) detail += '\n' + tr('session_tools_branch_from') + ': ' + payloadExtra;
    this.getUIContext().showAlertDialog({ title: tr('session_tools_git_confirm_title'),
      message: machine + '\n' + this.workspace.cwd + '\n' + branch + '\n' + detail,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('continue'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        this.runGitWrite(kind, payloadValue, payloadExtra, revision, target);
      } }
    });
  }

  private runGitWrite(kind: string, value: string, extra: string, revision: number, target: string): void {
    let action: Promise<boolean> = Promise.resolve(false);
    if (kind === 'stage' || kind === 'unstage') {
      const file = this.workspace.gitFiles.find((candidate: WorkspaceGitFile) => candidate.path === value);
      if (file) action = this.workspace.stageGitFile(file, kind === 'unstage');
    } else if (kind === 'commit') action = this.workspace.commitGit(value);
    else if (kind === 'push') action = this.workspace.pushGit(false);
    else if (kind === 'publish') action = this.workspace.pushGit(true);
    else if (kind === 'switch') action = this.workspace.switchGit(value);
    else if (kind === 'create') action = this.workspace.switchGit(value, true, extra);
    else if (kind === 'stash_push') action = this.workspace.stashGit('push', '', value, extra === '1');
    else if (kind === 'stash_apply' || kind === 'stash_pop' || kind === 'stash_drop')
      action = this.workspace.stashGit(kind.slice('stash_'.length), value);
    action.then((ok: boolean) => {
      if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
      if (!ok) this.transferError = this.errorText(new Error(this.workspace.gitWriteError || 'failed'));
      else { this.transferError = ''; this.gitSheetOpen = false; }
    });
  }

  private openFileActions(mode: string, entry: WorkspaceEntry): void {
    this.fileActionEntry = entry; this.fileEditMode = mode;
    if (mode === 'rename') this.renameDraft = entry.name;
    else if (mode === 'move') this.moveTarget = this.workspace.currentPath || this.workspace.cwd;
    this.fileEditSheetOpen = true;
  }

  private confirmFileEdit(): void {
    const entry = this.fileActionEntry;
    if (!entry || !this.workspace.canEditFiles() || this.fileEditBusy) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    const mode = this.fileEditMode;
    const destination = mode === 'rename' ? this.renameDraft.trim() : this.moveTarget.trim();
    const details = mode === 'rename' ? entry.path + ' → ' + destination : entry.path + '\n→ ' + destination;
    const replacing = mode === 'move_conflict' && this.moveConflictChoice === 1;
    this.getUIContext().showAlertDialog({ title: tr(replacing ? 'session_tools_replace_confirm' : 'session_tools_file_edit_confirm'),
      message: this.model.machineName(this.workspace.currentMachineId()) + '\n' + this.workspace.cwd + '\n' + details,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('continue'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget() ||
          this.fileActionEntry?.path !== entry.path) return;
        this.saveFileEdit();
      } }
    });
  }

  private async saveFileEdit(): Promise<void> {
    const entry = this.fileActionEntry;
    if (!entry || this.fileEditBusy) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    this.fileEditBusy = true;
    try {
      if (this.fileEditMode === 'rename') {
        const renamed = await this.workspace.renameFile(entry.path, this.renameDraft);
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        if (!renamed) this.transferError = this.errorText(new Error(this.workspace.fileWriteError || 'failed'));
        else { this.fileEditSheetOpen = false; this.workspace.fileWriteState = 'idle'; }
      } else if (this.fileEditMode === 'move') {
        const moved = await this.workspace.moveFiles([entry.path], this.moveTarget);
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        if (moved) this.fileEditSheetOpen = false;
        else if (this.workspace.fileWriteState === 'conflict') this.fileEditMode = 'move_conflict';
        else this.transferError = this.errorText(new Error(this.workspace.fileWriteError || 'failed'));
      } else if (this.fileEditMode === 'move_conflict') {
        const policy = this.moveConflictChoice === 0 ? 'keep' : 'replace';
        const moved = await this.workspace.moveFiles([entry.path], this.moveTarget, policy);
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        if (moved) this.fileEditSheetOpen = false;
        else this.transferError = this.errorText(new Error(this.workspace.fileWriteError || 'failed'));
      }
    } catch (error) {
      if (this.workspace.isCurrent() && revision === this.workspace.currentRevision() && target === this.workspace.currentTarget())
        this.transferError = this.errorText(error as Error);
    } finally {
      if (this.workspace.isCurrent() && revision === this.workspace.currentRevision() && target === this.workspace.currentTarget()) this.fileEditBusy = false;
    }
  }

  private confirmTrash(entry: WorkspaceEntry): void {
    if (!this.workspace.canEditFiles()) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    const machine = this.model.machineName(this.workspace.currentMachineId());
    this.getUIContext().showAlertDialog({ title: tr('session_tools_trash_title'),
      message: machine + '\n' + this.workspace.cwd + '\n' + entry.path,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('session_tools_move_to_trash'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        this.workspace.trashFiles([entry.path]).then((ok: boolean) => {
          if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
          if (!ok) this.transferError = this.errorText(new Error(this.workspace.fileWriteError || 'failed'));
        });
      } }
    });
  }

  private restoreTrash(): void {
    if (!this.workspace.canEditFiles() || !this.workspace.trashUndo.length) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    const trashed = this.workspace.trashUndo.map((item: WorkspaceTrashItem) => item.path);
    this.getUIContext().showAlertDialog({ title: tr('session_tools_restore_confirm'),
      message: this.model.machineName(this.workspace.currentMachineId()) + '\n' + this.workspace.cwd + '\n' + trashed.join('\n'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('session_tools_restore'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget() ||
          JSON.stringify(trashed) !== JSON.stringify(this.workspace.trashUndo.map((item: WorkspaceTrashItem) => item.path))) return;
        this.runRestoreTrash(revision, target);
      } }
    });
  }

  private runRestoreTrash(revision: number, target: string): void {
    this.workspace.restoreTrash().then((ok: boolean) => {
      if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
      if (!ok) this.transferError = this.errorText(new Error(this.workspace.fileWriteError || 'failed'));
    });
  }

  private async previewSelectedFile(): Promise<void> {
    if (!this.fileOpen || !this.workspace.selectedFile || this.previewBusy) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget(), path = this.workspace.selectedFile;
    this.previewBusy = true; this.transferError = '';
    try {
      if (!canIUse('SystemCapability.FileManagement.FilePreview.Core')) throw new Error('preview_unavailable');
      const file = await this.workspace.downloadFile();
      if (!this.fileOpen || !this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget() || path !== this.workspace.selectedFile)
        throw new Error('request_updated');
      const context = this.uiContext(), directory = context.cacheDir + '/session-workspace-previews';
      try { fileIo.statSync(directory); } catch (_) { fileIo.mkdirSync(directory, true); }
      this.prunePreviewCopies(directory);
      const extension = this.previewExtension(file.name, this.workspace.selectedFileMime);
      const copy = new FilePreviewCopy(); copy.name = file.name;
      copy.path = directory + '/' + util.generateRandomUUID() + (extension ? '.' + extension : '');
      const output = fileIo.openSync(copy.path, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.TRUNC);
      try { if (fileIo.writeSync(output.fd, file.data) !== file.data.byteLength) throw new Error('preview_unavailable'); }
      finally { fileIo.closeSync(output.fd); }
      this.previewCopies.push(copy);
      const uri = fileUri.getUriFromPath(copy.path);
      if (!await filePreview.canPreview(context, uri)) throw new Error('preview_unavailable');
      if (!this.fileOpen || !this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) throw new Error('request_updated');
      await filePreview.openPreview(context, { title: file.name, uri: uri, mimeType: this.workspace.selectedFileMime });
      if (!this.fileOpen || !this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) {
        await filePreview.closePreview(context); throw new Error('request_updated');
      }
    } catch (error) { this.transferError = this.errorText(error as Error); }
    finally { this.previewBusy = false; }
  }

  private previewExtension(name: string, mime: string): string {
    const match = name.match(/\.([A-Za-z0-9]{1,12})$/);
    if (match) return match[1].toLowerCase();
    if (mime === 'application/pdf') return 'pdf';
    if (mime === 'image/jpeg') return 'jpg';
    if (mime === 'image/png') return 'png';
    if (mime === 'image/webp') return 'webp';
    if (mime === 'image/gif') return 'gif';
    if (mime === 'image/heic' || mime === 'image/heif') return 'heic';
    if (mime.startsWith('audio/')) return 'audio';
    if (mime.startsWith('video/')) return 'video';
    return '';
  }

  private prunePreviewCopies(directory: string): void {
    let names: string[] = [];
    try { names = fileIo.listFileSync(directory); } catch (_) { return; }
    const keep = names.filter((name: string) => /^[A-Za-z0-9_-]{16,80}(?:\.[A-Za-z0-9]{1,12})?$/.test(name));
    while (keep.length >= 8) { const old = keep.shift(); if (old) { try { fileIo.unlinkSync(directory + '/' + old); } catch (_) {} } }
  }

  private async decodePreviewFrame(): Promise<void> {
    const frame = this.workspace.previewFrame;
    if (!frame) { this.previewPixels?.release(); this.previewPixels = undefined; return; }
    let source: image.ImageSource | undefined;
    let pixels: image.PixelMap | undefined;
    try {
      source = image.createImageSource(frame.data);
      const info = await source.getImageInfo();
      if (info.size.width <= 0 || info.size.height <= 0 || info.size.width * info.size.height > 40000000) throw new Error('response_too_large');
      const scale = Math.min(1, 1600 / Math.max(info.size.width, info.size.height));
      pixels = await source.createPixelMap({ desiredSize: { width: Math.max(1, Math.round(info.size.width * scale)), height: Math.max(1, Math.round(info.size.height * scale)) } });
      if (this.workspace.previewFrame !== frame || !this.workspace.isCurrent()) { await pixels.release(); return; }
      const old = this.previewPixels;
      this.previewPixels = pixels; pixels = undefined; this.previewRatio = info.size.width / info.size.height;
      old?.release(); this.workspace.acknowledgePreviewFrame(frame.seq);
    } catch (_) {
      if (this.workspace.previewFrame === frame) this.workspace.previewError = 'preview_unavailable';
    } finally { await pixels?.release(); await source?.release(); }
  }

  private openPreviewExternal(): void {
    const address = readerLink(this.workspace.previewExternalUrl);
    if (address) this.onOpenLink(address);
  }

  private previewNavigationPath(): string {
    const path = this.previewPathDraft.trim();
    if (!path.startsWith('/') || path.startsWith('//') || /[\\\r\n\u0000-\u001f]/.test(path) || path.includes('://')) return '';
    return path;
  }

  private sendPreviewNavigation(): void {
    const path = this.previewNavigationPath();
    if (!path) { this.transferError = tr('session_tools_preview_path_invalid'); return; }
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    if (this.workspace.sendPreviewAction(Wire.payload(['t', 'url'], ['nav', path]), revision, target)) {
      this.workspace.previewPath = path; this.transferError = '';
    }
  }

  private previewCommandWord(word: string): string {
    if (word.length > 0 && /^[A-Za-z0-9_./-]+$/.test(word)) return word;
    return "'" + word.replace(/'/g, "'\\''") + "'";
  }

  private previewCommand(server: WorkspacePreviewServer): string {
    return server.command.map((word: string) => this.previewCommandWord(word)).join(' ');
  }

  private confirmStartPreview(server: WorkspacePreviewServer): void {
    if (!this.workspace.canStartPreview()) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    const envKeys = Object.keys(server.env).sort();
    const environment = envKeys.length ? '\nenv ' + envKeys.map((key: string) => this.previewCommandWord(key + '=•••')).join(' ') : '';
    this.getUIContext().showAlertDialog({ title: tr('session_tools_preview_start_confirm'),
      message: this.model.machineName(this.workspace.currentMachineId()) + '\n' + (server.cwd || this.workspace.cwd) + '\n' +
        this.previewCommand(server) + environment,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('start'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        this.workspace.startPreview(server.name).then((ok: boolean) => {
          if (!ok && revision === this.workspace.currentRevision() && target === this.workspace.currentTarget())
            this.transferError = this.errorText(new Error(this.workspace.previewError || 'failed'));
        });
      } }
    });
  }

  private openReadyPreview(server: WorkspacePreviewServer): void {
    if (!this.workspace.canUsePreview() || server.port < 1) return;
    const path = this.previewPathFromUrl(server.url);
    this.previewPathDraft = path;
    this.workspace.openPreview(server.port, path, server.name).then((ok: boolean) => {
      if (ok) this.transferError = '';
      else this.transferError = this.errorText(new Error(this.workspace.previewError || 'preview_unavailable'));
    });
  }

  private previewPathFromUrl(value: string): string {
    if (!value) return '/';
    const marker = value.indexOf('://');
    const slash = marker >= 0 ? value.indexOf('/', marker + 3) : value.indexOf('/');
    const path = slash >= 0 ? value.slice(slash) : '/';
    return path.startsWith('/') && !path.startsWith('//') && !/[\\\r\n]/.test(path) ? path : '/';
  }

  private previewAction(name: string): void {
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    this.workspace.sendPreviewAction(Wire.payload(['t'], [name]), revision, target);
  }

  private sendPreviewText(): void {
    const text = this.previewInput, frame = this.workspace.previewFrame;
    if (!text || !frame) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    if (this.workspace.sendPreviewFrameAction(Wire.payload(['t', 'text'], ['text', text]), frame.seq, revision, target)) this.previewInput = '';
  }

  private confirmTerminalControl(takeover: boolean = false): void {
    if (!this.workspace.canControlTerminal()) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    this.getUIContext().showAlertDialog({ title: tr('session_tools_terminal_control_confirm'),
      message: this.model.machineName(this.workspace.currentMachineId()) + '\n' + this.workspace.cwd,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('continue'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        if (takeover) this.workspace.takeOverTerminal(); else this.workspace.setTerminalControl(true);
      } }
    });
  }

  private confirmScreenControl(): void {
    if (!this.workspace.canControlScreen()) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget();
    this.getUIContext().showAlertDialog({ title: tr('session_tools_terminal_screen_control_confirm'),
      message: this.model.machineName(this.workspace.currentMachineId()) + '\n' + this.workspace.cwd,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('continue'), action: () => {
        if (!this.workspace.isCurrent() || revision !== this.workspace.currentRevision() || target !== this.workspace.currentTarget()) return;
        if (!this.workspace.enableScreenControl()) this.transferError = tr('session_tools_view_only');
      } }
    });
  }

  private sendTerminalScreenText(): void {
    if (!this.screenInput) return;
    this.workspace.sendScreenKeys(['enter'], this.screenInput, this.workspace.screenRevision);
    this.screenInput = '';
  }

  @Builder fileMenu() {
    Menu() {
      MenuItem({content:tr('session_enter_path')}).onClick(() => this.editFolder())
      MenuItem({content:tr('session_project_folder')}).onClick(() => this.goFolder(this.workspace.cwd))
      MenuItem({content:tr('session_show_hidden')}).selected(this.showHidden).selectIcon(true).onClick(() => this.showHidden=!this.showHidden)
      MenuItem({ content: tr('refresh') }).onClick(() => this.refreshFolder())
      if (this.workspace.canUploadFiles()) MenuItem({ content: tr('upload') }).enabled(!this.pickerBusy).onClick(() => this.pickUpload())
      if (this.workspace.trashUndo.length) MenuItem({ content: tr('session_tools_restore') }).enabled(this.workspace.canEditFiles())
        .onClick(() => this.restoreTrash())
      MenuItem({ content: tr('parent_folder') }).enabled(this.workspace.currentPath !== this.workspace.cwd)
        .onClick(() => { this.workspace.openParent(); this.pathDraft = this.workspace.currentPath; })
    }
  }

  @Builder fileEntryMenu(binding: BoundRow<WorkspaceEntry>) {
    Menu() {
      MenuItem({ content: tr('open') }).onClick(() => this.openEntry(binding.value))
      if (this.workspace.canSendFile(binding.value)) MenuItem({ content: tr('file_transfer_send_to_computer') })
        .onClick(() => this.openFileTransfer(binding.value))
      if (this.workspace.canEditFiles()) MenuItem({ content: tr('rename') }).onClick(() => this.openFileActions('rename', binding.value))
      if (this.workspace.canEditFiles()) {
        MenuItem({ content: tr('session_tools_move') }).onClick(() => this.openFileActions('move', binding.value))
        MenuItem({ content: tr('session_tools_move_to_trash') }).onClick(() => this.confirmTrash(binding.value))
      }
    }
  }

  @Builder gitMenu() {
    Menu() {
      MenuItem({ content: tr('refresh') }).onClick(() => this.workspace.loadGit())
      if (this.model.features.includes('git_write')) {
        MenuItem({ content: tr('session_tools_commit') }).enabled(this.workspace.canWriteGit() && this.workspace.gitFiles.some((file: WorkspaceGitFile) => file.staged))
          .onClick(() => this.openGitSheet('commit'))
        MenuItem({ content: tr('session_tools_push') }).enabled(this.workspace.canWriteGit() && !!Wire.text(this.workspace.gitStatus, 'upstream'))
          .onClick(() => this.confirmGitWrite('push'))
        MenuItem({ content: tr('session_tools_publish_branch') }).enabled(this.workspace.canWriteGit() && !Wire.text(this.workspace.gitStatus, 'upstream'))
          .onClick(() => this.confirmGitWrite('publish'))
        MenuItem({ content: tr('session_tools_switch_branch') }).enabled(this.workspace.canWriteGit())
          .onClick(() => this.openGitSheet('branch'))
        MenuItem({ content: tr('session_tools_stash_save') }).enabled(this.workspace.canWriteGit() && this.workspace.gitFiles.length > 0)
          .onClick(() => this.openGitSheet('stash_push'))
      }
    }
  }

  @Builder gitFileMenu(binding: BoundRow<WorkspaceGitFile>) {
    Menu() {
      MenuItem({ content: tr('session_tools_stage') }).enabled(this.workspace.canWriteGit() && (binding.value.untracked || binding.value.unstaged))
        .onClick(() => this.confirmGitWrite('stage', binding.value.path))
      MenuItem({ content: tr('session_tools_unstage') }).enabled(this.workspace.canWriteGit() && binding.value.staged)
        .onClick(() => this.confirmGitWrite('unstage', binding.value.path))
    }
  }

  @Builder gitStashMenu(binding: BoundRow<Record<string, Object>>) {
    Menu() {
      MenuItem({ content: tr('session_tools_stash_apply') }).enabled(this.workspace.canWriteGit() && !!Wire.text(binding.value, 'oid'))
        .onClick(() => this.confirmGitWrite('stash_apply', Wire.text(binding.value, 'oid')))
      MenuItem({ content: tr('session_tools_stash_pop') }).enabled(this.workspace.canWriteGit() && !!Wire.text(binding.value, 'oid'))
        .onClick(() => this.confirmGitWrite('stash_pop', Wire.text(binding.value, 'oid')))
      MenuItem({ content: tr('session_tools_stash_drop') }).enabled(this.workspace.canWriteGit() && !!Wire.text(binding.value, 'oid'))
        .onClick(() => this.confirmGitWrite('stash_drop', Wire.text(binding.value, 'oid')))
    }
  }

  private timeLabel(value:string):string {
    const minute=workspaceAgeMinutes(value,Date.now());
    if (minute===0) return tr('agent_time_just_now');
    if (minute>0 && minute<60) return minute+tr('agent_time_minute_suffix');
    if (minute>=60 && minute<1440) return Math.floor(minute/60)+tr('agent_time_hour_suffix');
    if (minute>=1440 && minute<10080) return Math.floor(minute/1440)+tr('agent_time_day_suffix');
    return this.gitDate(value);
  }
  private fileSubtitle(f:WorkspaceEntry):string {
    const link=f.symlink ? tr('symlink')+(f.linkTarget ? ' → '+f.linkTarget : '') : '';
    return [link, f.ignored ? tr('ignored') : '',f.directory ? '' : this.sizeText(f.size),this.timeLabel(f.mtime)]
      .filter((v:string) => !!v).join(' · ');
  }
  @Builder fileRow(binding:BoundRow<WorkspaceEntry>) {
    Row({space:11}) {
      SymbolGlyph(binding.value.directory ? Symbols.folder : Symbols.document).fontSize('21vp')
        .fontColor([binding.value.ignored ? Colors.secondary : binding.value.directory ? Colors.link : Colors.secondary])
        .width(26).flexShrink(0).id('session.fileRow.icon.'+binding.value.path)
      Column({space:3}) {
        Text(binding.value.name).fontSize(16).fontWeight(FontWeight.Medium).fontColor(binding.value.ignored ? Colors.secondary : Colors.ink)
          .wordBreak(WordBreak.BREAK_WORD).id('session.fileRow.name.'+binding.value.path)
        if (this.fileSubtitle(binding.value)) Text(this.fileSubtitle(binding.value))
          .fontSize(12).fontColor(binding.value.linkBroken ? Colors.danger : Colors.secondary).wordBreak(WordBreak.BREAK_WORD)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').constraintSize({minHeight:46}).padding({left:13,right:13,top:8,bottom:8}).backgroundColor(Colors.surface)
  }
  @Builder pathMenu() {
    Menu() {
      ForEach(this.pathCrumbs,(crumb:FolderCrumb) => {
        MenuItem({content:crumb.path}).selected(crumb.path===this.workspace.currentPath).selectIcon(true).onClick(() => this.goFolder(crumb.path))
      },(crumb:FolderCrumb) => crumb.path)
      MenuItem({content:tr('session_project_folder')}).onClick(() => this.goFolder(this.workspace.cwd))
      MenuItem({content:tr('session_enter_path')}).onClick(() => this.editFolder())
    }
  }
  @Builder folderBar() {
    Column({space:5}) {
      Row({space:3}) {
        Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.collapse).fontSize('16vp').fontColor([Colors.ink]) }
          .width(44).height(44).padding(0).backgroundColor(Colors.surface).enabled(this.workspace.folderBack.length>0)
          .accessibilityText(tr('session_folder_back')).onClick(() => this.folderHistory(false)).id('session.files.back')
        Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.expand).fontSize('16vp').fontColor([Colors.ink]) }
          .width(44).height(44).padding(0).backgroundColor(Colors.surface).enabled(this.workspace.folderForward.length>0)
          .accessibilityText(tr('session_folder_forward')).onClick(() => this.folderHistory(true)).id('session.files.forward')
        Button({type:ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.arrow_up')).fontSize('16vp').fontColor([Colors.link]).id('session.files.parent') }
          .width(44).height(44).padding(0).backgroundColor(Colors.surface)
          .enabled(parentFolder(this.workspace.currentPath) !== this.workspace.currentPath && !!parentFolder(this.workspace.currentPath))
          .accessibilityText(parentFolder(this.workspace.currentPath)).onClick(() => this.goFolder(parentFolder(this.workspace.currentPath))).id('session.files.up')
        Row({space:5}) {
          if (this.pathCrumbs.length > 1) {
            Text(this.pathCrumbs.slice(-3,-1).map((crumb:FolderCrumb) => crumb.title).join(' › ')+' ›')
              .fontSize(12).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary)
              .constraintSize({maxWidth:'34%'}).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
          } else SymbolGlyph(Symbols.folder).fontSize('16vp').fontColor([Colors.link])
          Text(this.pathCrumbs[this.pathCrumbs.length-1]?.title || this.workspace.currentPath || tr('directory')).fontSize(16).maxFontScale(Tokens.navigationTextMaxScale)
            .fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
          SymbolGlyph(Symbols.down).fontSize('11vp').fontColor([Colors.secondary])
        }.layoutWeight(1).constraintSize({minHeight:44}).padding({left:9,right:8}).backgroundColor(Colors.surface).borderRadius(9)
          .accessibilityGroup(true).accessibilityText(this.workspace.currentPath).bindMenu(this.pathMenu).id('session.files.path')
        Button({type:ButtonType.Circle}) { Image(Symbols.more).width(17).height(17).fillColor(Colors.ink) }
          .width(44).height(44).padding(0).backgroundColor(Colors.surface).bindMenu(this.fileMenu).accessibilityText(tr('more')).id('session.files.more')
          .bindSheet($$this.transferOpen,this.fileTransferForm(),{title:{title:tr('file_transfer_title')},height:SheetSize.LARGE,showClose:true})
      }.width('100%')
      if (this.editingPath) Row({space:5}) {
        TextInput({text:this.pathDraft,placeholder:tr('path')}).layoutWeight(1).height(Math.max(44,20*nativeEnvironment.fontScale+12)).fontSize(15)
          .onChange((v:string) => this.pathDraft=v).onSubmit(() => this.openPath()).id('session.files.path_input')
        Button(tr('open'),{buttonStyle:ButtonStyleMode.TEXTUAL}).constraintSize({minHeight:44}).maxFontScale(Tokens.navigationTextMaxScale)
          .onClick(() => this.openPath()).id('session.files.open')
      }.width('100%')
      if (!this.editingPath) Row() {
        Search({value:this.fileQuery,placeholder:tr('session_find_files')})
          .height(Math.max(44,20*nativeEnvironment.fontScale+12)).textFont({size:16}).placeholderFont({size:16}).searchIcon({size:'18vp'}).layoutWeight(1)
          .onChange((v:string) => { this.fileQuery=v; if (v.trim()) this.workspace.loadFileIndex(); }).id('session.files.query')
      }.width('100%').id('session.files.find')
      if (this.fileQuery.trim()) Row({space:6}) {
        Text(tr('session_find_here')+' · '+(this.pathCrumbs[this.pathCrumbs.length-1]?.title || this.workspace.currentPath))
          .fontSize(12).fontColor(Colors.secondary).layoutWeight(1).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
        if (!this.workspace.findBusy) Text((this.findRows.length>=100 ? '≥' : '')+this.findRows.length).fontSize(12).fontColor(Colors.secondary)
      }.width('100%').padding({left:4,right:4})
    }.padding({left:12,right:12,top:7,bottom:4}).width('100%')
  }
  @Builder fileView() {
    Column() {
      this.folderBar()
      if (this.workspace.trashUndo.length && this.workspace.canEditFiles()) Row({ space: Tokens.gap }) {
        Text(tr('session_tools_trashed_count').replace('{count}', String(this.workspace.trashUndo.length))).fontSize(Tokens.meta).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_restore')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_restore', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_restore')).bindTips(tr('session_tools_restore')).onClick(() => this.restoreTrash())
      }.padding({ left: Tokens.inset, right: Tokens.inset, bottom: Tokens.gap })

      if (this.transferError) Text(this.transferError).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      if (this.workspace.fileError) Row({space:8}) {
        Text(tr(this.workspace.fileError === 'upstream_error' ? 'phone_directory_read_failed' : this.workspace.fileError)).fontSize(14).fontColor(Colors.secondary).layoutWeight(1)
        Button(tr('retry'), {buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(14).fontColor(Colors.link).onClick(() => this.openPath())
      }.padding(12).width('100%')
      if (['pending', 'unknown'].includes(this.workspace.fileWriteState)) Text(tr('session_tools_write_' + this.workspace.fileWriteState))
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      if (this.pickerBusy) LoadingProgress().height(Tokens.touch).width(Tokens.touch)
      if (this.workspace.fileBusy || this.fileQuery.trim() && this.workspace.findBusy) LoadingProgress().height(28).width(28)
      List() {
        if (this.fileQuery.trim()) {
          if (this.workspace.findError) ListItem() { Text(tr('session_find_unavailable')).fontSize(13).fontColor(Colors.secondary).padding(10) }
          ForEach(this.findRows,(hit:FileFindHit) => {
            ListItem() {
              Row({space:10}) {
                SymbolGlyph(hit.directory ? Symbols.folder : Symbols.document).fontSize('18vp').fontColor([hit.directory ? Colors.link : Colors.secondary])
                Text(hit.relative).fontSize(15).fontColor(Colors.ink).layoutWeight(1).wordBreak(WordBreak.BREAK_WORD)
                SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
              }.width('100%').padding(12).backgroundColor(Colors.surface)
            }.onClick(() => this.findHit(hit)).id('session.find.'+hit.relative)
          },(hit:FileFindHit) => hit.relative)
          if (!this.workspace.findBusy && !this.findRows.length) ListItem() { Text(tr('no_results')).fontSize(14).fontColor(Colors.secondary).padding(14) }
          if (this.workspace.findTruncated) ListItem() { Text(tr('session_tools_first_entries')).fontSize(13).padding(10) }
        } else {
        ForEach(this.shownFileRows, (binding: BoundRow<WorkspaceEntry>) => {
          ListItem() { this.fileRow(binding) }.onClick(() => this.openEntry(binding.value))
            .bindContextMenu(this.fileEntryMenu(binding), ResponseType.LongPress).id('session.files.' + binding.value.path)
        }, (binding: BoundRow<WorkspaceEntry>) => binding.key)
        if (!this.shownFileRows.length && !this.workspace.fileBusy && !this.workspace.fileError) ListItem() { Text(tr('empty')).fontSize(15).fontColor(Colors.secondary).padding(14) }
        if (this.workspace.hasMoreEntries) ListItem() { Text(tr('session_tools_first_entries')).padding(Tokens.inset) }
        }
      }.divider({strokeWidth:0.5,startMargin:46,color:Colors.divider}).width('100%').layoutWeight(1).padding({left:12,right:12})
        .bindSheet($$this.fileOpen,this.fileViewer(),{title:{title:this.workspace.selectedFileName || tr('file')},height:SheetSize.LARGE,showClose:true})
    }.width('100%').height('100%').backgroundColor(Colors.grouped)

      .bindSheet($$this.fileEditSheetOpen, this.fileEditForm(), {
        title: { title: tr(this.fileEditMode === 'rename' ? 'session_tools_rename' : 'session_tools_move') }, height: SheetSize.MEDIUM, showClose: true
      })

  }

  private transferSetting(source: boolean): FileTransferSetting {
    return source ? this.workspace.fileTransfer.sourceSetting : this.workspace.fileTransfer.targetSetting;
  }

  private transferSettingLabel(source: boolean): string {
    return tr('file_transfer_shared_switch') + ' · ' + (source ? this.workspace.fileTransfer.sourceMachineName() : this.workspace.fileTransfer.targetMachineName());
  }

  @Builder transferSettingRow(source: boolean) {
    Column({ space: Tokens.fine }) {
      Row({ space: Tokens.gap }) {
        Text(this.transferSettingLabel(source)).fontSize(Tokens.body).layoutWeight(1)
        Toggle({ type: ToggleType.Switch, isOn: this.transferSetting(source).enabled })
          .enabled(this.transferSetting(source).state === 'ready' && this.workspace.fileTransfer.isAvailable() && !this.workspace.fileTransfer.createBusy)
          .accessibilityText(this.transferSettingLabel(source))
          .onChange((enabled: boolean) => this.workspace.fileTransfer.setEnabled(this.transferSetting(source).machineId, enabled))
      }.width('100%')
      if (this.transferSetting(source).state === 'loading' || this.transferSetting(source).state === 'saving') Row({ space: Tokens.gap }) {
        LoadingProgress().width(Tokens.touch).height(Tokens.touch)
        Text(tr('file_transfer_setting_' + this.transferSetting(source).state)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      }
      if (this.transferSetting(source).state === 'unknown') Text(tr('file_transfer_setting_unknown') + (this.transferSetting(source).error ? ': ' + this.transferSetting(source).error : ''))
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.transferSetting(source).state === 'ready' && this.transferSetting(source).error) Text(tr('file_transfer_error') + ': ' + this.transferSetting(source).error)
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.transferSetting(source).state === 'ready' && this.transferSetting(source).inbox) Text(tr('file_transfer_inbox') + ': ' + this.transferSetting(source).inbox)
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      else if (this.transferSetting(source).state === 'ready' && !this.transferSetting(source).enabled) Text(tr('file_transfer_inbox_enable_hint'))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      else if (this.transferSetting(source).state === 'ready' && this.transferSetting(source).enabled && !this.transferSetting(source).inbox) Text(tr('file_transfer_inbox_unknown'))
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
    }.width('100%').padding({ top: Tokens.gap, bottom: Tokens.gap })
  }

  @Builder transferTargetRow(machine: FileTransferMachine) {
    Row({ space: Tokens.gap }) {
      Radio({ value: machine.id, group: 'session-file-transfer-target' })
        .checked(this.workspace.fileTransfer.targetId === machine.id)
        .enabled(!this.workspace.fileTransfer.targetSelectionFrozen())
        .accessibilityText(machine.title)
        .onChange((checked: boolean) => { if (checked && !this.workspace.fileTransfer.targetSelectionFrozen()) this.selectTransferTarget(machine.id); })
      Text(machine.title).fontSize(Tokens.body).layoutWeight(1)
    }.width('100%').padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.fine, bottom: Tokens.fine })
      .enabled(!this.workspace.fileTransfer.targetSelectionFrozen())
      .opacity(this.workspace.fileTransfer.targetSelectionFrozen() ? 0.55 : 1)
      .onClick(() => { if (!this.workspace.fileTransfer.targetSelectionFrozen()) this.selectTransferTarget(machine.id); })
  }

  @Builder transferUnknownRow(record: FileTransferRecord) {
    Column({ space: Tokens.fine }) {
      Text(tr('file_transfer_status_unknown') + ' · ' + record.targetName).fontSize(Tokens.body).width('100%')
      Flex({ wrap: FlexWrap.Wrap, space: { main: LengthMetrics.vp(Tokens.gap), cross: LengthMetrics.vp(Tokens.gap) } }) {
        if (record.id) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('file_transfer_check_status')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('file_transfer_check_status', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('file_transfer_check_status')).bindTips(tr('file_transfer_check_status')).flexShrink(0).onClick(() => this.checkTransfer(record.key))
        if (!record.acknowledged) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('file_transfer_i_checked')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('file_transfer_i_checked', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('file_transfer_i_checked')).bindTips(tr('file_transfer_i_checked')).flexShrink(0).onClick(() => this.acknowledgeTransfer(record.key))
      }.width('100%')
      if (record.code) Text(tr('file_transfer_error') + ': ' + record.code).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (record.acknowledged) Text(tr('file_transfer_possible_duplicate')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
    }.width('100%').padding({ top: Tokens.gap, bottom: Tokens.gap })
  }

  @Builder fileTransferForm() {
    Column({ space: Tokens.gap }) {
      Scroll() {
        Column({ space: Tokens.gap }) {
      Text(this.workspace.fileTransfer.sourceFileName()).fontSize(Tokens.title)
      Text(this.workspace.fileTransfer.sourceMachineName() + ' · ' + this.workspace.fileTransfer.sourceFilePath())
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      this.transferSettingRow(true)
      Text(tr('file_transfer_choose_target')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
      if (!this.workspace.fileTransfer.targets.length) Text(tr('file_transfer_no_targets')).fontSize(Tokens.meta)
      List() {
        ForEach(this.workspace.fileTransfer.targets, (machine: FileTransferMachine) => {
          ListItem() { this.transferTargetRow(machine) }.id('session.file-transfer.target.' + machine.id)
        }, (machine: FileTransferMachine) => machine.id)
      }.width('100%').height(180)
        .nestedScroll({ scrollForward: NestedScrollMode.SELF_FIRST, scrollBackward: NestedScrollMode.SELF_FIRST })
      if (this.workspace.fileTransfer.targetId && this.workspace.fileTransfer.targetId !== this.workspace.currentMachineId())
        this.transferSettingRow(false)
      if (this.currentTransfer()) {
        if (this.workspace.fileTransfer.targetId !== this.currentTransfer()?.targetMachine) {
          Text(this.currentTransfer()?.targetName || this.currentTransfer()?.targetMachine || '').fontSize(Tokens.body)
          if (this.currentTransfer()?.targetInbox) Text(this.currentTransfer()?.targetInbox || '').fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
        }
        if (this.currentTransfer()?.state === 'running') Progress({ value: this.currentTransferProgress(), total: 100, type: ProgressType.Linear }).width('100%')
        Text(this.currentTransferStatus()).fontSize(Tokens.body)
        if (this.currentTransfer()?.resultPath) Text(this.currentTransfer()?.resultPath || '').fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
        if (this.currentTransfer()?.code) Text(tr('file_transfer_error') + ': ' + (this.currentTransfer()?.code || ''))
          .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
        if (this.currentTransfer()?.cancelUnknown) Text(tr('file_transfer_cancel_unknown')).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
        if (this.currentTransfer()?.state === 'running' && this.currentTransfer()?.id) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('file_transfer_cancel_transfer')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('file_transfer_cancel_transfer', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('file_transfer_cancel_transfer')).bindTips(tr('file_transfer_cancel_transfer'))
          .enabled(!this.currentTransfer()?.cancelBusy).onClick(() => this.cancelTransfer(this.currentTransferCancelKey()))
      }
      ForEach(this.workspace.fileTransfer.unknownForSource(), (record: FileTransferRecord) => {
        this.transferUnknownRow(record)
      }, (record: FileTransferRecord) => record.key)
      if (this.transferError) Text(this.transferError).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.workspace.fileTransfer.createBusy) LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      if (this.workspace.fileTransfer.journalError) Text(tr('file_transfer_journal_unavailable'))
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
        }.width('100%').alignItems(HorizontalAlign.Start)
      }.width('100%').layoutWeight(1).align(Alignment.TopStart).scrollBar(BarState.Auto)
      Flex({ wrap: FlexWrap.Wrap, space: { main: LengthMetrics.vp(Tokens.gap), cross: LengthMetrics.vp(Tokens.gap) } }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('cancel')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('cancel', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('cancel')).bindTips(tr('cancel')).flexShrink(0).onClick(() => this.transferOpen = false)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('file_transfer_confirm_send')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('file_transfer_confirm_send', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('file_transfer_confirm_send')).bindTips(tr('file_transfer_confirm_send')).flexShrink(0)
          .enabled(this.workspace.fileTransfer.canStart() && !this.workspace.fileTransfer.createBusy)
          .onClick(() => this.confirmFileTransfer()).id('session.files.transfer.confirm')
      }.width('100%')
    }.padding(Tokens.page).width('100%').height('100%')
  }

  @Builder fileViewer() {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('download')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('download', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('download')).bindTips(tr('download')).enabled(!!this.workspace.selectedFile).onClick(() => this.saveDownload())
        if (!this.workspace.selectedFileIsText && this.workspace.selectedFile && this.model.features.includes('file_download'))
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_preview_file')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_preview_file', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_preview_file')).bindTips(tr('session_tools_preview_file')).enabled(!this.previewBusy && !this.pickerBusy)
            .onClick(() => this.previewSelectedFile())
      }.width('100%').justifyContent(FlexAlign.End)
      Text(this.workspace.selectedFile + ' · ' + this.sizeText(this.workspace.selectedFileSize) +
        (this.workspace.selectedFileMime ? ' · ' + this.workspace.selectedFileMime : ''))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(2)
      if (this.workspace.fileError) Text(trError(this.workspace.fileError)).fontColor($r('app.color.persona_danger'))
      if (this.transferError) Text(this.transferError).fontColor($r('app.color.persona_danger'))
      if (this.workspace.fileReadBusy) LoadingProgress().layoutWeight(1).width(Tokens.touch).height(Tokens.touch)
      else if (this.workspace.selectedFileIsText) {
        Scroll() {
          Text(this.workspace.selectedFileText).fontFamily('monospace').fontSize(Tokens.meta).copyOption(CopyOptions.LocalDevice)
            .width('100%').textAlign(TextAlign.Start)
        }.layoutWeight(1).width('100%')
        if (this.workspace.fileHasMore) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('load_more')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('load_more', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('load_more')).bindTips(tr('load_more')).onClick(() => this.workspace.loadMoreText())
        if (this.workspace.fileTruncated && !this.workspace.fileHasMore) Text(tr('session_tools_truncated')).fontSize(Tokens.meta)
      } else {
        Column({ space: Tokens.gap }) {
          Text(tr('session_tools_binary_file')).fontSize(Tokens.body)
          Text(this.workspace.selectedFileMime).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
        }.layoutWeight(1).justifyContent(FlexAlign.Center)
      }
    }.padding(Tokens.page).width('100%').height('100%')
  }

  @Builder fileEditForm() {
    Column({ space: Tokens.gap }) {
      if (this.fileActionEntry) Text(this.fileActionEntry.path).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(2)
      if (this.fileEditMode === 'rename') TextInput({ text: this.renameDraft, placeholder: tr('session_tools_new_name') })
        .onChange((value: string) => this.renameDraft = value).id('session.files.rename')
      else TextInput({ text: this.moveTarget, placeholder: tr('session_tools_destination_folder') })
        .onChange((value: string) => this.moveTarget = value).id('session.files.move')
      if (this.fileEditMode === 'move_conflict') {
        Text(tr('session_tools_move_conflict') + ' · ' + this.workspace.fileConflictName).fontSize(Tokens.meta)
        Select([{ value: tr('session_tools_conflict_keep') }, { value: tr('session_tools_conflict_replace') }])
          .selected(this.moveConflictChoice).value(tr(this.moveConflictChoice === 0 ? 'session_tools_conflict_keep' : 'session_tools_conflict_replace'))
          .onSelect((index: number) => this.moveConflictChoice = index === 1 ? 1 : 0).id('session.files.conflict')
      }
      if (['pending', 'unknown'].includes(this.workspace.fileWriteState)) Text(tr('session_tools_write_' + this.workspace.fileWriteState))
        .fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.workspace.fileWriteError) Text(trError(this.workspace.fileWriteError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('cancel')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('cancel', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('cancel')).bindTips(tr('cancel')).onClick(() => this.fileEditSheetOpen = false)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('continue')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('continue', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('continue')).bindTips(tr('continue'))
          .enabled(this.workspace.canEditFiles() && !this.fileEditBusy && !['pending', 'unknown'].includes(this.workspace.fileWriteState))
          .onClick(() => this.confirmFileEdit()).id('session.files.edit.confirm')
      }.width('100%')
      if (this.fileEditBusy) LoadingProgress().height(Tokens.touch).width(Tokens.touch)
    }.padding(Tokens.page).width('100%')
  }

  @Computed get visibleGitRows():BoundRow<WorkspaceGitFile>[] {
    return this.gitRowBindings.filter((binding:BoundRow<WorkspaceGitFile>) => this.gitFilter==='all' ||
      (this.gitFilter==='staged' ? binding.value.staged : this.gitFilter==='untracked' ? binding.value.untracked : binding.value.unstaged || binding.value.untracked));
  }
  private filterKeys():string[] {
    return this.gitFilter==='untracked' || this.workspace.gitFiles.some((f:WorkspaceGitFile) => f.untracked) ?
      ['all','unstaged','staged','untracked'] : ['all','unstaged','staged'];
  }
  private filterCount(filter:string):number {
    return filter==='all' ? this.workspace.gitFiles.length : this.workspace.gitFiles.filter((f:WorkspaceGitFile) =>
      filter==='staged' ? f.staged : filter==='untracked' ? f.untracked : f.unstaged || f.untracked).length;
  }
  private fileInk(file:WorkspaceGitFile):ResourceColor {
    return file.mark==='!' || file.mark==='D' ? Colors.danger : file.untracked || file.mark==='A' ? Colors.positive : Colors.working;
  }
  private leaf(path:string):string { return path.split(/[\\/]/).pop() || path; }
  private gitDate(value:string):string {
    const date=new Date(value);
    return Number.isFinite(date.getTime()) && date.getTime()>0 ? date.toLocaleDateString(this.model.language==='default' ? i18n.getSystemLanguage() : this.model.language) : '';
  }
  @Builder gitBranchCard() {
    Column({space:5}) {
      Row({space:9}) {
        SymbolGlyph($r('sys.symbol.code')).fontSize('21vp').fontColor([Colors.link])
        Text(Wire.text(this.workspace.gitStatus,'branch') || tr('session_git_detached')).fontSize(20).fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink).layoutWeight(1).maxLines(2)
        if (this.workspace.canWriteGit()) Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.down).fontSize('13vp').fontColor([Colors.link]) }
          .width(44).height(44).padding(0).backgroundColor(Colors.control).accessibilityText(tr('session_tools_switch_branch'))
          .onClick(() => this.openGitSheet('branch')).id('session.git.branch_picker')
        Button({type:ButtonType.Circle}) { Image(Symbols.more).width(17).height(17).fillColor(Colors.ink) }
          .width(44).height(44).padding(0).backgroundColor(Colors.control).bindMenu(this.gitMenu).accessibilityText(tr('more')).id('session.git.menu')
      }.width('100%')
      Text([Wire.text(this.workspace.gitStatus,'upstream') || tr('session_tools_no_upstream'),
        Wire.bool(this.workspace.gitStatus,'gone') ? tr('session_git_remote_gone') :
        Wire.number(this.workspace.gitStatus,'ahead')>0 || Wire.number(this.workspace.gitStatus,'behind')>0 ?
          [Wire.number(this.workspace.gitStatus,'ahead')>0 ? '↑ '+Wire.number(this.workspace.gitStatus,'ahead') : '',
            Wire.number(this.workspace.gitStatus,'behind')>0 ? '↓ '+Wire.number(this.workspace.gitStatus,'behind') : ''].filter((v:string) => !!v).join('  ') :
          Wire.text(this.workspace.gitStatus,'upstream') ? tr('session_tools_synced') : ''].filter((v:string) => !!v).join(' · '))
        .fontSize(14).fontColor(Colors.secondary).maxLines(2)
      Text(Wire.text(this.workspace.gitStatus,'root') || this.workspace.cwd).fontSize(12).fontColor(Colors.secondary)
        .fontFamily('monospace').maxLines(3).copyOption(CopyOptions.LocalDevice)
    }.alignItems(HorizontalAlign.Start).padding(14).width('100%').backgroundColor(Colors.surface).borderRadius(14).id('session.git.summary')
  }
  @Builder changesHeader() {
    Row({space:6}) {
      Text(tr('session_tools_changes')).fontSize(17).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
      Text(String(this.workspace.gitFiles.length)).fontSize(14).fontColor(Colors.secondary)
      Blank().layoutWeight(1)
      if (this.workspace.gitFiles.length) {
        Text('+'+this.workspace.gitFiles.reduce((n:number,f:WorkspaceGitFile) => n+f.added,0)).fontSize(13).fontColor(Colors.positive)
        Text('−'+this.workspace.gitFiles.reduce((n:number,f:WorkspaceGitFile) => n+f.deleted,0)).fontSize(13).fontColor(Colors.danger)
        SymbolGlyph(this.collapseChanges ? Symbols.expand : Symbols.down).fontSize('12vp').fontColor([Colors.secondary])
      }
    }.width('100%').constraintSize({minHeight:40}).padding({left:5,right:6}).onClick(() => this.collapseChanges=!this.collapseChanges)
      .id('session.git.changes_header')
  }
  @Builder gitFilters() {
    Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(3),cross:LengthMetrics.vp(3)}}) {
      ForEach(this.filterKeys(),(key:string) => {
        Button((key==='all' ? tr('all') : tr(key==='untracked' ? 'session_git_untracked' : 'session_tools_'+key))+' '+this.filterCount(key),{type:ButtonType.Normal})
          .height(44).flexGrow(1).padding({left:7,right:7}).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale)
          .fontColor(this.gitFilter===key ? Colors.ink : Colors.secondary)
          .backgroundColor(this.gitFilter===key ? Colors.surface : Color.Transparent).borderRadius(8)
          .accessibilitySelected(this.gitFilter===key).onClick(() => this.gitFilter=key).id('session.git.filter.'+key)
      },(key:string) => key)
    }.width('100%').padding(3).borderRadius(11).backgroundColor(Colors.control)
  }
  @Builder gitFileRow(binding:BoundRow<WorkspaceGitFile>) {
    Row({space:8}) {
      if (this.workspace.canWriteGit()) Button({type:ButtonType.Normal}) {
        Stack() {
          if (binding.value.staged) Text(binding.value.unstaged ? '−' : '✓').fontWeight(FontWeight.Bold).fontSize('13vp').fontColor(Colors.onAction)
        }.width(19).height(19).border({width:1.2,color:binding.value.staged ? Colors.link : Colors.secondary,radius:5})
          .backgroundColor(binding.value.staged ? Colors.link : Color.Transparent)
      }.width(44).height(44).padding(0).backgroundColor(Color.Transparent)
        .accessibilityText(tr(binding.value.staged && !binding.value.unstaged ? 'session_tools_unstage' : 'session_tools_stage')+' '+binding.value.path)
        .onClick(() => this.confirmGitWrite(binding.value.staged && !binding.value.unstaged ? 'unstage' : 'stage',binding.value.path))
        .id('session.git.toggle.'+binding.value.path)
      Row({space:8}) {
        Text(binding.value.mark).width(23).fontSize('13vp').textAlign(TextAlign.Center).fontWeight(FontWeight.Bold)
          .fontColor(this.fileInk(binding.value)).padding({top:3,bottom:3}).backgroundColor(Colors.control).borderRadius(5)
        Column({space:3}) {
          Text(this.leaf(binding.value.path)).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD)
          if (binding.value.untracked || binding.value.mark==='!') Text(tr(binding.value.untracked ? 'session_git_untracked_file' : 'session_git_change_conflict'))
            .fontSize(12).fontColor(this.fileInk(binding.value))
          if (binding.value.path.includes('/') || binding.value.originalPath) Text(binding.value.originalPath ?
            binding.value.originalPath+' → '+binding.value.path : binding.value.path.slice(0,binding.value.path.lastIndexOf('/')))
            .fontSize(12).fontColor(Colors.secondary).wordBreak(WordBreak.BREAK_WORD)
          if (nativeEnvironment.fontScale >= 1.5 && (binding.value.binary || binding.value.added || binding.value.deleted)) {
            if (binding.value.binary) Text(tr('session_tools_binary_file')).fontSize(11).fontColor(Colors.secondary)
            else Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(5)}}) { this.lineCounts(binding.value.added,binding.value.deleted) }.width('100%')
          }
        }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        if (nativeEnvironment.fontScale < 1.5) {
          if (binding.value.binary) Text(tr('session_tools_binary_file')).fontSize(11).fontColor(Colors.secondary)
          else if (binding.value.added || binding.value.deleted) Column({space:1}) {
            this.lineCounts(binding.value.added,binding.value.deleted)
          }.alignItems(HorizontalAlign.End)
        }
        SymbolGlyph(Symbols.expand).fontSize('11vp').fontColor([Colors.secondary])
      }.layoutWeight(1).constraintSize({minHeight:48}).onClick(() => this.openGitFile(binding.value,this.gitFilter==='all' ? undefined : this.gitFilter==='staged'))
        .bindContextMenu(this.gitFileMenu(binding),ResponseType.LongPress).id('session.git.'+binding.value.path)
    }.width('100%').padding({left:11,right:12,top:3,bottom:3}).backgroundColor(Colors.surface)
  }
  @Builder commitRow(c:Record<string,Object>) {
    Row({space:10}) {
      Column({space:4}) {
        Text(Wire.text(c,'subject') || Wire.text(c,'short')).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
        Text([Wire.text(c,'short') || Wire.text(c,'hash').slice(0,8),Wire.text(c,'author'),this.timeLabel(Wire.text(c,'date'))]
          .filter((v:string) => !!v).join(' · ')).fontSize(12).fontColor(Colors.secondary).maxLines(2)
        if (Wire.text(c,'refs')) Text(Wire.text(c,'refs').replace(/HEAD -> /g,'')).fontSize(12).fontColor(Colors.link).maxLines(2)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').padding({left:13,right:12,top:9,bottom:9}).backgroundColor(Colors.surface)
      .onClick(() => this.inspectCommit(c)).id('session.git.commit.'+Wire.text(c,'hash'))
  }
  private gitModalTitle():string {
    if (this.gitSheetMode==='diff') return this.leaf(this.workspace.gitDiffPath) || tr('git');
    if (this.gitSheetMode==='history') {
      if (this.commitFilePath) return this.leaf(this.commitFilePath);
      const summary = this.workspace.gitCommits.find((c: Record<string,Object>) => Wire.text(c,'hash') === this.workspace.gitCommitHash);
      return Wire.text(Wire.object(this.workspace.gitShow,'commit'),'short') ||
        (summary ? Wire.text(summary,'short') : '') || this.workspace.gitCommitHash.slice(0,8) || tr('session_git_history');
    }
    return tr(this.gitSheetMode==='commit' ? 'session_tools_commit' : this.gitSheetMode==='branch' ? 'session_tools_switch_branch' : 'session_tools_stash_save');
  }
  @Builder gitModal() {
    if (this.gitSheetMode==='diff') this.diffViewer()
    else if (this.gitSheetMode==='history') this.commitViewer()
    else this.gitSheet()
  }
  @Builder stagedCommitBar() {
    Row({space:8}) {
      SymbolGlyph($r('sys.symbol.checkmark_square_fill')).fontSize('18vp').fontColor([this.filterCount('staged') ? Colors.positive : Colors.secondary])
      Column({space:1}) {
        Text(tr('session_tools_staged')+' '+String(this.filterCount('staged'))).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        Text(tr('session_tools_changes')+' '+String(this.workspace.gitFiles.length)).fontSize(12).fontColor(Colors.secondary)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      Button(tr('session_tools_commit')).fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).padding({left:14,right:14}).constraintSize({minHeight:44})
        .enabled(this.workspace.canWriteGit() && !!Wire.text(this.workspace.gitStatus,'branch') && this.filterCount('staged') > 0)
        .backgroundColor(Colors.action).fontColor(Colors.onAction).onClick(() => this.openGitSheet('commit')).id('session.git.commit.new')
    }.width('100%').padding({left:12,right:9,top:6,bottom:6}).backgroundColor(Colors.surface)
      .border({width:0.5,color:Colors.divider,radius:14}).borderRadius(14).id('session.git.staged_summary')
  }

  @Builder gitView() {
    Column() {
      if (this.workspace.gitError) Row() {
        Text(trError(this.workspace.gitError)).fontSize(14).fontColor(Colors.danger).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.workspace.loadGit())
      }.width('100%').padding(10)
      if (this.workspace.gitWriteError) Text(trError(this.workspace.gitWriteError)).fontSize(14).fontColor(Colors.danger).padding(8)
      if (this.workspace.gitWriteState==='pending' || this.workspace.gitWriteState==='unknown')
        Text(tr('session_tools_write_'+this.workspace.gitWriteState)).fontSize(14).fontColor(Colors.danger).padding(8)
      if (Object.keys(this.workspace.gitStatus).length && Wire.bool(this.workspace.gitStatus,'is_repo')) {
        List({space:3}) {
          ListItem() { this.gitBranchCard() }
          ListItem() { this.changesHeader() }
          if (!this.collapseChanges) {
            if (this.workspace.gitFiles.length) ListItem() { this.gitFilters() }
            ForEach(this.visibleGitRows,(binding:BoundRow<WorkspaceGitFile>) => {
              ListItem() { this.gitFileRow(binding) }
            },(binding:BoundRow<WorkspaceGitFile>) => binding.key)
            if (!this.visibleGitRows.length) ListItem() {
              Row({space:7}) {
                SymbolGlyph(this.workspace.gitFiles.length ? Symbols.filter : Symbols.check).fontSize('16vp').fontColor([this.workspace.gitFiles.length ? Colors.secondary : Colors.positive])
                Text(tr(!this.workspace.gitFiles.length ? 'session_tools_clean' : 'no_results')).fontSize(15).fontColor(Colors.secondary)
              }.width('100%').padding(14).borderRadius(12).backgroundColor(Colors.surface)
            }
          }
          if (this.workspace.gitStashes.length) ListItem() { Text(tr('session_tools_stashes')).fontSize(17).fontWeight(FontWeight.Bold).padding(10) }
          ForEach(this.stashRowBindings,(binding:BoundRow<Record<string,Object>>) => {
            ListItem() {
              Row({space:8}) {
                Text('stash@{'+String(Wire.number(binding.value,'index'))+'}').fontSize(13).fontFamily('monospace')
                Text(Wire.text(binding.value,'message')).fontSize(14).layoutWeight(1).maxLines(2)
              }.width('100%').padding(12).backgroundColor(Colors.surface)
            }.bindContextMenu(this.gitStashMenu(binding),ResponseType.LongPress).id('session.git.stash.'+Wire.text(binding.value,'oid'))
          },(binding:BoundRow<Record<string,Object>>) => binding.key)
          if (this.workspace.gitCommits.length) ListItem() {
            Text(tr('session_git_history')).fontSize(17).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
              .padding({left:5,top:14,bottom:7}).width('100%').id('session.git.history')
          }
          ForEach(this.workspace.gitCommits,(commit:Record<string,Object>) => {
            ListItem() { this.commitRow(commit) }
          },(commit:Record<string,Object>) => Wire.text(commit,'hash')+'|'+Wire.text(commit,'refs'))
          if (this.workspace.gitMoreCommits) ListItem() {
            Button(tr(this.workspace.gitHistoryError ? 'retry' : 'session_git_earlier'),{buttonStyle:ButtonStyleMode.TEXTUAL})
              .height(44).width('100%').fontColor(Colors.link).enabled(!this.workspace.gitHistoryBusy)
              .onClick(() => this.workspace.olderGitCommits()).id('session.git.older')
          }
          if (this.workspace.gitHistoryBusy) ListItem() { LoadingProgress().width(25).height(25) }
          if (this.workspace.gitHistoryError) ListItem() { Text(tr('session_git_history_error')).fontSize(14).fontColor(Colors.danger).padding(9) }
        }.width('100%').layoutWeight(1).padding({left:12,right:12,top:6,bottom:12})
        if (this.workspace.gitFiles.length > 0 && this.workspace.canWriteGit()) Row() { this.stagedCommitBar() }
          .width('100%').padding({left:12,right:12,top:2,bottom:8})
      } else if (Object.keys(this.workspace.gitStatus).length) {
        Column({space:6}) {
          Text(tr('session_tools_not_repository')).fontSize(17).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          Text(this.workspace.cwd).fontFamily('monospace').fontSize(13).fontColor(Colors.secondary).copyOption(CopyOptions.LocalDevice)
          Button(tr('refresh'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.workspace.loadGit())
        }.width('100%').alignItems(HorizontalAlign.Start).padding(15)
      } else if (!this.workspace.gitError) LoadingProgress().width(28).height(28).margin(16)
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
      .bindSheet($$this.gitSheetOpen,this.gitModal(),{title:{title:this.gitModalTitle()},height:this.gitSheetMode==='diff' || this.gitSheetMode==='history' ? SheetSize.LARGE : SheetSize.MEDIUM,showClose:true})
  }
  private branchOptions(): SelectOption[] { return this.workspace.gitBranches.map((branch: WorkspaceBranch): SelectOption => ({ value: this.branchLabel(branch) })); }
  @Builder gitSheet() {
    Column({ space: Tokens.gap }) {
      if (this.gitSheetMode === 'commit') {
        TextArea({ text: this.gitDraftMessage, placeholder: tr('session_tools_commit_message') }).height(Tokens.inputExpanded)
          .onChange((value: string) => this.gitDraftMessage = value).id('session.git.commit_message')
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_commit')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_commit', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_commit')).bindTips(tr('session_tools_commit')).enabled(this.workspace.canWriteGit() && !!this.gitDraftMessage.trim() && !this.workspace.gitWriteBusy).onClick(() => this.confirmGitWrite('commit')).id('session.git.commit')
      } else if (this.gitSheetMode === 'branch') {
        if (this.workspace.gitBranches.length) {
          Select(this.branchOptions()).selected(Math.max(0, this.gitSelectedBranch)).value(this.branchLabel(this.workspace.gitBranches[this.gitSelectedBranch] || this.workspace.gitBranches[0]))
            .onSelect((index: number) => this.gitSelectedBranch = index).id('session.git.branch')
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_switch_branch')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_switch_branch', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_switch_branch')).bindTips(tr('session_tools_switch_branch')).enabled(this.workspace.canWriteGit() && !this.workspace.gitBranches[this.gitSelectedBranch]?.current).onClick(() => this.confirmGitWrite('switch')).id('session.git.switch')
        } else Text(tr('session_tools_no_branches')).fontSize(Tokens.meta)
        TextInput({ text: this.gitNewBranchName, placeholder: tr('session_tools_new_branch') })
          .onChange((value: string) => this.gitNewBranchName = value).id('session.git.new_branch')
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_create_branch')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_create_branch', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_create_branch')).bindTips(tr('session_tools_create_branch')).enabled(this.workspace.canWriteGit() && !!this.gitNewBranchName.trim()).onClick(() => this.confirmGitWrite('create')).id('session.git.create_branch')
      } else if (this.gitSheetMode === 'stash_push') {
        TextInput({ text: this.gitStashMessage, placeholder: tr('session_tools_stash_message') })
          .onChange((value: string) => this.gitStashMessage = value).id('session.git.stash_message')
        Row({ space: Tokens.gap }) {
          Text(tr('session_tools_stash_untracked')).layoutWeight(1).fontSize(Tokens.body)
          Toggle({ type: ToggleType.Switch, isOn: this.gitStashUntracked }).onChange((value: boolean) => this.gitStashUntracked = value)
            .accessibilityText(tr('session_tools_stash_untracked')).id('session.git.stash_untracked')
        }.width('100%')
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_stash_save')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_stash_save', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_stash_save')).bindTips(tr('session_tools_stash_save')).enabled(this.workspace.canWriteGit() && !this.workspace.gitWriteBusy).onClick(() => this.confirmGitWrite('stash_push')).id('session.git.stash_save')
      }
      if (this.workspace.gitWriteState === 'pending' || this.workspace.gitWriteState === 'unknown')
        Text(tr('session_tools_write_' + this.workspace.gitWriteState)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.workspace.gitWriteError) Text(trError(this.workspace.gitWriteError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger'))
      if (this.workspace.gitWriteBusy) LoadingProgress().height(Tokens.touch).width(Tokens.touch)
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('cancel')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('cancel', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('cancel')).bindTips(tr('cancel')).onClick(() => this.gitSheetOpen = false)
    }.alignItems(HorizontalAlign.Start).padding(Tokens.page).width('100%')
  }

  private async copyPatch(value:string):Promise<void> {
    if (!value) return;
    const target=this.workspace.currentTarget(), revision=this.workspace.currentRevision();
    try {
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,value));
    } catch (_) {
      if (this.workspace.isCurrent() && this.workspace.currentTarget()===target && this.workspace.currentRevision()===revision)
        this.getUIContext().getPromptAction().showToast({message:tr('failed')});
    }
  }
  @Builder diffViewer() {
    Column({space:7}) {
      Row({space:8}) {
        Text(this.workspace.gitDiffPath).fontSize(14).fontColor(Colors.secondary).layoutWeight(1).maxLines(3).copyOption(CopyOptions.LocalDevice)
        if (this.workspace.gitDiff) Button({type:ButtonType.Circle}) { SymbolGlyph(IconActions.symbol('copy')).fontSize('16vp').fontColor([Colors.link]) }
          .width(44).height(44).padding(0).backgroundColor(Colors.control).accessibilityText(tr('copy'))
          .onClick(() => this.copyPatch(this.workspace.gitDiff)).id('session.git.diff.copy')
      }.width('100%')
      if (this.gitDiffHasBoth()) Row({space:3}) {
        ForEach(['session_tools_unstaged','session_tools_staged'],(key:string,i:number) => {
          Button(tr(key),{type:ButtonType.Normal}).layoutWeight(1).height(44).fontSize(14).maxFontScale(Tokens.navigationTextMaxScale).padding(0)
            .fontColor(this.workspace.gitDiffStaged===(i===1) ? Colors.ink : Colors.secondary)
            .backgroundColor(this.workspace.gitDiffStaged===(i===1) ? Colors.surface : Color.Transparent).borderRadius(8)
            .onClick(() => { if (this.workspace.gitDiffStaged!==(i===1)) { this.workspace.gitDiffStaged=i===1; this.workspace.loadGitDiff(); } })
            .id(i ? 'session.git.diff.staged' : 'session.git.diff.unstaged')
        },(key:string) => key)
      }.width('100%').padding(3).borderRadius(10).backgroundColor(Colors.control)
      if (this.workspace.gitDiffError) Row() {
        Text(trError(this.workspace.gitDiffError)).fontSize(14).fontColor(Colors.danger).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.workspace.loadGitDiff())
      }.width('100%')
      if (this.workspace.gitDiffBusy) LoadingProgress().height(28).width(28)
      else if (this.workspace.gitDiff) WorkspacePatch({patch:this.workspace.gitDiff, fileOnly:true}).layoutWeight(1)
      else if (!this.workspace.gitDiffError) Text(tr('session_tools_no_diff')).fontSize(15).fontColor(Colors.secondary)
      if (this.workspace.gitDiffTruncated) Text(tr('session_tools_truncated')).fontSize(13).fontColor(Colors.secondary)
    }.padding({left:12,right:12,top:8,bottom:16}).width('100%').height('100%').alignItems(HorizontalAlign.Start)
  }

  @Computed get selectedCommitPatch():string {
    return commitFilePatch(Wire.text(this.workspace.gitShow, 'diff'), this.commitFilePath);
  }

  @Builder commitInfo(commit: Record<string,Object>) {
    Column({space:5}) {
      Text(Wire.text(commit,'subject')).fontSize(18).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
      if (Wire.text(commit,'body')) Text(Wire.text(commit,'body')).fontSize(15).fontColor(Colors.secondary)
      Text([Wire.text(commit,'author'),this.gitDate(Wire.text(commit,'date'))].filter((v:string) => !!v).join(' · '))
        .fontSize(13).fontColor(Colors.secondary)
      Button({type:ButtonType.Normal}) {
        Row({space:7}) {
          SymbolGlyph(IconActions.symbol('copy')).fontSize('14vp').fontColor([Colors.link])
          Text(this.workspace.gitCommitHash).fontSize(12).fontFamily('monospace').fontColor(Colors.link)
            .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).ellipsisMode(EllipsisMode.CENTER).layoutWeight(1)
        }.width('100%')
      }.width('100%').height(Math.max(44,16*nativeEnvironment.fontScale+10)).padding(0).backgroundColor(Color.Transparent)
        .accessibilityText(tr('copy')+' '+this.workspace.gitCommitHash).onClick(() => this.copyPatch(this.workspace.gitCommitHash)).id('session.git.commit.copy')
    }.width('100%').padding(13).alignItems(HorizontalAlign.Start).backgroundColor(Colors.surface).borderRadius(14)
  }

  @Builder lineCounts(added:number,deleted:number) {
    Text('+'+added).fontSize(12).fontColor(Colors.positive)
    Text('−'+deleted).fontSize(12).fontColor(Colors.danger)
  }

  @Builder commitFile(file: Record<string,Object>) {
    Row({space:8}) {
      SymbolGlyph(Symbols.document).fontSize('16vp').fontColor([Colors.secondary]).width(20).flexShrink(0)
        .id('session.commitRow.icon.'+Wire.text(file,'path'))
      Column({space:2}) {
        Text(this.leaf(Wire.text(file,'path'))).fontSize(16).fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD)
          .id('session.commitRow.name.'+Wire.text(file,'path'))
        if (Wire.text(file,'path').includes('/')) Text(Wire.text(file,'path').split('/').slice(0,-1).join('/'))
          .fontSize(12).fontColor(Colors.secondary).wordBreak(WordBreak.BREAK_WORD)
        if (nativeEnvironment.fontScale >= 1.5) Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(5)}}) {
          this.lineCounts(Wire.number(file,'added'),Wire.number(file,'deleted'))
        }.width('100%')
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (nativeEnvironment.fontScale < 1.5) {
        this.lineCounts(Wire.number(file,'added'),Wire.number(file,'deleted'))
      }
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').constraintSize({minHeight:46}).padding({left:12,right:12,top:5,bottom:5})
      .backgroundColor(Colors.surface).accessibilityGroup(true).accessibilityText(Wire.text(file,'path'))
      .onClick(() => this.commitFilePath=Wire.text(file,'path')).id('session.git.commit.file.'+Wire.text(file,'path'))
  }

  @Builder commitViewer() {
    Column({space:9}) {
      if (this.workspace.gitShowBusy) LoadingProgress().height(28).width(28)
      if (this.workspace.gitShowError) Row() {
        Text(tr('session_git_history_error')).fontSize(14).fontColor(Colors.danger).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.workspace.showGitCommit(this.workspace.gitCommitHash))
      }.width('100%')
      if (Object.keys(this.workspace.gitShow).length) {
        if (this.commitFilePath || this.showCommitPatch) {
          Row({space:7}) {
            Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.collapse).fontSize('17vp').fontColor([Colors.link]) }
              .width(44).height(44).padding(0).backgroundColor(Colors.surface).accessibilityText(tr('back'))
              .onClick(() => { this.commitFilePath=''; this.showCommitPatch=false; }).id('session.git.commit.back')
            Text(this.commitFilePath || tr('session_git_patch')).fontSize(14).fontColor(Colors.secondary).layoutWeight(1)
            if (this.selectedCommitPatch || !this.commitFilePath && Wire.text(this.workspace.gitShow,'diff')) {
              Button({type:ButtonType.Circle}) { SymbolGlyph(IconActions.symbol('copy')).fontSize('16vp').fontColor([Colors.link]) }
                .width(44).height(44).padding(0).backgroundColor(Colors.surface).accessibilityText(tr('copy'))
                .onClick(() => this.copyPatch(this.commitFilePath ? this.selectedCommitPatch : Wire.text(this.workspace.gitShow,'diff'))).id('session.git.commit.patch.copy')
            }
          }.width('100%')
          if (this.commitFilePath && !this.selectedCommitPatch) {
            Text(tr(Wire.bool(this.workspace.gitShow,'truncated') ? 'session_tools_truncated' : 'session_tools_no_diff')).fontColor(Colors.secondary)
          } else WorkspacePatch({patch:this.commitFilePath ? this.selectedCommitPatch : Wire.text(this.workspace.gitShow,'diff'), fileOnly:!!this.commitFilePath}).layoutWeight(1)
        } else {
          List({space:0}) {
            ListItem() { this.commitInfo(Wire.object(this.workspace.gitShow,'commit')) }
            ListItem() {
              Text(tr('files')+' · '+String(Wire.rows(this.workspace.gitShow,'files').length))
                .fontSize(14).fontColor(Colors.secondary).padding({left:9,top:18,bottom:7})
            }
            ForEach(Wire.rows(this.workspace.gitShow,'files'),(file:Record<string,Object>) => {
              ListItem() { this.commitFile(file) }
            },(file:Record<string,Object>) => Wire.text(file,'path'))
            if (Wire.text(this.workspace.gitShow,'diff')) ListItem() {
              Button(tr('session_git_patch'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.link)
                .onClick(() => this.showCommitPatch=true).id('session.git.commit.toggle_patch')
            }
          }.width('100%').layoutWeight(1)
        }
        if (Wire.bool(this.workspace.gitShow,'truncated') && (!this.commitFilePath || this.selectedCommitPatch))
          Text(tr('session_tools_truncated')).fontSize(13).fontColor(Colors.secondary)
      }
    }.width('100%').height('100%').padding(14).alignItems(HorizontalAlign.Start).id('session.git.commit_details')
  }
  @Builder terminalView() {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Text(tr('terminal')).fontSize(Tokens.title).layoutWeight(1)
        if (this.workspace.terminalControlState === 'active') Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('release')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('release', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('release')).bindTips(tr('release')).onClick(() => this.workspace.setTerminalControl(false))
        else if (this.workspace.terminalControlState === 'busy') Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('takeover')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('takeover', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('takeover')).bindTips(tr('takeover')).enabled(this.workspace.canControlTerminal())
          .onClick(() => this.confirmTerminalControl(true))
        else Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('control')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('control', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('control')).bindTips(tr('control')).enabled(this.workspace.canControlTerminal()).onClick(() => this.confirmTerminalControl())
      }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.gap })
      Text(this.controlDescription()).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).padding({ left: Tokens.page, right: Tokens.page })
      if (this.workspace.terminalError) Text(trError(this.workspace.terminalError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      Scroll() {
        Text(this.workspace.terminalText || tr('session_tools_terminal_waiting'))
          .fontFamily('monospace').fontSize(Tokens.meta).width('100%').textAlign(TextAlign.Start).copyOption(CopyOptions.LocalDevice)
      }.layoutWeight(1).width('100%').onAreaChange((oldArea: Area, newArea: Area) => this.measureTerminal(newArea))
      if (this.workspace.terminalControlState === 'active') {
        TextArea({ text: this.terminalInput, placeholder: tr('session_tools_terminal_input') })
          .height(Tokens.inputExpanded / 2).onChange((value: string) => this.terminalInput = value)
        Row({ space: Tokens.gap }) {
          Button('Esc').onClick(() => this.workspace.sendTerminalKey('escape'))
          Button('Tab').onClick(() => this.workspace.sendTerminalKey('tab'))
          Button('↑').onClick(() => this.workspace.sendTerminalKey('up'))
          Button('↓').onClick(() => this.workspace.sendTerminalKey('down'))
          Button('Ctrl-C').onClick(() => this.workspace.sendTerminalKey('ctrl_c'))
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('send')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('send', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('send')).bindTips(tr('send')).onClick(() => this.sendTerminalInput())
        }.padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })
      }
    }.width('100%').height('100%')
  }

  @Builder terminalScreenView() {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Text(tr('session_tools_terminal_screen')).fontSize(Tokens.title).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('refresh')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('refresh', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('refresh')).bindTips(tr('refresh')).onClick(() => this.workspace.refreshScreen())
        if (this.workspace.screenControlRequested) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('release')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('release', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('release')).bindTips(tr('release')).onClick(() => this.workspace.revokeScreenControl())
        else Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('control')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('control', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('control')).bindTips(tr('control')).enabled(this.workspace.canControlScreen()).onClick(() => this.confirmScreenControl())
      }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.gap })
      Text(tr(this.workspace.screenControlRequested ? 'session_tools_terminal_screen_control_active' : 'session_tools_terminal_screen_view_only'))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).padding({ left: Tokens.page, right: Tokens.page })
      if (this.workspace.screenError) Text(trError(this.workspace.screenError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      if (this.transferError) Text(this.transferError).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      Scroll() {
        Text(this.workspace.screenText || tr('session_tools_terminal_waiting'))
          .fontFamily('monospace').fontSize(Tokens.meta).width('100%').textAlign(TextAlign.Start).copyOption(CopyOptions.LocalDevice)
      }.layoutWeight(1).width('100%')
      if (this.workspace.screenControlRequested) {
        TextInput({ text: this.screenInput, placeholder: tr('session_tools_terminal_screen_input') })
          .height(Tokens.touch).onChange((value: string) => this.screenInput = value)
        Row({ space: Tokens.gap }) {
          Button('Esc').onClick(() => this.sendTerminalScreenKey('esc'))
          Button('Tab').onClick(() => this.sendTerminalScreenKey('tab'))
          Button('↑').onClick(() => this.sendTerminalScreenKey('up'))
          Button('↓').onClick(() => this.sendTerminalScreenKey('down'))
          Button('Ctrl-C').onClick(() => this.sendTerminalScreenKey('ctrl+c'))
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('send')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('send', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('send')).bindTips(tr('send')).onClick(() => this.sendTerminalScreenText())
        }.padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })
      }
    }.width('100%').height('100%')
  }

  @Builder previewServerRow(binding: BoundRow<WorkspacePreviewServer>) {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Text(binding.value.name).fontSize(Tokens.body).fontWeight(FontWeight.Medium).layoutWeight(1).maxLines(1)
        Text(binding.value.listening ? tr('session_tools_preview_listening') : tr('session_tools_preview_stopped'))
          .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      }
      if (binding.value.title) Text(binding.value.title).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(2)
      if (binding.value.problem) Text(tr('session_tools_preview_problem') + ': ' + binding.value.problem).fontSize(Tokens.meta)
        .fontColor($r('app.color.persona_danger'))
      if (binding.value.command.length) Text(this.previewCommand(binding.value)).fontSize(Tokens.meta).fontFamily('monospace').maxLines(3)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
      Text(binding.value.cwd || this.workspace.cwd).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(2)
      if (binding.value.listening && binding.value.port > 0) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_preview_open')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_preview_open', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_preview_open')).bindTips(tr('session_tools_preview_open')).enabled(this.workspace.canUsePreview()).onClick(() => this.openReadyPreview(binding.value))
      else if (binding.value.command.length) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('start')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('start', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('start')).bindTips(tr('start')).enabled(this.workspace.canStartPreview() && !binding.value.problem).onClick(() => this.confirmStartPreview(binding.value))
    }.alignItems(HorizontalAlign.Start).padding(Tokens.inset).width('100%')
  }

  @Builder previewView() {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Text(tr('preview')).fontSize(Tokens.title).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('refresh')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('refresh', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('refresh')).bindTips(tr('refresh')).onClick(() => this.workspace.loadPreviewServers())
        if (this.workspace.previewState === 'live') {
          Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.collapse).fontSize(Tokens.symbolFont).fontColor([IconActions.color('back', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('back')).bindTips(tr('back')).onClick(() => this.previewAction('back'))
          Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.expand).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_forward', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_forward')).bindTips(tr('session_tools_forward')).onClick(() => this.previewAction('forward'))
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('refresh')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('refresh', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('refresh')).bindTips(tr('refresh')).onClick(() => this.previewAction('reload'))
        }
      }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.gap })
      if (this.workspace.previewError) Text(trError(this.workspace.previewError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      if (this.transferError) Text(this.transferError).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset)
      if (this.workspace.previewState === 'starting' || this.workspace.previewState === 'opening' || this.workspace.previewState === 'connecting') LoadingProgress()
        .height(Tokens.touch).width(Tokens.touch)
      if (this.workspace.previewMessage) Text(this.workspace.previewMessage).fontSize(Tokens.meta).maxLines(1)
        .fontColor($r('sys.color.font_secondary')).padding({ left: Tokens.inset, right: Tokens.inset })
      if (this.workspace.previewState === 'live' && this.previewPixels) {
        Row({ space: Tokens.gap }) {
          TextInput({ text: this.previewPathDraft, placeholder: tr('session_tools_preview_path') }).layoutWeight(1)
            .onChange((value: string) => this.previewPathDraft = value).height(Tokens.touch)
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('open')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('open', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('open')).bindTips(tr('open')).onClick(() => this.sendPreviewNavigation())
        }.padding({ left: Tokens.inset, right: Tokens.inset })
        Image(this.previewPixels).objectFit(ImageFit.Contain).aspectRatio(this.previewRatio).width('100%').layoutWeight(1)
        Row({ space: Tokens.gap }) {
          TextInput({ text: this.previewInput, placeholder: tr('session_tools_preview_input') }).layoutWeight(1)
            .onChange((value: string) => this.previewInput = value).height(Tokens.touch)
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('send')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('send', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('send')).bindTips(tr('send')).onClick(() => this.sendPreviewText())
        }.padding({ left: Tokens.inset, right: Tokens.inset })
        if (this.workspace.previewExternalUrl) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('session_tools_preview_external_link')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('session_tools_preview_external_link', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('session_tools_preview_external_link')).bindTips(tr('session_tools_preview_external_link'))
          .onClick(() => this.openPreviewExternal())
      } else {
        List() {
          ForEach(this.previewRowBindings, (binding: BoundRow<WorkspacePreviewServer>) => ListItem() { this.previewServerRow(binding) }
            .id('session.preview.' + binding.value.name), (binding: BoundRow<WorkspacePreviewServer>) => binding.key)
          if (!this.workspace.previewServers.length && !this.workspace.previewError && this.workspace.previewState !== 'unsupported')
            ListItem() { Text(tr('session_tools_preview_none')).fontSize(Tokens.meta).padding(Tokens.page) }
        }.layoutWeight(1).width('100%')
      }
    }.width('100%').height('100%')
  }

  private sendTerminalScreenKey(key: string): void {
    const revision = this.workspace.screenRevision;
    this.workspace.sendScreenKeys([key], '', revision).then((ok: boolean) => {
      if (!ok && this.workspace.screenError) this.transferError = trError(this.workspace.screenError);
    });
  }

  private controlDescription(): string {
    const state = this.workspace.terminalControlState;
    if (state === 'active') return tr('session_tools_control_active');
    if (state === 'starting') return tr('session_tools_control_starting');
    if (state === 'busy') return tr('session_tools_control_busy');
    if (state === 'unsupported') return tr('session_tools_view_only');
    return tr('session_tools_control_off');
  }

  private sendTerminalInput(): void {
    if (!this.terminalInput) return;
    this.workspace.sendTerminalText(this.terminalInput);
    this.workspace.sendTerminalKey('enter');
    this.terminalInput = '';
  }

  private measureTerminal(area: Area): void {
    const width = Number(area.width), height = Number(area.height);
    if (width > 0 && height > 0) this.workspace.resizeTerminal(Math.floor(width / 8), Math.floor(height / 18));
  }

  private async pickUpload(): Promise<void> {
    this.transferError = '';
    this.pickerBusy = true;
    try {
      const options = new picker.DocumentSelectOptions();
      options.maxSelectNumber = 1;
      const viewPicker = new picker.DocumentViewPicker(this.uiContext());
      const uris = await viewPicker.select(options);
      if (uris.length === 0) return;
      const selected = this.readPickedFile(uris[0]);
      await this.workspace.uploadFile(selected.name, selected.data);
    } catch (error) { this.transferError = this.errorText(error as Error); }
    finally { this.pickerBusy = false; }
  }

  private readPickedFile(uri: string): PickedFile {
    const info = fileIo.statSync(uri);
    if (info.size <= 0 || info.size > TRANSFER_LIMIT) throw new Error('transfer_limit');
    const file = fileIo.openSync(uri, fileIo.OpenMode.READ_ONLY);
    let bytes: ArrayBuffer = new ArrayBuffer(0);
    try {
      const buffer = new ArrayBuffer(info.size);
      const read = fileIo.readSync(file.fd, buffer);
      if (read !== info.size) throw new Error('file_read_failed');
      bytes = buffer;
    } finally { fileIo.closeSync(file.fd); }
    const result = new PickedFile();
    const leaf = uri.substring(uri.lastIndexOf('/') + 1);
    try { result.name = decodeURIComponent(leaf); } catch (_) { result.name = leaf; }
    if (!result.name) result.name = 'upload';
    result.data = bytes;
    return result;
  }

  private async saveDownload(): Promise<void> {
    if (!this.fileOpen || !this.workspace.selectedFile) return;
    const revision = this.workspace.currentRevision(), target = this.workspace.currentTarget(), path = this.workspace.selectedFile;
    this.transferError = '';
    this.pickerBusy = true;
    try {
      const file = await this.workspace.downloadFile();
      if (!this.fileOpen || !this.workspace.isCurrent() || revision !== this.workspace.currentRevision() ||
        target !== this.workspace.currentTarget() || path !== this.workspace.selectedFile) throw new Error('request_updated');
      const options = new picker.DocumentSaveOptions();
      options.newFileNames = [file.name];
      const viewPicker = new picker.DocumentViewPicker(this.uiContext());
      const uris = await viewPicker.save(options);
      if (uris.length === 0) return;
      if (!this.fileOpen || !this.workspace.isCurrent() || revision !== this.workspace.currentRevision() ||
        target !== this.workspace.currentTarget() || path !== this.workspace.selectedFile) throw new Error('request_updated');
      const output = fileIo.openSync(uris[0], fileIo.OpenMode.READ_WRITE);
      try {
        const written = fileIo.writeSync(output.fd, file.data);
        if (written !== file.data.byteLength) throw new Error('file_write_failed');
      } finally { fileIo.closeSync(output.fd); }
    } catch (error) { this.transferError = this.errorText(error as Error); }
    finally { this.pickerBusy = false; }
  }

  private sizeText(size: number): string {
    if (size < 1024) return String(size) + ' B';
    if (size < 1024 * 1024) return (size / 1024).toFixed(1) + ' KB';
    if (size < 1024 * 1024 * 1024) return (size / (1024 * 1024)).toFixed(1) + ' MB';
    return (size / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
  }

  @Builder workspaceTab(label:string, position:number) {
    Text(tr(label)).fontSize(14).maxFontScale(Tokens.navigationTextMaxScale).maxLines(1)
      .fontWeight(this.workspace.tab === position ? FontWeight.Bold : FontWeight.Normal)
      .fontColor(this.workspace.tab === position ? Colors.ink : Colors.secondary).padding({left:12,right:12,top:7,bottom:7})
      .backgroundColor(this.workspace.tab === position ? Colors.surface : Color.Transparent).borderRadius(15)
      .accessibilityText(tr(label)).accessibilitySelected(this.workspace.tab === position).id('session.tab.' + label)
  }
  build() {
    Tabs({ index: this.workspace.tab }) {
      TabContent() { this.fileView() }.tabBar(this.workspaceTab('files', 0))
      TabContent() { this.gitView() }.tabBar(this.workspaceTab('git', 1))
      TabContent() { this.terminalView() }.tabBar(this.workspaceTab('terminal', 2))
      TabContent() { this.terminalScreenView() }.tabBar(this.workspaceTab('session_tools_terminal_screen', 3))
      TabContent() { this.previewView() }.tabBar(this.workspaceTab('preview', 4))
    }.barMode(BarMode.Scrollable).barBackgroundColor(Colors.grouped).barHeight(nativeEnvironment.fontScale >= 1.5 ? 50 : 44).onChange((index: number) => {
      this.workspace.selectTab(index);
      if (index===0 && this.fileQuery.trim()) this.workspace.loadFileIndex();
      this.pathDraft = this.workspace.currentPath;
    }).width('100%').height('100%')
  }
}
