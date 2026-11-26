import { launcherModelLabel, startEfforts } from '../core/ModelLabel';
import { IconActions } from '../design/IconActions';
import { ThemeScope } from '../design/ThemeScope';
import { util } from '@kit.ArkTS';
import { AdminWorkspacePage } from '../components/AdminWorkspacePage';
import { ExtensionPage } from '../components/ExtensionPage';
import { ExtensionNavigationPage } from '../components/ExtensionNavigationPage';
import { ExtensionNavigation, ExtensionNavigationEntry } from '../models/ExtensionNavigation';
import { extensionIcon } from '../design/ExtensionIcons';
import type { ExtensionUINavigationEffect } from '../models/ExtensionUI';
import { ComputerScreenPage } from '../components/ComputerScreenPage';
import { isPairingAddress } from '../core/WireCompatibility';
import { detectBarcode, scanBarcode, scanCore } from '@kit.ScanKit';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { HubDemoVideo } from '../components/HubDemoVideo';
import { WorkspaceConnections } from '../components/WorkspaceConnections';
import { HubWelcome } from '../components/HubWelcome';
import { HubPairingForm } from '../components/HubPairingForm';
import { HttpFailure } from '../platform/HubTransport';
import { BusinessError } from '@kit.BasicServicesKit';
import { RemoteDocumentPage } from '../components/RemoteDocumentPage';
import { TodoWorkspacePage } from '../components/TodoWorkspacePage';
import { KnowledgeWorkspacePage } from '../components/KnowledgeWorkspacePage';
import { HealthWorkspacePage } from '../components/HealthWorkspacePage';
import { NavigationContext } from '../models/NavigationContext';
import { LinkReader } from '../components/LinkReader';
import { readerLink, readerHost } from '../platform/ReaderLink';
import { common } from '@kit.AbilityKit';
import { DevicePage } from '../components/DevicePage';
import { deviceFeatures } from '../models/DeviceFeatures';
import { SessionTools } from '../components/SessionTools';
import { SessionWorkspace } from '../models/SessionWorkspace';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { ComposeListItemV2, ContentItemV2, KeyboardAvoidMode, TextModifier, SymbolGlyphModifier, CapsuleSegmentButtonV2, SegmentButtonV2Items, ColorMetrics, LengthMetrics, uiMaterial, window } from '@kit.ArkUI';
import { ConfigurationConstant } from '@kit.AbilityKit';
import { ClientModel, client } from '../models/ClientModel';
import { ResourceRow, HubProfile, Wire, SessionLauncher, SessionModelCatalog } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { Pairing, PairingTarget } from '../platform/Pairing';
import { Conversation } from '../components/Conversation';
import { AgentListPage } from '../components/AgentListPage';
import { InteractionHistoryPage } from '../components/InteractionHistoryPage';
import { InboxWorkspacePage } from '../components/InboxWorkspacePage';
import { ResourcePage } from '../components/ResourcePage';
import { SignedInDevicesPage } from '../components/SignedInDevicesPage';
import { SettingsPage } from '../components/SettingsPage';
import { SshSettingsPage } from '../components/SshSettingsPage';
import { DeviceLook, deviceLook } from '../models/DeviceLook';
import { nativeLookTheme } from '../design/LookTheme';
import { IconTypeV2 } from '@kit.ArkUI';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { agentBrand, agentBrandColor } from '../design/AgentBrand';
import type { RelatedReadingPosition } from '../core/ConversationReading';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { AgentWorkspace } from '../models/AgentWorkspace';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';
import { nativeSheetInset } from '../platform/NativeInsets';
import { mainConversationData } from '../core/DefaultConversation';
import { AssistantDetailPage } from '../components/AssistantDetailPage';
import { tr } from '../design/Strings';
import { isHubStateKey } from '../core/StorageOwnership';

@Entry
@ComponentV2
struct Index {
  @Local model: ClientModel = client;
  @Local look: DeviceLook = deviceLook;
  @Local detailModel: ClientModel = client;
  @Local chatReady: boolean = true;
  @Local detailRoute: string = '';
  @Local detailSelection: string = '';
  private detailOwner: ClientModel | undefined;
  @Local adminRefreshRevision: number = 0;
  @Local inboxRefreshRevision: number = 0;
  @Local extensionTitle: string = '';
  @Local todoTitle: string = '';
  @Local extensionRefreshRevision: number = 0;
  @Local applications: ExtensionNavigation = new ExtensionNavigation();
  @Local shownName: string = '';
  @Local shownParam: string = '';
  @Local chatMenuActions: SheetInfo[] = [];
  @Local chatTitleMenuVisible: boolean = false;
  @Local chatBodyMenuVisible: boolean = false;
  @Local chatMenuRevision: number = 0;
  private routeRevision: number = 0;
  @Local stack: NavPathStack = new NavPathStack();
  @Local breakpoint: WidthBreakpoint = WidthBreakpoint.WIDTH_SM;
  @Local collapsed: boolean = false;
  @Local query: string = '';
  @Local filter: number = 0;
  @Local conversationRowBindings: BoundRow<ResourceRow>[] = [];
  private conversationBindingOwner: Object | undefined;
  private conversationBindingScope: string = '';
  @Local visibilityEpoch: number = 0;
  private rowVisibility: Map<string, number> = new Map();
  @Local readerAddress: string = '';
  @Local readerTitle: string = '';
  @Local readerShown: boolean = false;
  @Local sheetKind: string = '';
  @Local showSheet: boolean = false;
  @Local address: string = '';
  @Local pairCode: string = '';
  private pairingRevision: number = 0;
  @Local pairMode: string = 'scan';
  @Local pairBusy: boolean = false;
  @Local pairError: string = '';
  @Local pairTarget?: PairingTarget;
  private pendingPair?: Pairing;
  private pairOwner?: ClientModel;
  private pairGeneration: number = -1;
  private pairScope: string = '';
  private launchPairCamera: boolean = false;
  @Local resourceKind: string = 'todo';
  @Local newTask: string = '';
  @Local agentMachineId: string = '';
  @Local fixedAgentMachine: string = '';
  @Local sessionLaunchers: SessionLauncher[] = [];
  @Local launchersLoading: boolean = false;
  @Local launchersFailed: boolean = false;
  @Local startModels: Record<string, Object>[] = [];
  @Local startModel: string = '';
  @Local startEffort: string = '';
  @Local modelsLoading: boolean = false;
  @Local modelsFailed: boolean = false;
  @Local modelLocked: boolean = false;
  @Local typingStartModel: boolean = false;
  private modelRequest: number = 0;
  private launcherRequest: number = 0;
  @Local creatingAgent: boolean = false;
  @Local agentModel: ClientModel = client;
  @Local agentTodo: ResourceRow | undefined = undefined;
  private agentFormRevision: number = 0;
  @Local launcher: string = '';
  @Local directory: string = '';
  @Local settingsPage: string = '';
  @Local activeChild: string = '';
  @Local fullscreenComputer: string = '';
  @Local computerOrientation: window.Orientation = window.Orientation.AUTO_ROTATION_RESTRICTED;
  @Local computerReturnOrientation: window.Orientation | undefined = undefined;
  private computerExitTimer: number = -1;
  @Local tool: number = 0;
  private titleMaterial: uiMaterial.Material = uiMaterial.isImmersiveMaterialSupported() ? new uiMaterial.ImmersiveMaterial({ style: uiMaterial.ImmersiveStyle.THIN }) : uiMaterial.Material.empty;
  @Local agentWorkspace: AgentWorkspace = new AgentWorkspace();
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Local spacesShown: boolean = false;
  @Local spacesInset: number = 0;
  private spacesLayoutReady: boolean = false;
  @Local inlineStopIdentity: string = '';
  private defaultMainAttempt: string = '';
  private navigationRestoredScope: string = '';
  private navigationIntent: number = 0;
  private mainReadWaiters: (() => void)[] = [];
  private defaultMainPending: boolean = false;
  private creatingConversations: Map<string, number> = new Map();
  private paths: Map<string, NavPathStack> = new Map();
  private activeScope: string = '';
  private restoring: number = 0;
  private peer(): number { return this.model.section === 1 ? 1 : 0; }
  private headerHeight(): number { return Tokens.header * Math.max(1, this.environment.fontScale / 2); }
  @Local pendingInboxHandoff: string = '';
  @Local mainRowVisible: number = 0;
  private mainDestination(name: string, param: Object): boolean {
    if (name !== 'conversation') return false;
    const row = this.routeRow(param);
    return name === 'conversation' && Wire.bool(row.data(), 'navigation_root') && row.id === this.model.mainId && (!row.group || row.group === this.model.profile.id) &&
      (!row.manager || row.manager === this.model.profile.manager) && (!this.model.mainMachineId || row.machine === this.model.mainMachineId);
  }
  private hideChatTitle(name: string, param: Object): boolean {
    if (name !== 'session' && name !== 'conversation') return false;
    if (!Wire.bool(this.routeRow(param).data(), 'navigation_root')) return false;
    if (name === 'session') return this.hideAgentTitle();
    const row = this.routeRow(param), epoch = this.visibilityEpoch;
    const visible = row.id === this.model.mainId && row.machine === this.model.mainMachineId ? this.mainRowVisible : this.rowVisibility.get(this.model.scope() + this.model.target(row)) || 0;
    return name === 'conversation' && this.detailModel === this.model && this.peer() === 0 && this.wide() && !this.rail() &&
      this.model.target() === this.model.target(row) && visible >= 0.99;
  }
  @Builder peerTitle(_main: boolean = false) {
    Row({ space: Tokens.gap }) {
      CapsuleSegmentButtonV2({ items: new SegmentButtonV2Items([
        { symbol: Symbols.persona, accessibilityText: tr('persona') },
        { symbol: Symbols.agents, accessibilityText: tr('agents') }]),
        selectedIndex: this.peer(), $selectedIndex: (index: number) => this.selectPeer(index),
        itemSelectedBackgroundColor: ColorMetrics.resourceColor(Colors.action),
        itemSymbolFontColor: ColorMetrics.resourceColor(Colors.secondary),
        itemSelectedSymbolFontColor: ColorMetrics.resourceColor(Colors.onAction),
        itemSymbolFontSize: LengthMetrics.vp(Tokens.symbol),
        buttonBackgroundColor: ColorMetrics.resourceColor(Colors.control),
        itemMinHeight: LengthMetrics.vp(Tokens.touch), buttonMinHeight: LengthMetrics.vp(Tokens.touch) })
        .width(Tokens.headerPeerIcons).id('shell.peer')
      Blank().layoutWeight(1)
      this.shellMenu()
    }.width('100%').height(this.headerHeight()).padding({ left: Tokens.page }).alignItems(VerticalAlign.Center)
  }
  private selectPeer(index: number): void {
    if (index === this.model.section) return;
    this.spacesShown = false; this.changeSection(index);
    if (index === 0) this.ensureMainDestination();
  }
  private ensureAgentWorkspace(): void {
    if (!this.agentWorkspace.isBoundTo(this.model)) this.agentWorkspace.open(this.model, this.peer() === 1).catch(() => {});
  }
  @Monitor('model.ready', 'model.mainId', 'model.mainMachineId', 'model.connected') mainAvailabilityChanged(): void { this.ensureMainDestination(); }
  private ensureMainDestination(): void {
    if (!this.model.ready || !this.model.connected || this.model.section !== 0 || this.navigationRestoredScope !== this.model.scope() || this.showSheet || this.spacesShown || this.stack.size() || this.defaultMainPending || (this.creatingConversations.get(JSON.stringify([this.model.scope(), this.model.connectionGeneration()])) || 0) > 0) return;
    const attempt = JSON.stringify([this.model.scope(), this.model.connectionGeneration(), this.model.mainId, this.model.mainMachineId]);
    if (attempt === this.defaultMainAttempt) return;
    this.defaultMainAttempt = attempt; this.resolveMainDestination();
  }
  private async resolveMainDestination(): Promise<void> {
    if (this.defaultMainPending || !this.model.connected) return;
    const owner = this.model, repo = owner.repo(), generation = owner.connectionGeneration(), scope = owner.scope();
    const stack = this.stack, revision = this.routeRevision, selected = owner.selected.key(), section = owner.section, intent = this.navigationIntent;
    const names = stack.getAllPathName(), pathStamp = JSON.stringify(names.map((name: string, index: number) => [name, stack.getParamByIndex(index)]));
    const valid = (): boolean => this.model === owner && owner.connected && owner.owns(generation, scope) && owner.repo() === repo && this.stack === stack &&
      this.routeRevision === revision && this.navigationIntent === intent && !this.showSheet && !this.spacesShown && owner.section === section && owner.selected.key() === selected &&
      JSON.stringify(stack.getAllPathName().map((name: string, index: number) => [name, stack.getParamByIndex(index)])) === pathStamp;
    this.defaultMainPending = true;
    try {
      const response = await repo.call('/api/v1/assistant');
      if (!valid()) return;
      const data = mainConversationData(response);
      if (!data) return;
      const row = ResourceRow.from(data as Object as Record<string, Object>, 'conversation');
      row.group = owner.profile.id; row.manager = owner.profile.manager;
      if (!valid()) return;
      owner.mainId = row.id; owner.mainMachineId = row.machine;
      this.pushChat(row);
      this.defaultMainAttempt = JSON.stringify([owner.scope(), generation, owner.mainId, owner.mainMachineId]);
    } catch (error) { if (valid()) owner.reportError(error as Error); }
    finally { this.defaultMainPending = false; const waiting = this.mainReadWaiters; this.mainReadWaiters = []; waiting.forEach((resolve: () => void) => resolve()); if (scope !== this.model.scope() || generation !== this.model.connectionGeneration()) this.ensureMainDestination(); }
  }
  private openAssistantCard(itemId: string, kind: string): void {
    const source = this.detailModel.selected;
    if (!itemId || !this.activeChild || !['conversation', 'session'].includes(source.kind)) return;
    const row = NavigationContext.rowFrom(JSON.stringify(source));
    const data = row.data(); data['assistant_item_id'] = itemId; data['assistant_item_kind'] = kind; row.raw = JSON.stringify(data);
    this.stack.pushPath({ name: 'assistant', param: JSON.stringify(row) }); this.savePath();
  }
  private wide(): boolean { return this.breakpoint === WidthBreakpoint.WIDTH_LG || this.breakpoint === WidthBreakpoint.WIDTH_XL; }
  private rail(): boolean { return this.wide() && this.collapsed; }
  private filtered(): ResourceRow[] {
    return this.model.sessions.filter((row: ResourceRow) =>
      (!this.query || (row.title + ' ' + row.path + ' ' + row.machine).toLowerCase().includes(this.query.toLowerCase())) &&
      (this.filter === 0 || this.filter === 1 && row.state === 'needs_you' || this.filter === 2 && row.state === 'working'));
  }
  private hideAgentTitle(): boolean {
    const epoch = this.visibilityEpoch;
    const ratio = this.rowVisibility.get(this.model.scope() + this.model.selected.key()) || 0;
    return this.detailModel === this.model && this.model.section === 1 && this.wide() && !this.rail() && this.model.selected.kind === 'session' && ratio >= 0.99;
  }
  private openReader(address: string): void { const safe = readerLink(address); if (safe) { this.readerAddress = safe; this.readerTitle = readerHost(safe); this.readerShown = true; } }
  @Builder readerContent() { LinkReader({ address: this.readerAddress, onHost: (host: string) => this.readerTitle = host }) }
  private openSheet(kind: string): void {
    if (kind === 'connect') { this.openPairing('scan'); return; }
    this.navigationIntent++; this.readerShown = false;
    if (kind === 'newagent') { this.prepareAgent(); return; }
    this.sheetKind = kind; this.showSheet = true;
  }
  private openPairing(mode: string, scan: boolean = false, link: string = ''): void {
    this.navigationIntent++; this.readerShown = false;
    this.resetPairing(mode); this.address = link;
    this.launchPairCamera = scan && this.pairMode === 'scan';
    this.sheetKind = 'connect'; this.showSheet = true;
  }
  private resetPairing(mode: string): void {
    this.pairingRevision++;
    this.pairMode = mode === 'scan' && !canIUse('SystemCapability.Multimedia.Scan.ScanBarcode') ? 'link' : mode;
    this.pairTarget = undefined; this.pendingPair = undefined; this.pairOwner = undefined;
    this.address = ''; this.pairCode = ''; this.pairBusy = false; this.pairError = ''; this.launchPairCamera = false;
  }
  @Monitor('showSheet') pairingSheetChanged(): void {
    if (!this.showSheet && this.sheetKind === 'connect') this.resetPairing('scan');
  }
  private sheetAppeared(): void {
    if (this.sheetKind === 'connect' && this.showSheet && this.launchPairCamera) {
      this.launchPairCamera = false; this.scanPairing(false);
    }
  }
  private sheetTitle(): string { return tr(this.sheetKind === 'tour' ? 'hub_tour_title' : this.sheetKind === 'hub' ? 'hub' : this.sheetKind === 'newagent' ? 'agent_new_session' : 'welcome'); }
  @Builder sheetContent() {
    if (this.sheetKind === 'tour') {
      HubDemoVideo({ onDemo: () => { this.showSheet = false; this.model.useDemo(); } })
    } else if (this.sheetKind === 'hub') this.workspaceSheet()
    else if (this.sheetKind === 'newagent') this.newAgentForm()
    else this.pairingForm()
  }
  private updateBreakpoint(): void {
    const old = this.breakpoint; this.breakpoint = this.getUIContext().getWindowWidthBreakpoint();
    hilog.info(0x0000, 'PersonaLayout', 'widthBreakpoint=%{public}s', String(this.breakpoint));
    if (old !== this.breakpoint && this.wide() && this.model.section === 1 && this.stack.size() === 0 && this.model.sessions.length)
      this.openRow(this.model.sessions.find((row: ResourceRow) => row.key() === this.model.selected.key()) || this.model.sessions[0]);
  }
  private bindDeviceOwner(model: ClientModel): void {
    const host = this.getUIContext().getHostContext();
    if (host) deviceFeatures.bind(model, host as common.UIAbilityContext,
      (title: string, message: string, approve: () => void, deny: () => void): void => {
        this.getUIContext().showAlertDialog({ title: title, message: message,
          primaryButton: { value: tr('cancel'), action: deny }, secondaryButton: { value: tr('confirm'), action: approve } });
      });
  }
  private restoreDeviceOwner(owner: ClientModel): void { if (deviceFeatures.boundTo(owner)) this.bindDeviceOwner(this.model); }
  aboutToAppear(): void {
    this.syncConversationRows(); this.getUIContext().setKeyboardAvoidMode(KeyboardAvoidMode.RESIZE); this.updateBreakpoint(); this.consumePairingLink(); this.restoreScope(); this.consumeNotification();
    this.bindDeviceOwner(this.model);
    this.applications.bind(this.model); this.applications.refresh();
  }
  aboutToDisappear(): void { this.finishComputerFullscreen(); this.resetPairing('scan'); this.agentWorkspace.hide(); this.applications.dispose(); this.dismissChatActions(); }
  @Monitor('model.profile', 'model.connected', 'model.features') applicationOwnerChanged(): void {
    this.applications.bind(this.model); this.applications.connectionChanged();
  }
  @Monitor('model.profile') scopeChanged(): void { if (this.activeScope !== this.model.scope()) { this.pendingInboxHandoff = ''; this.agentWorkspace.hide(); this.spacesShown = false; this.dismissChatActions(); this.showSheet = false; this.agentFormRevision++; this.creatingAgent = false; this.agentTodo = undefined; this.readerShown = false; this.readerAddress = ''; this.readerTitle = ''; this.detailModel.hideConversation(); this.detailModel = this.model; this.chatReady = false; this.detailRoute = ''; this.routeRevision++; this.bindDeviceOwner(this.model); } this.syncConversationRows(); this.restoreScope(); deviceFeatures.context(); }
  @Monitor('model.conversations', 'model.mainId', 'model.mainMachineId') conversationsChanged(): void { this.syncConversationRows(); }
  @Monitor('model.appearance') appearanceChanged(): void {
    this.getUIContext().getHostContext()?.getApplicationContext().setColorMode(this.model.appearance === 1 ? ConfigurationConstant.ColorMode.COLOR_MODE_LIGHT : this.model.appearance === 2 ? ConfigurationConstant.ColorMode.COLOR_MODE_DARK : ConfigurationConstant.ColorMode.COLOR_MODE_NOT_SET);
  }
  @Monitor('detailModel.connected') chatMenuConnectionChanged(): void { if (!this.detailModel.connected && (this.chatTitleMenuVisible || this.chatBodyMenuVisible)) this.dismissChatActions(); }
  @Monitor('model.connected') connectionChanged(): void { deviceFeatures.context(); if (this.model.connected && this.shownName && this.activeChild === this.childKey(this.shownName, this.shownParam)) this.restoreTarget(this.shownName, this.shownParam); }
  @Monitor('model.forgottenEpoch') forgetNavigation(): void {
    for (const key of this.paths.keys()) if (isHubStateKey('view:' + key, this.model.forgottenProfile)) this.paths.delete(key);
    this.rowVisibility.clear(); this.visibilityEpoch++;
  }
  private async restoreScope(): Promise<void> {
    const scope = this.model.scope(); if (scope === this.activeScope) return;
    this.activeScope = scope; this.stack = this.paths.get(scope + ':' + this.model.section) || new NavPathStack(); this.navigationRestoredScope = ''; this.rowVisibility.clear(); const epoch = ++this.restoring, intent = this.navigationIntent;
    let view: Record<string, Object>;
    try { view = Wire.parse(await this.model.loadView('navigation')); }
    catch (_) { if (epoch === this.restoring && scope === this.model.scope()) { this.activeScope = ''; this.model.error = 'cache_unavailable'; } return; }
    if (epoch !== this.restoring || scope !== this.model.scope()) return;
    if (intent === this.navigationIntent) this.model.section = Math.max(0, Math.min(3, Wire.number(view, 'section')));
    Wire.rows(view, 'tabs').forEach((tab: Record<string, Object>) => {
      const key = scope + ':' + Wire.number(tab, 'index');
      if (!this.paths.has(key)) { const stack = new NavPathStack();
        Wire.rows(tab, 'routes').forEach((route: Record<string, Object>, index: number) => {
          const name = Wire.text(route, 'name'); let param = Wire.text(route, 'param');
          if (index === 0 && ['session', 'conversation'].includes(name)) { const row = this.routeRow(param), data = row.data(); data['navigation_root'] = true; row.raw = JSON.stringify(data); param = JSON.stringify(row); }
          stack.pushPath({ name: name, param: param }, false);
        });
        this.paths.set(key, stack);
      }
    });
    this.stack = this.paths.get(scope + ':' + this.model.section) || new NavPathStack();
    this.paths.set(scope + ':' + this.model.section, this.stack);
    this.navigationRestoredScope = scope;
    if (intent !== this.navigationIntent) this.savePath();
    if (this.model.section === 2 && !this.stack.size()) this.openResources('inbox');
    else if (this.model.section === 3 && !this.stack.size()) this.openSettingsRoot();
    else this.ensureMainDestination();
  }
  @Monitor('model.pairingLink') pairingChanged(): void { this.consumePairingLink(); }
  @Monitor('model.notificationTarget') notificationChanged(): void { this.consumeNotification(); }
  private consumeNotification(): void {
    if (!this.model.notificationTarget) return;
    const row = this.routeRow(this.model.notificationTarget); this.model.notificationTarget = '';
    if (!this.model.sessions.concat(this.model.conversations).some((item: ResourceRow) => item.key() === row.key())) return;
    this.changeSection(row.kind === 'conversation' ? 0 : 1); this.openRow(row, false);
  }
  private consumePairingLink(): void {
    if (!this.model.pairingLink) return;
    const link = this.model.pairingLink; this.model.pairingLink = ''; this.openPairing('link', false, link);
  }
  private savePath(): void {
    if (this.activeScope !== this.model.scope()) this.restoreScope();
    this.navigationIntent++;
    const key = this.model.scope() + ':' + this.model.section;
    if (this.navigationRestoredScope === this.model.scope() || this.stack.size() || this.paths.has(key)) this.paths.set(key, this.stack);
    const tabs: Record<string, Object>[] = [];
    for (let index = 0; index < 4; index++) { const stack = this.paths.get(this.model.scope() + ':' + index);
      const routes: Record<string, Object>[] = [];
      stack?.getAllPathName().forEach((name: string, position: number) => routes.push(Wire.payload(['name', 'param'], [name, stack.getParamByIndex(position) as string || ''])));
      tabs.push(Wire.payload(['index', 'routes'], [index, routes]));
    }
    if (this.navigationRestoredScope !== this.model.scope()) return;
    this.model.saveView('navigation', JSON.stringify(Wire.payload(['section', 'tabs'], [this.model.section, tabs]))).catch(() => this.model.error = 'failed');
  }
  private changeSection(index: number): void {
    this.pendingInboxHandoff = ''; this.savePath(); this.routeRevision++; this.activeChild = ''; this.shownName = ''; this.chatReady = false; this.detailRoute = ''; this.detailModel.hideConversation(); this.model.releaseTerminal(); this.model.section = index; this.stack = this.paths.get(this.model.scope() + ':' + index) || new NavPathStack(); this.query = ''; this.filter = 0;
    if (index === 1 && this.wide() && !this.stack.size() && this.model.sessions.length) this.openRow(this.model.sessions[0]);
    if (index === 2) this.model.listResources('inbox'); this.savePath();
  }
  private routeTarget(row: ResourceRow, root: boolean): ResourceRow {
    const copy = NavigationContext.rowFrom(JSON.stringify(row));
    if (copy.kind === 'session' || copy.kind === 'conversation') { const data = copy.data(); data['navigation_root'] = root; copy.raw = JSON.stringify(data); }
    return copy;
  }
  private openRow(row: ResourceRow, root: boolean = true): void {
    const name = NavigationContext.destination(row);
    if (row.kind === 'session') { this.relatedAnchors.clear(); this.stack.clear(); this.tool = 0; }
    this.stack.pushPath({ name: name, param: JSON.stringify(this.routeTarget(row, root || !this.stack.size())) }); this.savePath();
  }
  private async openLinkedResource(row: ResourceRow): Promise<void> {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), source = this.activeChild;
    try {
      if (!['inbox', 'todo', 'knowledge', 'file', 'todo_source', 'todo_thread'].includes(row.kind)) await this.model.viewFor(row, row.kind === 'session');
      if (!this.model.owns(generation, scope) || this.activeChild !== source) return;
      const target = this.routeTarget(row, false), name = NavigationContext.destination(target), data = target.data();
      const handoff = target.kind === 'inbox' && Wire.bool(data, 'open_handoff');
      if (Wire.has(data, 'open_handoff')) data['open_handoff'] = false; target.raw = JSON.stringify(data);
      const param = JSON.stringify(target);
      if (handoff) this.pendingInboxHandoff = this.childKey(name, param);
      this.stack.pushPath({ name: name, param: param }); this.savePath();
    } catch (error) { if (this.model.owns(generation, scope) && this.activeChild === source) this.model.reportError(error as Error); }
  }
  private async openMachineChat(param: Object, agent: ResourceRow): Promise<void> {
    const source = this.childKey('settings', param), machine = NavigationContext.from('settings', param as string).row;
    const stack = this.stack, top = stack.size() - 1;
    if (this.activeChild !== source || top < 0 || stack.getParamByIndex(top) !== param ||
      agent.kind !== 'session' || agent.machine !== machine.id || (agent.group && machine.group && agent.group !== machine.group) ||
      (agent.manager && machine.manager && agent.manager !== machine.manager)) return;
    const root = this.model, generation = root.connectionGeneration(), scope = root.scope(), intent = ++this.navigationIntent;
    const current = (): boolean => this.activeChild === source && this.stack === stack && stack.size() === top + 1 &&
      stack.getParamByIndex(top) === param && this.navigationIntent === intent && root.owns(generation, scope);
    try {
      const view = await root.viewFor(machine);
      const viewGeneration = view.connectionGeneration(), viewScope = view.scope(), pairing = view.pairingRevision();
      const admitted = (): boolean => current() && view.owns(viewGeneration, viewScope) && view.pairingRevision() === pairing;
      if (!admitted() || (agent.group && agent.group !== view.profile.id) || (agent.manager && agent.manager !== view.profile.manager)) return;
      const session = view.sessions.find((row: ResourceRow) => row.kind === 'session' && row.machine === machine.id &&
        row.id === agent.id && (row.session || row.id) === (agent.session || agent.id) &&
        (!row.group || row.group === view.profile.id) && (!row.manager || row.manager === view.profile.manager));
      if (!session) { this.getUIContext().getPromptAction().showToast({ message: tr('request_updated') }); return; }
      const target = this.routeTarget(session, false);
      target.group = view.profile.id; target.manager = view.profile.manager;
      if (view.selected.key() !== target.key() || view.draftLoading) await view.select(target, false, admitted);
      if (!admitted() || view.selected.key() !== target.key() || view.draftLoading) return;
      const next = JSON.stringify(target);
      if (this.detailModel !== view) this.detailModel.hideConversation();
      this.detailModel = view;
      this.markDetailReady(this.childKey('session', next));
      stack.pushPath({ name: 'session', param: next }); this.savePath();
    } catch (error) { if (current()) root.reportError(error as Error); }
  }
  private relatedAnchors = new Map<string,RelatedReadingPosition>();
  private takeRelatedAnchor(name: string, param: Object): RelatedReadingPosition | undefined {
    const key = this.childKey(name,param), anchor = this.relatedAnchors.get(key);
    this.relatedAnchors.delete(key); return anchor;
  }
  private openRelated(name: string, param: Object, row: ResourceRow, anchor?: RelatedReadingPosition): void {
    const key = this.childKey(name,param);
    if (this.activeChild !== key || !this.targetReady(name,param)) return;
    const current = this.detailModel.selected;
    if (row.machine !== current.machine || row.manager !== this.detailModel.profile.manager || row.group !== this.detailModel.profile.id) return;
    const routes = this.stack.getAllPathName();
    for (let i=routes.length-2;i>=0;i--) {
      if (routes[i] === 'session' && this.routeRow(this.stack.getParamByIndex(i) as string).key() === row.key()) {
        this.stack.popToIndex(i); this.savePath(); return;
      }
    }
    if (anchor) this.relatedAnchors.set(key,anchor);
    this.openLinkedResource(row);
  }
  private openFrom(name: string, param: Object, row: ResourceRow): void {
    if (this.activeChild === this.childKey(name, param)) { if (!row.group) row.group = NavigationContext.from(name, param as string).row.group || this.model.profile.id; this.openLinkedResource(row); }
  }
  private createAgentFrom(name: string, param: Object, row: ResourceRow): void {
    if (this.activeChild === this.childKey(name, param)) this.prepareAgent(row);
  }
  private openComputer(row: ResourceRow): void {
    if (row.kind !== 'machine' || !row.id) return;
    const param = JSON.stringify(row), names = this.stack.getAllPathName(), top = names.length - 1;
    if (top >= 0 && names[top] === 'computer' && this.stack.getParamByIndex(top) === param) return;
    this.stack.pushPath({ name: 'computer', param: param }); this.savePath();
  }
  private selectedComputer(): ResourceRow | undefined {
    return this.detailModel.machines.find((row: ResourceRow) => row.id === this.detailModel.selected.machine);
  }
  private async handoffResource(row: ResourceRow): Promise<void> {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), source = this.activeChild;
    try {
      const target = await this.model.handoff(row);
      if (this.model.owns(generation, scope) && this.activeChild === source) await this.openLinkedResource(target);
    } catch (error) { if (this.model.owns(generation, scope) && this.activeChild === source) this.model.reportError(error as Error); }
  }
  private childKey(name: string, param: Object): string { return name + ':' + (param as string); }
  private pushChat(row: ResourceRow): void {
    const names = this.stack.getAllPathName();
    const index = names.findIndex((name: string, position: number) => name === 'conversation' && this.routeRow(this.stack.getParamByIndex(position) as string).key() === row.key());
    if (index >= 0) this.stack.popToIndex(index);
    else this.stack.pushPath({ name: 'conversation', param: JSON.stringify(this.routeTarget(row, !this.stack.size())) });
    this.savePath();
  }
  private async openMain(): Promise<void> {
    this.navigationIntent++; if (this.model.section !== 0) this.changeSection(0);
    const owner = this.model, scope = owner.scope(), generation = owner.connectionGeneration(), intent = this.navigationIntent, revision = this.routeRevision, stack = this.stack;
    const current = (): boolean => this.model === owner && owner.owns(generation, scope) && owner.section === 0 && intent === this.navigationIntent && revision === this.routeRevision && stack === this.stack;
    while (this.defaultMainPending && current()) await new Promise<void>((resolve) => this.mainReadWaiters.push(resolve));
    if (current()) await this.resolveMainDestination();
  }
  private newConversation(): void {
    this.navigationIntent++;
    const scope = this.model.scope(), generation = this.model.connectionGeneration(), ownerKey = JSON.stringify([scope, generation]);
    this.creatingConversations.set(ownerKey, (this.creatingConversations.get(ownerKey) || 0) + 1);
    this.model.newConversation().then((row: ResourceRow) => { if (this.model.owns(generation, scope)) this.pushChat(row); })
      .catch((error: Error) => { if (this.model.owns(generation, scope)) this.model.reportError(error); })
      .finally(() => { const remaining = (this.creatingConversations.get(ownerKey) || 1) - 1; if (remaining) this.creatingConversations.set(ownerKey, remaining); else this.creatingConversations.delete(ownerKey); });
  }
  private openResources(kind: string): void { this.resourceKind = kind; this.model.listResources(kind); this.stack.pushPath({ name: 'resources', param: kind }); this.savePath(); }
  private openSettings(page: string, contextual: boolean = false): void { if (page === 'appearance') this.ensureAgentWorkspace(); this.settingsPage = page; this.stack.pushPath({ name: 'settings', param: NavigationContext.settings(page, contextual ? this.detailModel.selected : undefined) }); this.savePath(); }
  private settingsFamily(page: string): string { if (page === 'machine_detail') return 'machines'; return ['extension_detail', 'extension_catalog'].includes(page) ? 'extensions' : page === 'connector_detail' ? 'connectors' : page === 'channel_detail' ? 'channels' : page; }
  private isAdminPage(page: string): boolean { return ['assistant', 'memory', 'managers', 'launchers', 'engine', 'hub_update', 'node_update'].includes(page); }
  private settingsLabel(page: string): string { return page === 'ssh' ? 'ssh_settings' : page === 'assistant' ? 'admin_identity' : page === 'memory' ? 'admin_memory' : page === 'managers' ? 'admin_managers' : page === 'launchers' ? 'admin_launchers' : page === 'engine' ? 'admin_machine_engine' : page === 'hub_update' ? 'admin_hub_update' : page === 'node_update' ? 'admin_node_update' : page; }
  private adminMenus(name: string, param: Object): NavigationMenuItem[] { return [{ value: tr('refresh'), symbolIcon: new SymbolGlyphModifier($r('sys.symbol.arrow_clockwise')), action: () => { if (this.activeChild === this.childKey(name, param)) this.adminRefreshRevision++; } }]; }
  private settingsTitle(param: Object): string { const descriptor = NavigationContext.from('settings', param as string); return descriptor.scoped && ['extension_detail', 'connector_detail', 'channel_detail', 'extension_catalog'].includes(descriptor.page) ? descriptor.row.title : tr(this.settingsLabel(descriptor.page)); }
  private openSettingsDetail(page: string, row: ResourceRow): void { this.stack.pushPath({ name: 'settings', param: NavigationContext.settings(page, row) }); this.savePath(); }
  private openApplications(): void {
    const param = JSON.stringify([this.model.profile.id, this.model.profile.manager]);
    const found = this.stack.getAllPathName().findIndex((name: string): boolean => name === 'applications');
    if (found >= 0) this.stack.popToIndex(found);
    else this.stack.pushPath({ name: 'applications', param: param });
    this.savePath(); this.applications.refresh();
  }
  private openApplication(entry: ExtensionNavigationEntry): void {
    if (!this.applications.accepts(entry)) return;
    const found = this.stack.getAllPathName().findIndex((name: string): boolean => name === 'applications');
    if (found < 0) return;
    this.stack.popToIndex(found);
    this.openExtension(this.model, entry.extensionId, entry.uiId, entry.title, '', {}, entry.extensionId, entry.uiId);
  }
  private applicationAllowed(param: Object): boolean {
    const row = this.routeRow(param), data = row.data(), root = Wire.text(data, 'navigation_extension');
    return !root || this.applications.allows(row.group, row.manager, root, Wire.text(data, 'navigation_ui'));
  }
  private openExtension(owner: ClientModel, extensionId: string, uiId: string, title: string, state: string = '', context: Record<string, Object> = Wire.payload([], []), navigationExtension: string = '', navigationUI: string = ''): void {
    if (!extensionId || !uiId || !owner.profile.id) return;
    if (this.stack.getAllPathName().filter((name: string) => name === 'extension').length >= 8) { this.getUIContext().getPromptAction().showToast({ message: tr('extension_ui_stack_limit') }); return; }
    const row = new ResourceRow(extensionId, title || extensionId, 'extension'); row.group = owner.profile.id; row.manager = owner.profile.manager;
    row.raw = JSON.stringify(Wire.payload(['ui', 'state', 'context', 'instance', 'navigation_extension', 'navigation_ui'], [uiId, state, context, util.generateRandomUUID(), navigationExtension, navigationUI]));
    this.stack.pushPath({ name: 'extension', param: JSON.stringify(row) }); this.savePath();
  }
  private extensionNavigate(name: string, param: Object, effect: ExtensionUINavigationEffect): void {
    if (this.activeChild !== this.childKey(name, param) || !this.applicationAllowed(param) || effect.kind !== 'push') return;
    const source = this.routeRow(param).data();
    this.openExtension(this.detailModel, effect.extensionId, effect.uiId, effect.title, effect.state, effect.context, Wire.text(source, 'navigation_extension'), Wire.text(source, 'navigation_ui'));
  }
  private extensionMenus(name: string, param: Object): NavigationMenuItem[] { return [{ value: tr('refresh'), symbolIcon: new SymbolGlyphModifier($r('sys.symbol.arrow_clockwise')), action: () => { if (this.activeChild === this.childKey(name, param)) this.extensionRefreshRevision++; } }]; }
  private settingsKind(param: Object): string { return NavigationContext.from('settings', param as string).page; }
  private targetReady(name: string, param: Object): boolean {
    if (!this.chatReady || this.detailRoute !== this.childKey(name, param)) return false;
    if (name !== 'session' && name !== 'conversation') return true;
    return this.detailOwner === this.detailModel &&
      this.detailSelection === JSON.stringify([this.detailModel.scope(), this.detailModel.selected.key()]);
  }
  private markDetailReady(key: string): void {
    this.detailOwner = this.detailModel;
    this.detailSelection = JSON.stringify([this.detailModel.scope(), this.detailModel.selected.key()]);
    this.detailRoute = key; this.chatReady = true;
  }
  private settingsModel(param: Object): ClientModel { return NavigationContext.from('settings', param as string).scoped ? this.detailModel : this.model; }
  private pairingCurrent(revision: number): boolean {
    return this.showSheet && this.sheetKind === 'connect' && this.pairingRevision === revision;
  }

  private pairingError(error: Error, redeeming: boolean): string {
    const failure = error as HttpFailure;
    if (redeeming && (failure.status === 410 || (failure.status === 401 && failure.code === 'invalid_token'))) return 'hub_code_expired';
    if (['hub_identity_mismatch', 'hub_identity_changed', 'hub_pin_mismatch', 'pairing_target_changed'].includes(error.message)) return 'hub_identity_error';
    if (['invalid_pairing', 'invalid_endpoint', 'invalid_pin', 'pin_requires_https'].includes(error.message)) return 'hub_invalid_input';
    return 'hub_unreachable';
  }

  private async connect(): Promise<void> {
    if (this.pairBusy || !this.showSheet || this.sheetKind !== 'connect') return;
    const revision = ++this.pairingRevision, owner = this.model, generation = owner.connectionGeneration(), scope = owner.scope();
    this.pairBusy = true; this.pairError = '';
    try {
      const pairing = this.pairMode === 'manual' ? Pairing.manual(this.address.trim(), this.pairCode.trim()) : Pairing.parse(this.address.trim());
      const target = await pairing.inspect();
      if (!this.pairingCurrent(revision)) return;
      if (owner !== this.model || !owner.owns(generation, scope)) { this.pairError = 'request_updated'; return; }
      this.pairGeneration = generation; this.pairScope = scope; this.pairOwner = owner;
      this.pendingPair = pairing; this.pairTarget = target;
    } catch (error) {
      if (this.pairingCurrent(revision)) this.pairError = this.pairingError(error as Error, false);
    } finally { if (this.pairingRevision === revision) this.pairBusy = false; }
  }

  private async trustPairing(): Promise<void> {
    const owner = this.pairOwner, target = this.pairTarget, pairing = this.pendingPair, revision = this.pairingRevision;
    if (!owner || !target || !pairing || this.pairBusy || !this.pairingCurrent(revision)) return;
    const current = (): boolean => this.pairingCurrent(revision) && this.model === owner && this.pairTarget === target &&
      owner.owns(this.pairGeneration, this.pairScope);
    if (!current()) { this.resetPairing(this.pairMode); this.pairError = 'request_updated'; return; }
    this.pairBusy = true;
    try {
      await owner.pair(pairing, target, current);
      if (this.pairingCurrent(revision) && this.model === owner && owner.profile.id === target.hubId) this.showSheet = false;
    } catch (error) {
      if (this.pairingCurrent(revision)) {
        this.pendingPair = undefined; this.pairTarget = undefined;
        this.pairError = this.pairingError(error as Error, true);
        if (this.pairError === 'hub_code_expired') { this.pairCode = ''; if (this.pairMode !== 'manual') this.address = ''; }
      }
    } finally { if (this.pairingRevision === revision) this.pairBusy = false; }
  }
  private menus(): NavigationMenuItem[] {
    if (this.rail()) return [];
    return [
      { value: tr(this.peer() === 1 ? 'agent_new_session' : 'new_chat'), symbolIcon: new SymbolGlyphModifier(Symbols.plus), action: () => this.peer() === 1 ? this.openSheet('newagent') : this.newConversation() },
      { value: tr('conversation_spaces'), symbolIcon: new SymbolGlyphModifier(Symbols.spaces), action: () => this.spacesShown = true }
    ];
  }
  @Builder shellMenu() {
    Row({ space: Tokens.gap }) {
      if (!this.rail()) ForEach(this.menus(), (item: NavigationMenuItem, index: number) => {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(index === 0 ? Symbols.plus : Symbols.spaces).fontSize(Tokens.symbolFont).fontColor([Colors.ink]) }
          .attributeModifier(ButtonStyles.chrome).accessibilityText(item.value as string).onClick(() => item.action?.()).id(index === 0 ? 'shell.new' : 'shell.spaces')
      })
    }.height(this.headerHeight()).alignItems(VerticalAlign.Center).padding({ right: Tokens.page })
  }
  @Builder destinationMenus(name: string, param: Object) {
    if (!this.mainDestination(name, param)) Row({ space: Tokens.gap }) {
      ForEach(this.destinationMenuItems(name, param), (item: NavigationMenuItem, index: number) => {
        Button({ type: ButtonType.Circle }) {
          if (item.symbolIcon) SymbolGlyph().attributeModifier(item.symbolIcon).fontSize(Tokens.symbolFont)
          else Image(item.icon).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink)
        }.attributeModifier(ButtonStyles.chrome).accessibilityText(item.value as string).enabled(item.isEnabled !== false).onClick(() => item.action?.())
          .id(name === 'session' || name === 'conversation' ? 'chat.title.more' : 'destination.action.' + String(index))
          .bindMenu((name === 'session' || name === 'conversation') && this.chatTitleMenuVisible && this.activeChild === this.childKey(name, param),
            this.chatActionsMenu, this.chatMenuOptions(this.chatMenuRevision))
      })
    }.height(this.headerHeight()).alignItems(VerticalAlign.Center).padding({ right: Tokens.page })
  }
  private openSettingsRoot(): void { this.stack.pushPath({ name: 'settings_root', param: '' }); this.savePath(); }
  private openSpace(kind: string): void {
    this.spacesShown = false;
    if (kind === 'persona') { this.selectPeer(0); this.openMain(); }
    else if (kind === 'settings') this.openSettingsRoot();
    else if (kind === 'machines' || kind === 'devices') this.openSettings(kind);
    else if (kind === 'applications') this.openApplications();
    else if (kind === 'hub') this.openSheet('hub');
    else if (kind === 'collapse') this.collapsed = true;
    else this.openResources(kind);
  }
  private spaceSymbol(kind: string): Resource {
    if (kind === 'inbox') return Symbols.inbox;
    if (kind === 'todo') return Symbols.check;
    if (kind === 'knowledge') return Symbols.document;
    if (kind === 'health') return Symbols.health;
    if (kind === 'machines') return Symbols.machine;
    if (kind === 'devices') return Symbols.device;
    if (kind === 'settings') return Symbols.gear;
    if (kind === 'applications') return Symbols.apps;
    if (kind === 'collapse') return Symbols.collapse;
    return Symbols.spaces;
  }
  private spaceColor(kind: string): ResourceColor {
    if (kind === 'inbox' || kind === 'persona') return Colors.link;
    if (kind === 'todo') return Colors.positive;
    if (kind === 'knowledge') return Colors.ai;
    if (kind === 'health') return Colors.teal;
    if (kind === 'machines') return Colors.working;
    return Colors.secondary;
  }
  private spaceTitle(kind: string): string {
    if (kind === 'hub') return tr('hub') + ' · ' + this.model.workspaceName(this.model.profile);
    if (kind === 'persona') return tr('main_chat');
    if (kind === 'todo') return tr('todos');
    if (kind === 'health') return tr('health_home_title');
    return tr(kind);
  }
  @Builder spaceRow(kind: string) {
    ListItem() {
      Row({ space: Tokens.inset }) {
        SymbolGlyph(this.spaceSymbol(kind)).fontSize(Tokens.symbolFont).fontColor([this.spaceColor(kind)]).width(Tokens.symbol)
        Text(this.spaceTitle(kind)).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
      }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.inset }).constraintSize({ minHeight: Tokens.touch }).width('100%')
    }.selectable(true).selected(this.activeModule() === kind).onClick(() => this.openSpace(kind)).id('spaces.' + kind)
  }
  @Builder spacesContent() {
    Column() {
      List() {
        this.spaceRow('persona')
        ForEach(this.conversationRowBindings, (binding: BoundRow<ResourceRow>) => {
          ListItem() { this.conversationRow(binding, true) }.onClick(() => { this.spacesShown = false; if (this.model.section !== 0) this.changeSection(0); this.openRow(binding.value); })
        }, (binding: BoundRow<ResourceRow>) => binding.key)
        this.spaceRow('inbox')
        this.spaceRow('todo')
        this.spaceRow('knowledge')
        this.spaceRow('health')
        if (this.applications.visible) this.spaceRow('applications')
        this.spaceRow('machines')
        this.spaceRow('devices')
        this.spaceRow('settings')
        this.spaceRow('hub')
        if (this.wide() && !this.rail()) this.spaceRow('collapse')
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').padding({ bottom: this.spacesInset + Tokens.page }).id('shell.spaces.content')
      .onAreaChange(() => this.measureSpaces())
  }
  private measureSpaces(): void {
    if (!this.spacesLayoutReady) return;
    const value = nativeSheetInset(this.getUIContext(), 'shell.spaces.content', this.environment.safeBottomPx);
    if (Math.abs(value - this.spacesInset) > 0.1) this.spacesInset = value;
  }
  @Monitor('environment.safeBottomPx') safeBottomChanged(): void { this.measureSpaces(); }
  @Monitor('showSheet', 'spacesShown') modalChanged(): void { if (!this.showSheet && !this.spacesShown) this.ensureMainDestination(); }
  private openHistory(): void { this.stack.pushPath({ name: 'history', param: JSON.stringify(this.detailModel.selected) }); this.savePath(); }
  private openTools(path: string = ''): void { const data = Wire.record(JSON.parse(JSON.stringify(this.detailModel.selected)) as Object); if (path) data['open_file'] = path; this.stack.pushPath({ name: 'tools', param: JSON.stringify(data) }); this.savePath(); }
  private destinationMenuItems(name: string, param: Object): NavigationMenuItem[] {
    if (name === 'settings' && this.isAdminPage(this.settingsKind(param))) return this.adminMenus(name, param);
    if (name === 'extension' && this.applicationAllowed(param)) return this.extensionMenus(name, param);
    if (this.mainDestination(name, param)) return [];
    if (name === 'session' || name === 'conversation') return [{ value: tr('more'), icon: $r('sys.media.ohos_ic_public_more'), isEnabled: this.targetReady(name, param), action: () => this.showChatActions(name, param) }];
    return [];
  }
  private chatMenuOptions(revision: number): MenuOptions {
    return { onDisappear: () => this.closeChatActions(revision) };
  }
  @Builder chatActionsMenu() {
    Menu() {
      ForEach(this.chatMenuActions, (item: SheetInfo) => {
        MenuItem({ content: item.title }).constraintSize({ minHeight: Tokens.touch }).onClick(() => item.action())
      }, (_item: SheetInfo, index: number) => String(this.chatMenuRevision) + ':' + String(index))
    }
  }
  private closeChatActions(revision: number): void {
    if (revision !== this.chatMenuRevision) return;
    this.chatTitleMenuVisible = false; this.chatBodyMenuVisible = false;
  }
  private dismissChatActions(): void {
    this.chatMenuRevision++; this.chatTitleMenuVisible = false; this.chatBodyMenuVisible = false; this.chatMenuActions = [];
  }
  private showChatActions(name: string, param: Object): void {
    const owner = this.detailModel, key = owner.selected.key(), generation = owner.connectionGeneration(), scope = owner.scope();
    const valid = (): boolean => this.activeChild === this.childKey(name, param) && this.targetReady(name, param) &&
      this.detailModel === owner && owner.owns(generation, scope) && owner.selected.key() === key;
    if (!valid()) { this.dismissChatActions(); return; }
    const sheets: SheetInfo[] = [];
    if (owner.isChat() && owner.selected.machine) sheets.push({ title: tr('interaction_history'), action: () => { if (valid()) this.openHistory(); } });
    const computer = this.selectedComputer();
    if (computer) sheets.push({ title: tr('computer_screen_open'), action: () => { if (valid()) this.openComputer(computer); } });
    if (owner.selected.machine) sheets.push({ title: tr('workspace'), action: () => { if (valid()) this.openTools(); } });
    for (const page of ['permissions', 'voice']) sheets.push({ title: tr(page), action: () => {
      if (valid()) this.openSettings(page === 'permissions' ? 'session_permissions' : page, true);
    } });
    const identity = JSON.stringify([owner.scope(), owner.connectionGeneration(), owner.target()]);
    const data = owner.selected.data();
    if (owner.selected.kind === 'session' && Wire.bool(Wire.object(data, 'capabilities'), 'modes') && Wire.strings(data, 'efforts').length) sheets.push({ title: tr('effort'), action: () => { if (valid()) this.openSettings('session_effort', true); } });
    if (owner.canStop() && this.inlineStopIdentity !== identity) sheets.push({ title: tr('stop'), action: () => { if (valid()) owner.stop(valid); } });
    this.chatMenuRevision++; this.chatMenuActions = sheets;
    const body = this.mainDestination(name, param) || this.hideChatTitle(name, param);
    this.chatTitleMenuVisible = !body; this.chatBodyMenuVisible = body;
  }
  private syncConversationRows(): void {
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager]);
    const rows = this.model.conversations.filter((row: ResourceRow) => row.id !== this.model.mainId || !!this.model.mainMachineId && row.machine !== this.model.mainMachineId);
    this.conversationRowBindings = reconcileBoundRows(this.conversationRowBindings, rows,
      (row: ResourceRow) => row.key(), this.conversationBindingOwner, this.model, this.conversationBindingScope, scope);
    this.conversationBindingOwner = this.model;
    this.conversationBindingScope = scope;
  }
  private conversationDetail(row: ResourceRow): string {
    return row.kind === 'session' ? [this.model.machineName(row.machine), this.model.shortPath(row.path), row.state ? tr(row.state) : ''].filter((value: string) => value.length > 0).join(' · ') : row.detail;
  }
  @Builder conversationRow(binding: BoundRow<ResourceRow>, showDetail: boolean) {
    Row({ space: Tokens.inset }) {
      SymbolGlyph(Symbols.spaces).fontSize(Tokens.symbolFont).fontColor([Colors.secondary]).width(Tokens.symbol)
      Column({ space: Tokens.fine }) {
        Text(binding.value.title || binding.value.id).fontSize(Tokens.body).fontColor(Colors.ink).width('100%').maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (showDetail && this.conversationDetail(binding.value)) Text(this.conversationDetail(binding.value)).fontSize(Tokens.meta).fontColor(Colors.secondary).width('100%').maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.inset }).constraintSize({ minHeight: Tokens.touch }).width('100%')
  }
  @Builder state() {
    if (this.model.busy) LoadingProgress().height(Tokens.touch).width(Tokens.touch)
    if (this.model.error) {
      ListItem() { Text(tr(this.model.error)).fontSize(Tokens.meta).padding(Tokens.page) }
    }
  }
  private retryRoot(): void {
    if (this.navigationRestoredScope !== this.model.scope()) { this.activeScope = ''; this.model.error = ''; this.restoreScope(); }
    else if (this.model.connected) { this.model.error = ''; this.defaultMainAttempt = ''; this.model.refresh().then(() => this.ensureMainDestination()); }
    else this.model.foregrounded();
  }
  @Builder connectionState() {
    Row({ space: Tokens.gap }) { Text(tr(this.model.error)).fontSize(Tokens.meta).layoutWeight(1)
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.retryRoot())
    }.padding(Tokens.inset)
  }
  @Builder personaList() {
    List({ space: Tokens.section }) {
      if (this.model.error) ListItem() { this.connectionState() }
      ListItemGroup() {
      ListItem() { Row({ space: Tokens.inset }) { Image(this.look.avatarUri || $r('app.media.foreground')).width(Tokens.symbol).height(Tokens.symbol).objectFit(ImageFit.Contain); Text(tr('main_chat')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1) }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.inset }).constraintSize({ minHeight: Tokens.header }).width('100%') }.selectable(true).selected(this.chatSelectionShown('conversation') && this.detailModel === this.model && this.detailModel.selected.id === this.model.mainId && this.detailModel.selected.kind === 'conversation' && (!this.model.mainMachineId || this.detailModel.selected.machine === this.model.mainMachineId))
        .onVisibleAreaChange([0, 1], (visible: boolean, ratio: number) => { this.mainRowVisible = visible ? ratio : 0; const row = new ResourceRow(this.model.mainId, '', 'conversation'); row.manager = this.model.profile.manager; row.machine = this.model.mainMachineId; row.session = row.id; if (row.id && row.machine) { this.rowVisibility.set(this.model.scope() + this.model.target(row), visible ? ratio : 0); this.visibilityEpoch++; } })
        .onDisAppear(() => this.mainRowVisible = 0).onClick(() => this.openMain()).id('persona.main')
      ForEach(this.conversationRowBindings, (binding: BoundRow<ResourceRow>) => {
        ListItem() {
          this.conversationRow(binding, false)
        }.selectable(true).selected(this.chatSelectionShown('conversation') && this.detailModel === this.model && this.model.target() === this.model.target(binding.value))
          .onVisibleAreaChange([0, 1], (visible: boolean, ratio: number) => { this.rowVisibility.set(this.model.scope() + this.model.target(binding.value), visible ? ratio : 0); if (this.model.target() === this.model.target(binding.value)) this.visibilityEpoch++; })
          .onDisAppear(() => { this.rowVisibility.delete(this.model.scope() + this.model.target(binding.value)); this.visibilityEpoch++; }).onClick(() => this.openRow(binding.value))
      }, (binding: BoundRow<ResourceRow>) => binding.key)
      }.backgroundColor(Colors.canvas)
      ListItemGroup() {
      this.spaceRow('inbox')
      this.spaceRow('todo')
      this.spaceRow('knowledge')
      this.spaceRow('health')
      if (this.applications.visible) this.spaceRow('applications')
      this.spaceRow('machines')
      this.spaceRow('devices')
      this.spaceRow('settings')
      }.backgroundColor(Colors.canvas)
    }.width('100%').height('100%').backgroundColor(Colors.canvas).padding({ bottom: Tokens.listTail })
  }
  private activeModule(): string {
    if (!this.activeChild) return '';
    const names = this.stack.getAllPathName();
    for (let index = names.length - 1; index >= 0; index--) {
      const name = names[index];
      const value: Object | undefined = this.stack.getParamByIndex(index) as Object | undefined;
      const param = typeof value === 'string' ? value : '';
      if (name === 'resources') return param;
      if (['todo', 'knowledge', 'inbox'].includes(name)) return name;
      if (name === 'settings_root') return 'settings';
      if (name === 'settings') {
        const page = this.settingsKind(param);
        return page === 'machines' || page === 'devices' ? page : 'settings';
      }
      if (name === 'applications' || name === 'extension' && !!Wire.text(this.routeRow(param).data(), 'navigation_extension')) return 'applications';
      if (['conversation', 'session', 'assistant', 'tools', 'history'].includes(name)) return 'chat';
    }
    return '';
  }
  private chatSelectionShown(kind: string): boolean {
    return this.activeModule() === 'chat' && this.stack.getAllPathName().includes(kind);
  }
  private readingAgentDetail(): boolean {
    return this.model.section === 1 && this.detailModel.selected.kind === 'session' &&
      (!!this.activeChild || this.stack.getAllPathName().includes('session'));
  }
  @Builder agentsList() {
    AgentListPage({ model: this.model, workspace: this.agentWorkspace, retainWorkspace: true, active: this.model.section === 1, selectedRow: this.detailModel.selected, selectionVisible: this.chatSelectionShown('session'), rail: this.rail(), readingDetail: this.readingAgentDetail(),
      onOpen: (row: ResourceRow) => this.openRow(row), onVisible: (row: ResourceRow, ratio: number) => {
        this.rowVisibility.set(this.model.scope() + row.key(), ratio); if (row.key() === this.detailModel.selected.key()) this.visibilityEpoch++;
      } })
  }
  @Builder inboxList() {
    InboxWorkspacePage({ model: this.model, visible: this.model.section === 2, refreshRevision: this.inboxRefreshRevision, onChanged: () => this.inboxRefreshRevision++, onOpen: (row: ResourceRow) => this.openRow(row), onLink: (address: string) => this.openReader(address),
      onHandoff: (row: ResourceRow) => this.openLinkedResource(row) })
  }
  @Builder settingsList() {
    List() {
      ForEach(['assistant', 'memory', 'machines', 'devices', 'hub', 'managers', 'launchers', 'engine', 'models', 'schedules', 'extensions', 'connectors', 'channels', 'translation', 'triage', 'ledger', 'phone', 'ssh', 'appearance', 'notifications', 'hub_update', 'node_update', 'privacy', 'diagnostics'], (page: string) => {
        ListItem() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: tr(this.settingsLabel(page)) }) }) }.selectable(true).selected(!!this.activeChild && (this.shownName === 'extension' ? page === 'extensions' : this.shownName === 'settings' && this.settingsFamily(this.settingsKind(this.shownParam)) === page)).onClick(() => { if (page === 'hub') this.openSheet('hub'); else this.openSettings(page); }).id('settings.' + page)
      }, (page: string) => page)
    }.width('100%').height('100%').padding({ bottom: Tokens.listTail })
  }
  @Builder primaryContent() {
    if (this.peer() === 1) this.agentsList()
    else this.personaList()
  }
  @Builder workspaceSheet() {
    WorkspaceConnections({ model: this.model, onClose: () => this.showSheet = false,
      onAdd: () => this.openPairing('scan') })
  }
  private async scanPairing(photo: boolean): Promise<void> {
    const host = this.getUIContext().getHostContext();
    if (!host || this.pairBusy || !this.showSheet || this.sheetKind !== 'connect') return;
    const revision = ++this.pairingRevision, owner = this.model, scope = owner.scope(), epoch = owner.pairingRevision();
    const current = (): boolean => this.pairingCurrent(revision) && this.model === owner && owner.scope() === scope && owner.pairingRevision() === epoch;
    this.pairBusy = true; this.pairError = '';
    try {
      let value = '';
      const options: scanBarcode.ScanOptions = { scanTypes: [scanCore.ScanType.QR_CODE], enableAlbum: true, enableMultiMode: false };
      if (photo) {
        const optionsPhoto = new photoAccessHelper.PhotoSelectOptions();
        optionsPhoto.MIMEType = photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE; optionsPhoto.maxSelectNumber = 1;
        const selected = await new photoAccessHelper.PhotoViewPicker().select(optionsPhoto);
        if (!selected.photoUris.length || !current()) return;
        const matches = (await detectBarcode.decode({ uri: selected.photoUris[0] }, options))
          .filter((item: scanBarcode.ScanResult) => isPairingAddress(item.originalValue.trim()));
        if (matches.length === 1) value = matches[0].originalValue;
      } else {
        value = (await scanBarcode.startScanForResult(host, options)).originalValue;
      }
      if (!current()) return;
      try { Pairing.parse(value.trim()); } catch (_) { this.pairError = 'hub_wrong_qr'; return; }
      this.address = value.trim(); this.pairCode = ''; this.pairBusy = false;
      await this.connect();
    } catch (error) {
      if (current() && (error as BusinessError).code !== 1000500002) this.pairError = photo ? 'hub_photo_failed' : 'hub_camera_failed';
    } finally { if (this.pairingRevision === revision) this.pairBusy = false; }
  }

  @Builder pairingForm() {
    HubPairingForm({ mode: this.pairMode, address: this.address, code: this.pairCode, busy: this.pairBusy,
      error: this.pairError, targetName: this.pairTarget?.name || '', targetAddress: this.pairTarget?.baseUrl || '',
      alreadyPaired: !!this.pairTarget && this.model.profiles.some((p: HubProfile) => p.hubId === this.pairTarget?.hubId || p.id === this.pairTarget?.hubId),
      onMode: (mode: string) => this.resetPairing(mode), onAddress: (value: string) => { this.address = value; this.pairError = ''; },
      onCode: (value: string) => { this.pairCode = value; this.pairError = ''; }, onScan: (photo: boolean) => this.scanPairing(photo),
      onVerify: () => this.connect(), onTrust: () => this.trustPairing(), onCancel: () => this.resetPairing(this.pairMode) })
  }
  private createAgentKey(): string {
    if (this.creatingAgent) return 'sending';
    return this.agentModel.operationBlocked('/api/v1/sessions') ? 'result_unknown' : 'create';
  }

  private readyToStart(): boolean {
    return !!this.agentMachine() && !this.creatingAgent && !this.launchersLoading && !this.modelsLoading &&
      (!this.typingStartModel || SessionModelCatalog.validTypedId(this.startModel)) &&
      (!!this.newTask.trim() || this.chosenLauncher()?.promptOptional === true) && !this.agentModel.operationBlocked('/api/v1/sessions');
  }

  @Builder setupLabel(title: string) {
    Text(tr(title)).fontSize(Tokens.meta).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
  }

  @Builder newAgentForm() {
    Scroll() {
      Column({space:Tokens.page}) {
        Column() {
          Row({space:Tokens.gap}) {
            SymbolGlyph(Symbols.machine).fontSize(Tokens.body).fontColor([Colors.secondary])
            this.setupLabel('machine')
            Blank()
            Select(this.agentMachines().map((row: ResourceRow): SelectOption => ({ value: this.agentMachineLabel(row) })))
              .selected(this.agentMachines().findIndex(row => this.isAgentMachine(row)))
              .value(this.agentMachine() ? this.agentMachineLabel(this.agentMachine()!) : tr('agent_no_online_machine')).attributeModifier(SelectStyles.inline)
              .enabled(!this.fixedAgentMachine && !this.creatingAgent).constraintSize({maxWidth:'65%'})
              .onSelect((index: number) => {
                const row = this.agentMachines()[index]; if (!row || this.isAgentMachine(row)) return;
                const owner = this.model.directoryOwnerFor(row); if (!owner) return;
                this.agentModel = owner; this.agentMachineId = row.id; this.directory = ''; this.launcher = ''; this.startModel = ''; this.startEffort = ''; this.saveAgentDraft(); void this.readSessionLaunchers();
              }).id('agent.machine')
          }.width('100%').constraintSize({minHeight:Tokens.touch})
          Divider().color(Colors.divider)
          Row({space:Tokens.gap}) {
            if (agentBrand(this.chosenLauncher()?.agent || '')) Image(agentBrand(this.chosenLauncher()?.agent || '')).width(Tokens.body).height(Tokens.body)
              .objectFit(ImageFit.Contain).fillColor(agentBrandColor(this.chosenLauncher()?.agent || ''))
            else SymbolGlyph(Symbols.agent).fontSize(Tokens.body).fontColor([Colors.teal])
            Text('Agent').fontSize(Tokens.meta).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Blank()
            if (this.launchersFailed) Button(tr('retry')).attributeModifier(ButtonStyles.quiet).onClick(() => { void this.readSessionLaunchers(); })
            else Select([{value:tr('agent_machine_default')}, ...this.sessionLaunchers.map((item:SessionLauncher): SelectOption => ({value:item.label}))])
              .selected(this.sessionLaunchers.findIndex(item => item.name === this.launcher) + 1)
              .value(this.launchersLoading ? tr('loading') : this.chosenLauncher()?.label || tr('agent_machine_default'))
              .attributeModifier(SelectStyles.inline).constraintSize({maxWidth:'65%'}).enabled(!this.creatingAgent && !this.launchersLoading && !!this.agentMachine())
              .onSelect((index:number) => {
                this.launcher = this.sessionLaunchers[index-1]?.name || ''; this.startModel = ''; this.startEffort = ''; this.saveAgentDraft(); void this.readStartModels();
              }).id('agent.launcher')
          }.width('100%').constraintSize({minHeight:Tokens.touch})
          if (this.launchersFailed) {
            Text(tr('agent_launchers_unavailable')).fontSize(Tokens.caption).fontColor(Colors.secondary)
            TextInput({text:this.launcher, placeholder:tr('launcher')}).onChange((v:string) => {this.launcher = v.trim(); this.saveAgentDraft();}).id('agent.launcher.manual')
          }
          if (this.chosenLauncher()) {
            Divider().color(Colors.divider)
            Row({space:Tokens.gap}) {
              this.setupLabel('model')
              Blank()
              Select(this.startModelOptions())
                .selected(this.typingStartModel ? this.startModels.length + 1 : this.startModels.findIndex(item => Wire.text(item,'id') === this.startModel) + 1)
                .value(this.modelsLoading ? tr('loading') : this.modelLocked ? this.modelLockLabel() : this.modelLabel())
                .attributeModifier(SelectStyles.inline).constraintSize({maxWidth:'64%'})
                .enabled(!this.creatingAgent && !this.modelsLoading && !this.modelLocked && (this.startModels.length > 0 || this.canTypeStartModel()))
                .onSelect((index:number) => this.pickStartModel(index)).id('agent.model')
              if (!this.modelLocked || this.modelsFailed) {
                Button({type:ButtonType.Circle}) {
                  SymbolGlyph(Symbols.refresh).fontSize(Tokens.body).fontColor([Colors.secondary])
                }.attributeModifier(ButtonStyles.chrome).enabled(!this.modelsLoading && !this.creatingAgent)
                  .accessibilityText(tr('agent_refresh_models')).onClick(() => {void this.readStartModels(true);}).id('agent.models.refresh')
              }
            }.width('100%').constraintSize({minHeight:Tokens.touch})
            if (this.typingStartModel && this.canTypeStartModel()) {
              TextInput({text:this.startModel,placeholder:tr('model_id')}).enabled(!this.creatingAgent && !this.modelsLoading)
                .backgroundColor(Colors.control).borderRadius(Tokens.inset).margin({bottom:Tokens.gap})
                .onChange((value:string) => {this.startModel = value.trim(); this.saveAgentDraft();}).id('agent.model.custom')
              if (this.startModel && !SessionModelCatalog.validTypedId(this.startModel)) {
                Text(tr('agent_model_id_invalid')).fontSize(Tokens.caption).fontColor(Colors.danger).width('100%').margin({bottom:Tokens.gap})
              }
            }
            if (this.modelsFailed) Text(tr('agent_models_unavailable')).width('100%').fontSize(Tokens.caption).fontColor(Colors.secondary)
              .margin({bottom:Tokens.gap})
            if (!this.modelLocked && this.offeredEfforts().length > 0) {
              Divider().color(Colors.divider)
              Row({space:Tokens.gap}) {
                this.setupLabel('reasoning_effort')
                Blank()
                Select(this.startEffortOptions())
                  .selected(this.offeredEfforts().indexOf(this.startEffort) + 1)
                  .value(this.startEffort ? this.effortName(this.startEffort) : tr('agent_default_effort'))
                  .attributeModifier(SelectStyles.inline).constraintSize({maxWidth:'64%'})
                  .enabled(!this.creatingAgent && !this.modelsLoading)
                  .onSelect((index:number) => {this.startEffort = this.offeredEfforts()[index - 1] || ''; this.saveAgentDraft();}).id('agent.effort')
              }.width('100%').constraintSize({minHeight:Tokens.touch})
            }
          }
        }.padding({left:Tokens.page,right:Tokens.page}).backgroundColor(Colors.planSurface).border({width:Tokens.divider,color:Colors.planBorder}).borderRadius(Tokens.card)
        Column({space:Tokens.gap}) {
          this.setupLabel('directory')
          TextInput({ text:this.directory, placeholder:tr('agent_default_folder') }).enabled(!this.creatingAgent)
            .backgroundColor(Colors.control).borderRadius(Tokens.inset).onChange((value:string) => {this.directory=value;this.saveAgentDraft();}).id('agent.directory')
        }.alignItems(HorizontalAlign.Start).width('100%')
        Column({space:Tokens.gap}) {
          this.setupLabel('agent_first_message')
          TextArea({ text:this.newTask, placeholder:tr(this.chosenLauncher()?.promptOptional ? 'agent_message_optional' : 'message') })
            .enabled(!this.creatingAgent).height(104).padding(Tokens.inset).backgroundColor(Colors.control).borderRadius(Tokens.inset)
            .onChange((value:string) => {this.newTask=value;this.saveAgentDraft();}).id('agent.task')
        }.alignItems(HorizontalAlign.Start).width('100%')
        if (this.agentModel.error) Text(tr(this.agentModel.error)).fontSize(Tokens.meta).fontColor(Colors.danger)
        Button(tr(this.createAgentKey() === 'create' ? 'agent_new_session' : this.createAgentKey())).attributeModifier(ButtonStyles.primary)
          .width('100%').enabled(this.readyToStart()).onClick(() => this.confirmCreateAgent()).id('agent.create')
      }.padding(Tokens.page).width('100%').constraintSize({maxWidth:Tokens.readingMax})
    }.width('100%').align(Alignment.Top)
  }

  private canTypeStartModel(): boolean {
    const launcher = this.chosenLauncher();
    return !!launcher && launcher.agent === 'codex' && launcher.name !== 'codex-app' && !this.modelLocked;
  }
  private modelLockLabel(): string {
    return tr(this.chosenLauncher()?.name === 'codex-app' ? 'agent_codex_desktop_model' : 'agent_locked_model');
  }
  private startModelOptions(): SelectOption[] {
    const options: SelectOption[] = [{value:tr('agent_default_model')}];
    this.startModels.forEach(item => options.push({value:launcherModelLabel(item)}));
    if (this.canTypeStartModel()) options.push({value:tr('agent_custom_model')});
    return options;
  }
  private offeredEfforts(): string[] { return startEfforts(this.startModels, this.startModel, this.typingStartModel); }
  private startEffortOptions(): SelectOption[] {
    const options: SelectOption[] = [{value:tr('agent_default_effort')}];
    this.offeredEfforts().forEach(item => options.push({value:this.effortName(item)}));
    return options;
  }
  private effortName(effort: string): string { const name = tr(effort); return name === effort ? effort : name; }
  /** Keeps a picked effort only while the model in the form lists it, so a start never asks for one the agent lacks. */
  /** The effort a start sends: the pick, while the model in the form lists it; '' runs the agent's own default. */
  private chosenEffort(): string { return !this.modelLocked && this.offeredEfforts().includes(this.startEffort) ? this.startEffort : ''; }
  private keepStartEffort(): void { if (this.startEffort && !this.offeredEfforts().includes(this.startEffort)) this.startEffort = ''; }
  private pickStartModel(index: number): void {
    this.typingStartModel = this.canTypeStartModel() && index === this.startModels.length + 1;
    const model = this.startModels[index - 1];
    this.startModel = model ? Wire.text(model,'id') : '';
    this.keepStartEffort();
    this.saveAgentDraft();
  }
  private modelLabel(): string {
    if (this.typingStartModel) return tr('agent_custom_model');
    const match = this.startModels.find(item => Wire.text(item,'id') === this.startModel);
    return match ? launcherModelLabel(match) : tr('agent_default_model');
  }
  private async readStartModels(refresh: boolean = false): Promise<void> {
    const ticket = ++this.modelRequest, owner = this.agentModel, machine = this.agentMachine(), launcher = this.chosenLauncher();
    if (!refresh) { this.startModels = []; this.modelLocked = false; this.typingStartModel = false; }
    this.modelsFailed = false; this.modelsLoading = !!machine && !!launcher;
    if (!machine || !launcher) return;
    const generation = owner.connectionGeneration(), scope = owner.scope();
    const current = (): boolean => ticket === this.modelRequest && this.showSheet && this.sheetKind === 'newagent' &&
      this.agentModel === owner && owner.owns(generation, scope) && this.agentMachineId === machine.id && this.launcher === launcher.name;
    try {
      const result = await owner.repo().sessionModels(machine.id, launcher.name, refresh || launcher.agent === 'codex');
      if (!current()) return;
      this.modelLocked = result.locked;
      this.startModels = result.models;
      const listed = this.startModels.some(item => Wire.text(item,'id') === this.startModel);
      this.typingStartModel = this.canTypeStartModel() && !listed && (!!this.startModel || this.typingStartModel);
      if (this.modelLocked || !listed && !this.typingStartModel) this.startModel = '';
      this.keepStartEffort();
    } catch (_) {
      if (current()) {
        this.modelsFailed = true;
        if (!this.startModels.length && this.startModel) {
          this.typingStartModel = this.canTypeStartModel();
          if (!this.typingStartModel) this.startModel = '';
        }
      }
    } finally { if (ticket === this.modelRequest) this.modelsLoading = false; }
  }
  private agentDraftKey(): string { return 'new-agent:' + (this.agentTodo?.id || (this.fixedAgentMachine ? 'machine:' + this.fixedAgentMachine : 'standalone')); }
  private async prepareAgent(todo?: ResourceRow, computer?: ResourceRow): Promise<void> {
    const revision = ++this.agentFormRevision, generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.readerShown = false; this.creatingAgent = false; this.fixedAgentMachine = computer?.id || ''; this.agentMachineId = this.fixedAgentMachine; this.launcherRequest++; this.sessionLaunchers = []; this.launchersLoading = false; this.launchersFailed = false; this.newTask = ''; this.directory = ''; this.launcher = ''; this.agentTodo = todo;
    try {
      const origin = computer || todo;
      const owner = origin ? await this.model.viewFor(origin) : this.model;
      if (revision !== this.agentFormRevision || !this.model.owns(generation, scope)) return;
      this.agentModel = owner;
      const raw = await owner.loadView(this.agentDraftKey());
      if (revision !== this.agentFormRevision || !this.model.owns(generation, scope)) return;
      const saved = Wire.parse(raw); this.newTask = Wire.text(saved, 'task', todo ? todo.title + (Wire.text(todo.data(), 'notes') ? '\n\n' + Wire.text(todo.data(), 'notes') : '') : '');
      this.startModel = Wire.text(saved, 'model'); this.startEffort = Wire.text(saved, 'effort'); this.directory = Wire.text(saved, 'directory'); this.launcher = Wire.text(saved, 'launcher');
      const savedMachine = Wire.text(saved, 'machine');
      const available = this.agentMachines();
      const savedScope = Wire.text(saved,'machine_scope',owner.scope());
      const selected = available.find(row => row.id === (this.fixedAgentMachine || savedMachine) &&
        this.model.directoryOwnerFor(row)?.scope() === savedScope) || available[0];
      if (selected) this.agentModel = this.model.directoryOwnerFor(selected) || owner;
      this.agentMachineId = this.fixedAgentMachine || selected?.id || '';
      if (savedMachine && (savedMachine !== this.agentMachineId || this.agentModel.scope() !== savedScope)) {
        this.launcher = ''; this.startModel = ''; this.startEffort = ''; this.directory = '';
      }
      this.sheetKind = 'newagent'; this.showSheet = true; void this.readSessionLaunchers();
    } catch (error) { if (revision === this.agentFormRevision && this.model.owns(generation, scope)) this.model.reportError(error as Error); }
  }
  private saveAgentDraft(): void {
    if (this.sheetKind !== 'newagent' || !this.showSheet) return;
    const owner = this.agentDraftOwner(), generation = owner.connectionGeneration(), scope = owner.scope();
    owner.saveView(this.agentDraftKey(), JSON.stringify(Wire.payload(['task','directory','launcher','machine','model','effort','machine_scope'],
      [this.newTask,this.directory,this.launcher,this.agentMachineId,this.startModel,this.startEffort,this.agentModel.scope()])))
      .catch(() => { if (owner.owns(generation, scope)) owner.error = 'cache_unavailable'; });
  }
  private agentDraftOwner(): ClientModel { return this.agentTodo || this.fixedAgentMachine ? this.agentModel : this.model; }
  private agentMachines(): ResourceRow[] {
    const roster = this.agentTodo || this.fixedAgentMachine || !this.model.directoryReady ? this.agentModel.machines : this.model.directoryMachines;
    return roster.filter(row => {
      const data = row.data();
      return !!this.model.directoryOwnerFor(row)?.connected && (!this.fixedAgentMachine || row.id === this.fixedAgentMachine) &&
        Wire.text(data,'trust') !== 'pending' && !Wire.bool(data,'pending_confirm') && !Wire.bool(data,'pending') && Wire.bool(data,'online');
    });
  }
  private isAgentMachine(row: ResourceRow): boolean { return row.id === this.agentMachineId && this.model.directoryOwnerFor(row) === this.agentModel; }
  private agentMachine(): ResourceRow | undefined { return this.agentMachines().find(row => this.isAgentMachine(row)); }
  private agentMachineLabel(row: ResourceRow): string {
    const label = row.title || row.id;
    return this.agentMachines().some(item => item.key() !== row.key() && (item.title || item.id) === label) ?
      label + ' · ' + this.model.groupName(row) + ' · ' + row.manager : label;
  }
  private chosenLauncher(): SessionLauncher | undefined { return this.sessionLaunchers.find(item => item.name === this.launcher); }
  private async readSessionLaunchers(): Promise<void> {
    const ticket = ++this.launcherRequest, owner = this.agentModel, machine = this.agentMachine();
    this.modelRequest++; this.modelsLoading = false; this.typingStartModel = false; this.modelLocked = false; this.startModels = []; this.sessionLaunchers = []; this.launchersFailed = false; this.launchersLoading = !!machine;
    if (!machine) return;
    const generation = owner.connectionGeneration(), scope = owner.scope();
    const current = (): boolean => this.showSheet && this.sheetKind === 'newagent' && ticket === this.launcherRequest &&
      this.agentModel === owner && owner.owns(generation, scope) && this.agentMachineId === machine.id;
    try {
      const choices = await owner.repo().sessionLaunchers(machine.id);
      if (!current()) return;
      this.sessionLaunchers = choices;
      if (!this.chosenLauncher()) { this.launcher = ''; this.startModel = ''; this.startEffort = ''; }
      void this.readStartModels();
    } catch (_) { if (current()) { this.launchersFailed = true; this.startModel = ''; } }
    finally { if (ticket === this.launcherRequest) this.launchersLoading = false; }
  }
  private agentFingerprint(): string { return JSON.stringify([this.agentModel.scope(), this.agentTodo?.id || '', this.agentMachine()?.id || '', this.newTask, this.directory, this.launcher, this.startModel, this.chosenEffort()]); }
  private confirmCreateAgent(): void {
    const machine = this.agentMachine(); if (!machine || !this.readyToStart()) return;
    const revision = this.agentFormRevision, fingerprint = this.agentFingerprint(), owner = this.agentModel, generation = owner.connectionGeneration(), scope = owner.scope();
    this.getUIContext().showAlertDialog({ title: tr('agent_new_session'), message: owner.profile.name + ' · ' + owner.profile.manager + '\n' + (machine.title || machine.id) + ' · ' + (this.chosenLauncher()?.label || this.launcher || tr('agent_machine_default')) + (this.startModel ? ' · ' + (this.typingStartModel ? this.startModel : this.modelLabel()) : '') + (this.chosenEffort() ? ' · ' + this.effortName(this.chosenEffort()) : '') + (this.directory ? '\n' + this.directory : '') + (this.newTask ? '\n\n' + this.newTask : ''),
      primaryButton: { value: tr('cancel'), action: () => {} }, secondaryButton: { value: tr('create'), action: () => {
        if (revision === this.agentFormRevision && owner.owns(generation, scope) && fingerprint === this.agentFingerprint()) this.createAgent();
      } } });
  }
  private async createAgent(): Promise<void> {
    const machine = this.agentMachine(); if (!machine || !this.readyToStart()) return;
    const owner = this.agentModel, generation = owner.connectionGeneration(), scope = owner.scope(), rootGeneration = this.model.connectionGeneration(), rootScope = this.model.scope(), revision = this.agentFormRevision, fingerprint = this.agentFingerprint(), draftKey = this.agentDraftKey();
    const valid = (): boolean => owner.owns(generation, scope) && this.model.owns(rootGeneration, rootScope) && revision === this.agentFormRevision && this.showSheet && this.sheetKind === 'newagent' && fingerprint === this.agentFingerprint();
    this.creatingAgent = true;
    try {
      const row = await owner.createAgent(this.newTask, machine, this.directory, this.launcher, this.agentTodo?.id || '', valid, this.chosenLauncher()?.agent || '', this.startModel, this.chosenEffort());
      if (!valid()) return;
      this.showSheet = false; this.newTask = ''; this.agentDraftOwner().saveView(draftKey, '').catch(() => {}); this.openRow(row);
    } catch (error) { if (valid()) owner.reportError(error as Error); }
    finally { if (revision === this.agentFormRevision) this.creatingAgent = false; }
  }
  private routeRow(param: Object): ResourceRow { return NavigationContext.rowFrom(param as string); }
  private chatTitle(param: Object): string {
    const row = this.routeRow(param);
    const current = this.detailModel.sessions.concat(this.detailModel.conversations).find((item: ResourceRow) => item.key() === row.key());
    return current?.title || row.title || tr(row.kind === 'conversation' ? 'main_chat' : 'agents');
  }
  private async restoreTarget(name: string, param: Object): Promise<void> {
    const descriptor = NavigationContext.from(name, param as string);
    if (!descriptor.scoped) return;
    const key = this.childKey(name, param), revision = ++this.routeRevision, generation = this.model.connectionGeneration(), scope = this.model.scope();
    if (name === 'settings' && descriptor.page === 'machine_detail') return;
    const chat = name === 'session' || name === 'conversation';
    const current = (): boolean => revision === this.routeRevision && this.activeChild === key && this.model.owns(generation, scope);
    const keepContent = this.targetReady(name, param);
    if (!keepContent) { this.chatReady = false; this.detailRoute = ''; }
    try {
      let row = descriptor.row; const view = await this.model.viewFor(row, name === 'session');
      if (['session', 'conversation'].includes(row.kind) && !row.machine) {
        let resolved = view.conversations.concat(view.sessions).find((item: ResourceRow) => item.id === row.id || item.session === row.session && !!row.session);
        if (!resolved && view.connected) { await view.refresh(); resolved = view.conversations.concat(view.sessions).find((item: ResourceRow) => item.id === row.id || item.session === row.session && !!row.session); }
        if (!resolved) throw new Error('request_updated'); row = resolved;
      }
      if (!current()) return;
      if (this.detailModel !== view) this.detailModel.hideConversation();
      this.detailModel = view;
      if (row.key() !== view.selected.key() || (chat && view.draftLoading)) await view.select(row, !chat, current);
      if (!current() || view.selected.key() !== row.key() || (chat && view.draftLoading)) return;
      this.markDetailReady(key);
      if (chat) view.showConversation(row);
      if (name === 'assistant' || name === 'settings' && descriptor.page === 'voice') view.watchDecisions(true);
    } catch (error) { if (revision === this.routeRevision && this.activeChild === key && this.model.owns(generation, scope)) this.model.reportError(error as Error); }
  }
  private routeShown(name: string, param: Object): void {
    if (this.activeChild !== this.childKey(name, param)) this.dismissChatActions();
    this.activeChild = this.childKey(name, param); if (name === 'extension') this.extensionTitle = this.routeRow(param).title; if (name === 'todo') this.todoTitle = this.routeRow(param).title; this.shownName = name; this.shownParam = param as string;
    this.detailModel.watchDecisions(false);
    if (!['session', 'conversation'].includes(name)) this.detailModel.hideConversation(name !== 'tools');
    if (name === 'applications') this.applications.refresh();
    if (name === 'settings' && this.settingsKind(param) === 'appearance') this.ensureAgentWorkspace();
    const descriptor = NavigationContext.from(name, param as string);
    if (descriptor.scoped) this.restoreTarget(name, param);
    else { this.routeRevision++; this.markDetailReady(this.activeChild); if (name === 'resources') this.model.listResources(param as string); }
  }
  @Builder targetLoading(name: string, param: Object) {
    Column({ space: Tokens.gap }) {
      if (this.model.error) {
        Text(tr(this.model.error)).fontSize(Tokens.meta)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.restoreTarget(name, param))
      } else LoadingProgress().width(Tokens.touch).height(Tokens.touch)
    }.width('100%').height('100%').justifyContent(FlexAlign.Center)
  }
  @Builder destinationTitle(name: string, param: Object) {
    if (this.mainDestination(name, param) && !this.wide()) this.peerTitle(true)
    else Text(this.destinationTitleText(name, param)).height(this.headerHeight()).fontSize(Tokens.title).fontWeight(FontWeight.Medium).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
      .padding({ left: this.destinationBackHidden(name, param) ? Tokens.page : 0 })
      .margin({ start: LengthMetrics.vp(this.deviceTitleGap(name, param)) })
  }
  private deviceTitleGap(name: string, param: Object): number {
    if (name !== 'settings' || this.destinationBackHidden(name, param)) return 0;
    const page = this.settingsKind(param);
    return page === 'devices' || page === 'machines' || page === 'machine_detail' ? Tokens.inset : 0;
  }
  private immersiveComputer(name: string, param: Object): boolean {
    return name === 'computer' && this.fullscreenComputer === this.childKey(name, param);
  }

  private screenOrientation(name: string, param: Object): window.Orientation | undefined {
    if (name !== 'computer') return undefined;
    return this.immersiveComputer(name, param) ? this.computerOrientation : this.computerReturnOrientation;
  }

  private finishComputerFullscreen(): void {
    if (this.computerExitTimer !== -1) clearTimeout(this.computerExitTimer);
    this.computerExitTimer = -1;
    this.fullscreenComputer = '';
    this.environment.setComputerImmersive(false);
  }

  @Monitor('environment.windowWidthPx')
  computerWindowChanged(): void {
    if (this.computerExitTimer !== -1 && this.environment.viewingOrientation() === this.computerReturnOrientation) this.finishComputerFullscreen();
  }

  private exitComputerFullscreen(): void {
    if (this.computerExitTimer !== -1 || this.computerReturnOrientation === undefined ||
      this.environment.viewingOrientation() === this.computerReturnOrientation) { this.finishComputerFullscreen(); return; }
    // Rotate back while Navigation still owns the whole window. A split destination cannot request orientation.
    this.computerOrientation = this.computerReturnOrientation;
    this.computerExitTimer = setTimeout((): void => this.finishComputerFullscreen(), 1000);
  }

  private destinationBackHidden(name: string, param: Object): boolean {
    if (this.mainDestination(name, param) || name === 'session' && Wire.bool(this.routeRow(param).data(), 'navigation_root') && this.wide() && this.model.section === 1) return true;
    return this.wide() && this.stack.size() === 1 && this.stack.getAllPathName()[0] === name && this.stack.getParamByIndex(0) === param;
  }
  private destinationTitleText(name: string, param: Object): string {
    if (name === 'computer') return tr('computer_screen_open');
    if (name === 'settings_root') return tr('settings');
    if (name === 'assistant') return tr(Wire.text(this.routeRow(param).data(), 'assistant_item_kind') === 'plan' ? 'assistant_feed_plan' : 'assistant_feed_receipt');
    return name === 'session' || name === 'conversation' ? this.chatTitle(param) : name === 'resources' ? tr(param === 'todo' ? 'todos' : param === 'inbox' ? 'inbox' : param === 'health' ? 'health_home_title' : 'knowledge') : name === 'applications' ? tr('applications') : name === 'extension' ? this.extensionTitle : name === 'todo' ? this.todoTitle || this.routeRow(param).title || this.routeRow(param).id : name === 'settings' ? this.settingsTitle(param) : name === 'tools' ? tr('workspace') : name === 'history' ? tr('interaction_history') : this.routeRow(param).title || this.routeRow(param).id;
  }
  @Builder destination(name: string, param: Object) {
    NavDestination() {
      if (name === 'session' || name === 'conversation') {
        if (this.targetReady(name, param)) Conversation({ model: this.detailModel, onOpenComputer: (row: ResourceRow) => {
          if (this.activeChild === this.childKey(name, param) && this.targetReady(name, param) && this.selectedComputer()?.key() === row.key()) this.openComputer(row);
        }, onRelated: (row: ResourceRow, anchor?: RelatedReadingPosition) => this.openRelated(name,param,row,anchor),
          takeReturnAnchor: () => this.takeRelatedAnchor(name,param), admission: (): boolean => this.activeChild === this.childKey(name, param) && this.targetReady(name, param), onOpenFile: (path: string) => this.openTools(path), active: this.activeChild === this.childKey(name, param), onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address), showContextMenu: this.mainDestination(name, param) || this.hideChatTitle(name, param), onOpenCard: (itemId: string, kind: string) => { if (this.activeChild === this.childKey(name, param) && this.targetReady(name, param)) this.openAssistantCard(itemId, kind); }, onPrimaryStop: (shown: boolean) => { if (this.activeChild === this.childKey(name, param) && this.targetReady(name, param)) this.inlineStopIdentity = shown ? JSON.stringify([this.detailModel.scope(), this.detailModel.connectionGeneration(), this.detailModel.target()]) : ''; }, onShowActions: () => this.showChatActions(name, param), actionItems: this.chatMenuActions, actionsVisible: this.chatBodyMenuVisible && this.activeChild === this.childKey(name, param), actionsRequest: this.chatMenuRevision, onHideActions: (request: number) => this.closeChatActions(request) })
        else this.targetLoading(name, param)
      } else if (name === 'history') {
        if (this.targetReady(name, param)) InteractionHistoryPage({ model: this.detailModel, active: this.activeChild === this.childKey(name, param), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'tools') {
        if (this.targetReady(name, param)) SessionTools({ model: this.detailModel, onOpenLink: (address: string) => this.openReader(address), initialFile: Wire.text(Wire.parse(param as string), 'open_file'), onWorkspaceReady: (workspace: SessionWorkspace | undefined) => this.detailModel.setWorkspace(workspace) })
        else this.targetLoading(name, param)
      } else if (name === 'applications') {
        ExtensionNavigationPage({ directory: this.applications, onOpen: (entry: ExtensionNavigationEntry): void => { if (this.activeChild === this.childKey(name, param)) this.openApplication(entry); } })
      } else if (name === 'extension') {
        Column() {
          if (!this.applicationAllowed(param)) Row({ space: Tokens.gap }) {
            Text(tr('application_unavailable')).fontSize(Tokens.meta).layoutWeight(1)
            Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).enabled(!this.applications.loading).onClick(() => this.applications.refresh())
          }.width('100%').padding(Tokens.page)
          Column() {
        if (this.targetReady(name, param)) ExtensionPage({ model: this.detailModel, extensionId: this.routeRow(param).id, uiId: Wire.text(this.routeRow(param).data(), 'ui'), state: Wire.text(this.routeRow(param).data(), 'state'), context: Wire.object(this.routeRow(param).data(), 'context'), active: this.activeChild === this.childKey(name, param), authorized: this.applicationAllowed(param), refreshRevision: this.extensionRefreshRevision + (Wire.text(this.routeRow(param).data(), 'navigation_extension') ? this.applications.revision : 0), onTitle: (title: string) => { if (this.activeChild === this.childKey(name, param)) this.extensionTitle = title; }, onNavigate: (effect: ExtensionUINavigationEffect) => this.extensionNavigate(name, param, effect), onLink: (address: string) => { if (this.activeChild === this.childKey(name, param)) this.openReader(address); }, onBack: () => { if (this.activeChild === this.childKey(name, param)) { this.stack.pop(); this.savePath(); } } })
            else this.targetLoading(name, param)
          }.width('100%').layoutWeight(1).visibility(this.applicationAllowed(param) ? Visibility.Visible : Visibility.None)
        }.width('100%').height('100%')
      } else if (name === 'computer') {
        if (this.targetReady(name, param)) ComputerScreenPage({ model: this.detailModel, row: this.routeRow(param), active: this.activeChild === this.childKey(name, param),
          fullscreen: this.immersiveComputer(name, param), onFullscreen: (shown: boolean) => {
            if (this.activeChild === this.childKey(name, param)) {
              if (shown) {
                this.computerReturnOrientation = this.environment.viewingOrientation();
                this.environment.setComputerImmersive(true);
                this.computerOrientation = window.Orientation.AUTO_ROTATION_RESTRICTED;
                this.fullscreenComputer = this.childKey(name, param);
              } else this.exitComputerFullscreen();
            }
          }, onRotate: (landscape: boolean) => {
            if (this.immersiveComputer(name, param)) this.computerOrientation = landscape ? window.Orientation.LANDSCAPE : window.Orientation.PORTRAIT;
          }, onClose: () => { if (this.activeChild === this.childKey(name, param)) { this.stack.pop(); this.savePath(); } } })
        else this.targetLoading(name, param)
      } else if (name === 'file') {
        if (this.targetReady(name, param)) RemoteDocumentPage({ model: this.detailModel, row: this.routeRow(param), active: this.activeChild === this.childKey(name, param), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'inbox' || name === 'todo_source' || name === 'todo_thread' || name === 'resources' && param === 'inbox') {
        if (name === 'resources' || this.targetReady(name, param)) InboxWorkspacePage({ model: name === 'resources' ? this.model : this.detailModel, row: name === 'resources' ? undefined : this.routeRow(param), list: name === 'resources', visible: this.activeChild === this.childKey(name, param), refreshRevision: this.inboxRefreshRevision, handoffRequested: this.pendingInboxHandoff === this.childKey(name, param), onHandoffConsumed: () => { if (this.pendingInboxHandoff === this.childKey(name, param)) this.pendingInboxHandoff = ''; },
          onChanged: () => { if (this.activeChild === this.childKey(name, param)) this.inboxRefreshRevision++; }, onOpen: (row: ResourceRow) => this.openFrom(name, param, row), onHandoff: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'todo' || name === 'resources' && param === 'todo') {
        if (name === 'resources' || this.targetReady(name, param)) TodoWorkspacePage({ model: name === 'resources' ? this.model : this.detailModel, row: name === 'todo' ? this.routeRow(param) : undefined, list: name === 'resources', visible: this.activeChild === this.childKey(name, param),
          onTitle: (title: string) => { if (name === 'todo' && this.activeChild === this.childKey(name, param)) this.todoTitle = title; }, onOpen: (row: ResourceRow) => this.openFrom(name, param, row), onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address), onCreateAgent: (todo: ResourceRow) => this.createAgentFrom(name, param, todo) })
        else this.targetLoading(name, param)
      } else if (name === 'knowledge' || name === 'resources' && param === 'knowledge') {
        if (name === 'resources' || this.targetReady(name, param)) KnowledgeWorkspacePage({ model: name === 'resources' ? this.model : this.detailModel, row: name === 'knowledge' ? this.routeRow(param) : undefined, visible: this.activeChild === this.childKey(name, param),
          onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onChat: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'resources' && param === 'health') {
        HealthWorkspacePage({ model: this.model, onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onChat: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address) })
      } else if (name === 'resources' || name === 'resource') {
        if (name === 'resources' || this.targetReady(name, param)) ResourcePage({ model: name === 'resources' ? this.model : this.detailModel, onLink: (address: string) => this.openReader(address), kind: name === 'resources' ? param as string : this.routeRow(param).kind, list: name === 'resources', onOpen: (row: ResourceRow) => this.openFrom(name, param, row),
          onHandoff: (row: ResourceRow) => this.handoffResource(row) })
        else this.targetLoading(name, param)
      } else if (name === 'settings_root') {
        this.settingsList()
      } else if (name === 'assistant') {
        if (this.targetReady(name, param)) AssistantDetailPage({ model: this.detailModel, active: this.activeChild === this.childKey(name, param), itemId: Wire.text(this.routeRow(param).data(), 'assistant_item_id'), kind: Wire.text(this.routeRow(param).data(), 'assistant_item_kind'), onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'settings' && (this.settingsKind(param) === 'phone' || this.settingsKind(param) === 'voice')) {
        if (!NavigationContext.from(name, param as string).scoped || this.targetReady(name, param)) DevicePage({ model: this.settingsModel(param), page: this.settingsKind(param), active: this.activeChild === this.childKey(name, param), onLeave: (owner: ClientModel) => this.restoreDeviceOwner(owner), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'settings' && this.isAdminPage(this.settingsKind(param))) {
        if (!NavigationContext.from(name, param as string).scoped || this.targetReady(name, param)) AdminWorkspacePage({ model: this.settingsModel(param), page: this.settingsKind(param), row: NavigationContext.from(name, param as string).scoped ? NavigationContext.from(name, param as string).row : undefined, active: this.activeChild === this.childKey(name, param), refreshRevision: this.adminRefreshRevision,
          onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onOpenMachine: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openSettingsDetail('machines', row); } })
        else this.targetLoading(name, param)
      } else if (name === 'settings' && this.settingsKind(param) === 'devices') {
        SignedInDevicesPage({ model: this.model, active: this.activeChild === this.childKey(name, param) })
      } else if (name === 'settings' && this.settingsKind(param) === 'ssh') {
        SshSettingsPage({ tunnel: this.settingsModel(param).ssh, active: this.activeChild === this.childKey(name, param),
          onSaved: (): void => { const owner = this.settingsModel(param); if (this.activeChild === this.childKey(name, param)) owner.selectProfile(owner.profile, true); } })
      } else if (name === 'settings' && this.settingsKind(param) === 'machine_detail') {
        SettingsPage({ model: this.model, page: 'machine_detail', machineRoute: param as string,
          onNewSession: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) void this.prepareAgent(undefined, row); },
          active: this.activeChild === this.childKey(name, param), onChat: (row: ResourceRow) => { this.openMachineChat(param, row); },
          onComputer: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openComputer(row); } })
      } else if (name === 'settings') {
        if (!NavigationContext.from(name, param as string).scoped || this.targetReady(name, param)) SettingsPage({ model: this.settingsKind(param) === 'appearance' ? this.model : this.settingsModel(param), agentWorkspace: this.agentWorkspace, onChat: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openLinkedResource(row); }, active: this.activeChild === this.childKey(name, param), page: this.settingsKind(param), onDetail: (page: string, row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openSettingsDetail(page, row); }, onExtension: (extensionId: string, uiId: string, title: string) => { if (this.activeChild === this.childKey(name, param)) this.openExtension(this.settingsModel(param), extensionId, uiId, title); }, onBack: () => { if (this.activeChild === this.childKey(name, param)) { this.stack.pop(); this.savePath(); } }, onComputer: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openComputer(row); }, onOpen: (page: string) => this.openSettings(page, NavigationContext.from(name, param as string).scoped) })
        else this.targetLoading(name, param)
      }
    }.title({ builder: () => this.destinationTitle(name, param), height: this.headerHeight() }, { systemMaterial: this.titleMaterial })
      .hideTitleBar(this.immersiveComputer(name, param) || this.hideChatTitle(name, param))
      .preferredOrientation(this.screenOrientation(name, param))
      .hideBackButton(this.destinationBackHidden(name, param))
      .backgroundColor((name === 'resources' && param === 'inbox') ||
        (name === 'settings' && ['machines', 'machine_detail'].includes(this.settingsKind(param))) ? Colors.grouped : Colors.canvas)
      .menus(this.destinationMenus(name, param))
      .onShown(() => this.routeShown(name, param))
      .onHidden(() => { if (name === 'computer' && (this.activeChild === this.childKey(name, param) || this.immersiveComputer(name, param))) {
        this.finishComputerFullscreen(); this.computerReturnOrientation = undefined;
      } if (this.pendingInboxHandoff === this.childKey(name, param)) this.pendingInboxHandoff = ''; if (this.activeChild === this.childKey(name, param)) { this.activeChild = ''; this.dismissChatActions(); } if ((name === 'session' || name === 'conversation') && this.routeRow(param).key() === this.detailModel.selected.key()) this.detailModel.hideConversation(false); if (name === 'assistant') this.detailModel.watchDecisions(false); if (name === 'tools') this.detailModel.releaseTerminal(); })
      .onBackPressed((): boolean => { if (this.immersiveComputer(name, param)) { this.exitComputerFullscreen(); return true; } if (this.pendingInboxHandoff === this.childKey(name, param)) this.pendingInboxHandoff = ''; this.detailModel.releaseTerminal(); this.stack.pop(); this.savePath(); return true; })
  }
  build() {
    Stack() {
    WithTheme({ theme: nativeLookTheme(this.look, this.model.style, this.environment.dark), colorMode: this.environment.dark ? ThemeColorMode.DARK : ThemeColorMode.LIGHT }) {
    ThemeScope() {
    Column() {
      if (!this.model.ready && !this.model.profile.id) {
        Column() { LoadingProgress().width(Tokens.avatar).height(Tokens.avatar).color(Colors.link) }
          .justifyContent(FlexAlign.Center).width('100%').height('100%')
      } else if (!this.model.profile.id) {
        HubWelcome({ error: this.model.error, cleanup: this.model.pendingCleanupCount > 0,
          onScan: () => this.openPairing('scan', true), onOther: () => this.openPairing('link'), onGuide: () => this.openPairing('guide'),
          onDemo: () => this.model.useDemo(), onVideo: () => this.openSheet('tour'), onCleanup: () => this.model.retryCleanup() })
          .bindSheet($$this.showSheet, this.sheetContent(), { title: { title: this.sheetTitle() }, height: SheetSize.LARGE, showClose: true,
            keyboardAvoidMode: this.sheetKind === 'connect' ? SheetKeyboardAvoidMode.RESIZE_ONLY : undefined, onAppear: () => this.sheetAppeared() })
      } else {
        Navigation(this.stack) { this.primaryContent() }.title({ builder: this.peerTitle, height: this.headerHeight() }, { systemMaterial: this.titleMaterial }).titleMode(NavigationTitleMode.Mini)
          .hideBackButton(true).hideTitleBar(this.rail()).menus([])
          .backgroundColor(Colors.canvas)
          .toolbarConfiguration(this.rail() ? [{ value: tr('expand'), icon: $r('sys.media.ohos_ic_public_arrow_right'), action: () => this.collapsed = false }] : [])
          .hideNavBar(!!this.fullscreenComputer).mode(this.wide() && !this.fullscreenComputer ? NavigationMode.Split : NavigationMode.Stack)
          .navBarWidth(this.rail() ? Tokens.collapsed : Tokens.master).navBarWidthRange(this.rail() ? [Tokens.collapsed, Tokens.collapsed] : [Tokens.masterMin, Tokens.masterMax])
          .minContentWidth(Tokens.detailMin).enableDragBar(!this.rail()).navDestination(this.destination)
          .onAreaChange(() => this.updateBreakpoint()).width('100%').height('100%')

          .bindSheet($$this.showSheet, this.sheetContent(), { title: { title: this.sheetTitle() }, height: SheetSize.LARGE, showClose: true,
            keyboardAvoidMode: this.sheetKind === 'connect' ? SheetKeyboardAvoidMode.RESIZE_ONLY : undefined, onAppear: () => this.sheetAppeared() })
      }
    }.width('100%').height('100%')
      .bindSheet($$this.spacesShown, this.spacesContent(), { title: { title: tr('conversation_spaces') }, height: SheetSize.LARGE, showClose: true, onAppear: () => { this.spacesLayoutReady = true; this.measureSpaces(); }, onDisappear: () => this.spacesLayoutReady = false })
    }
    }
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
      .bindSheet($$this.readerShown, this.readerContent(), { title: { title: this.readerTitle }, height: Tokens.readerFull, width: Tokens.readerFull, preferType: SheetType.BOTTOM,
        detents: [Tokens.readerFull, Tokens.readerPartial], enableOutsideInteractive: true, showClose: true })
  }
}
