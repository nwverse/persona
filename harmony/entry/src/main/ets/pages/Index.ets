import { AgentStartPicker } from '../components/AgentStartPicker';
import { AgentLaunchChoices, LaunchChoice, RecentAgentSetup, loadAgentSetups, rememberAgentSetup } from '../models/AgentLaunchChoices';
import { WorktreeSheet } from '../components/WorktreeSheet';
import { phonePlaceAtRoot, phonePanIntent, finishesPhonePan } from '../models/PhoneNavigation';
import { PhoneConversationSearch } from '../components/PhoneConversationSearch';
import { SessionSpaceTabs } from '../components/SessionSpaceTabs';
import { SessionSpace, SessionSpaceTab, sessionSpace } from '../models/SessionSpace';
import { MachineDirectory, machineBrandMark } from '../components/MachineDirectory';
import { PhoneIconButton, PhoneStyle, PhoneSectionTitle } from '../design/PhoneStyle';
import { SettingsDirectory, PhoneSettingsOptions } from '../components/SettingsDirectory';
import { WorktreeChange, WorktreeActions } from '../models/WorktreeActions';
import { ProjectCatalog, ProjectWorktree, PersonaProject } from '../models/ProjectCatalog';
import { ProjectsPage } from '../components/ProjectsPage';
import { IconActions } from '../design/IconActions';
import { ThemeScope } from '../design/ThemeScope';
import { util } from '@kit.ArkTS';
import { PhoneAgentSettingsPage } from '../components/PhoneAgentSettingsPage';
import { AdminWorkspacePage } from '../components/AdminWorkspacePage';
import { ExtensionPage } from '../components/ExtensionPage';
import { ExtensionNavigationPage } from '../components/ExtensionNavigationPage';
import { ExtensionNavigation, ExtensionNavigationEntry } from '../models/ExtensionNavigation';
import { extensionIcon } from '../design/ExtensionIcons';
import type { ExtensionUINavigationEffect } from '../models/ExtensionUI';
import { ComputerScreenPage } from '../components/ComputerScreenPage';
import { isPairingAddress } from '../core/WireCompatibility';
import { scanBarcode, scanCore } from '@kit.ScanKit';
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
import { ComposeListItemV2, ContentItemV2, KeyboardAvoidMode, TextModifier, SymbolGlyphModifier, CapsuleSegmentButtonV2, SegmentButtonV2Items, ColorMetrics, LengthMetrics, uiMaterial } from '@kit.ArkUI';
import { ConfigurationConstant } from '@kit.AbilityKit';
import { ClientModel, client } from '../models/ClientModel';
import { ResourceRow, HubProfile, Wire } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { Pairing, PairingTarget } from '../platform/Pairing';
import { Conversation } from '../components/Conversation';
import { AgentListPage } from '../components/AgentListPage';
import { InteractionHistoryPage } from '../components/InteractionHistoryPage';
import { InboxWorkspacePage } from '../components/InboxWorkspacePage';
import { ResourcePage } from '../components/ResourcePage';
import { SettingsPage } from '../components/SettingsPage';
import { SshSettingsPage } from '../components/SshSettingsPage';
import { DeviceLook, deviceLook } from '../models/DeviceLook';
import { nativeLookTheme } from '../design/LookTheme';
import { IconTypeV2 } from '@kit.ArkUI';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { ButtonStyles } from '../design/Controls';
import { AgentWorkspace } from '../models/AgentWorkspace';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';
import { nativeSheetInset } from '../platform/NativeInsets';
import { mainConversationData } from '../core/DefaultConversation';
import { AssistantDetailPage } from '../components/AssistantDetailPage';
import { tr, trError } from '../design/Strings';
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
  @Local phonePath: string[] = [];
  @Local phoneParam: string = '';
  @Local shownParam: string = '';
  @Local chatMenuActions: SheetInfo[] = [];
  @Local chatTitleMenuVisible: boolean = false;
  @Local chatBodyMenuVisible: boolean = false;
  @Local chatMenuRevision: number = 0;
  private routeRevision: number = 0;
  @Local stack: NavPathStack = new NavPathStack();
  @Local breakpoint: WidthBreakpoint = WidthBreakpoint.WIDTH_SM;
  @Local collapsed: boolean = false;
  @Local drawerSearch: string = '';
  @Local drawerSearching: boolean = false;
  @Local recentExpanded: boolean = true;
  @Local chatSearchShown: boolean = false;
  @Local startSearchInAgents: boolean = false;
  @Local drawerAgentsExpanded: boolean = false;
  @Local pendingSearchHit: string = '';
  private pendingSearchTarget: string = '';
  @Local drawerFraction: number = 0;
  @Local drawerMounted: boolean = false;
  @Local phoneWidth: number = 0;
  private phoneHeight: number = 0;
  private gestureKind: string = '';
  private gestureScope: string = '';
  private gesturePlace: number = -1;
  private drawerFrom: number = 0;
  private drawerAnimation: number = 0;
  private drawerTapBlockedUntil: number = 0;
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
  @Local scanning: boolean = false;
  private scanRevision: number = 0;
  @Local resourceKind: string = 'todo';
  @Local newTask: string = '';
  @Local creatingAgent: boolean = false;
  @Local agentModel: ClientModel = client;
  @Local agentTodo: ResourceRow | undefined = undefined;
  private agentFormRevision: number = 0;
  @Local launcher: string = '';
  @Local directory: string = '';
  @Local settingsPage: string = '';
  @Local activeChild: string = '';
  @Local tool: number = 0;
  private titleMaterial: uiMaterial.Material = uiMaterial.isImmersiveMaterialSupported() ? new uiMaterial.ImmersiveMaterial({ style: uiMaterial.ImmersiveStyle.THIN }) : uiMaterial.Material.empty;
  @Local projectRootRevision: number = 0;
  @Local projectCatalog: ProjectCatalog = new ProjectCatalog();
  @Local agentWorktree: ProjectWorktree | undefined = undefined;
  @Local pickedAgentTree: string = '';
  @Local agentLauncherOptions: string[] = [];
  @Local agentLauncherLabels: string[] = [];
  @Local launchChoices: AgentLaunchChoices = new AgentLaunchChoices();
  @Local agentPanel: string = '';
  @Local agentMachineKey: string = '';
  @Local switchingAgentMachine: boolean = false;
  @Local agentModelId: string = '';
  @Local agentEffort: string = '';
  @Local defaultLauncherName: string = '';
  @Local launcherData: Record<string,Object>[] = [];
  @Local blockedLaunchers: LaunchChoice[] = [];
  @Local launchListReady: boolean = false;
  @Local recentSetups: RecentAgentSetup[] = [];
  @Local startFailure: string = '';
  @Local startWorktree: WorktreeActions = new WorktreeActions();
  private startTreeSource: ProjectWorktree | undefined;
  private machineIntent: number = 0;
  @Local agentPresets: Record<string,Object>[] = [];
  @Local agentPreset: string = '';
  @Local machineDefaultLabel: string = '';
  private launcherRevision: number = 0;
  @Local agentWorkspace: AgentWorkspace = new AgentWorkspace();
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Local spacesShown: boolean = false;
  @Local phoneSettingsShown: boolean = false;
  @Local phoneSettingsQuery: string = '';
  private phoneSettingsDepth: number = -1;
  private phoneSettingsOwner: string = '';
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
  private previousPrimary: number = 0;
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
  private projectPlace(): boolean {
    return this.model.section === 4;
  }
  @Monitor('model.projectsRequested', 'model.ready')
  projectLaunchChanged(): void { this.consumeProjectLaunch(); }
  private consumeProjectLaunch(): void {
    if (!this.model.projectsRequested || !this.model.ready || !this.model.profile.id || this.navigationRestoredScope !== this.model.scope()) return;
    this.model.projectsRequested = false; this.projectRootRevision++;
    this.navigationIntent++; this.spacesShown = false;
    if (this.model.section !== 4) this.changeSection(4);
    this.stack.clear(); this.savePath(); this.projectCatalog.refresh(this.model);
  }
  private openProjects(): void {
    this.navigationIntent++; this.spacesShown = false;
    if (!this.projectPlace()) this.changeSection(4);
    else { this.stack.clear(); this.savePath(); }
    this.projectCatalog.refresh(this.model);
  }
  private pushPlace(name: string, key: string): void {
    this.stack.pushPath({ name: name, param: key }); this.savePath();
  }
  private browsePeer(index: number): void {
    if (index !== 0 && index !== 1) return;
    this.spacesShown = false;
    if (this.model.section !== index) this.changeSection(index);
    if (index === 1) this.ensureAgentWorkspace();
    else if (!this.stack.size()) this.openMain();
  }

  private mergedPhone():boolean { return !this.wide() && this.look.phoneNavigation === 'merged'; }
  private syncPhonePath():void {
    this.phonePath = this.stack.getAllPathName().slice();
    const param: Object = this.phonePath.length ? this.stack.getParamByIndex(this.phonePath.length-1) as Object : '';
    this.phoneParam = typeof param === 'string' ? param : '';
  }
  @Computed get phoneTop():string { return this.phonePath[this.phonePath.length-1] || ''; }
  @Computed get phoneAtRoot():boolean {
    const isRootChat = this.phonePath.length === 1 && (this.phoneTop === 'session' || this.phoneTop === 'conversation') &&
      Wire.bool(this.routeRow(this.phoneParam).data(),'navigation_root');
    if (this.mergedPhone() && this.model.section === 1 && this.phoneTop === 'session' && isRootChat) return true;
    return phonePlaceAtRoot(this.model.section,this.phonePath,this.phoneTop === 'conversation' && isRootChat);
  }
  private phonePeer(): boolean {
    return !this.mergedPhone() && this.phoneAtRoot && [0, 1, 4].includes(this.model.section);
  }
  private compactPeerIndex(): number { return this.model.section === 4 ? 1 : this.model.section; }
  private showAllChats(agents:boolean): void {
    this.startSearchInAgents = agents; this.drawerSearching = false; this.spacesShown = false; this.chatSearchShown = true;
  }
  private hitIdentity(row:ResourceRow):string {
    return JSON.stringify([this.model.scope(),row.group || this.model.profile.id,row.manager || this.model.profile.manager,row.kind,row.machine,row.id]);
  }
  private searchHitFor(param:Object):string {
    return this.pendingSearchHit && this.pendingSearchTarget === this.hitIdentity(this.routeRow(param)) ? this.pendingSearchHit : '';
  }
  private openPickedChat(row:ResourceRow,hit:string = ''): void {
    this.pendingSearchHit = hit; this.pendingSearchTarget = hit ? this.hitIdentity(row) : '';

    this.spacesShown = false; this.drawerSearching = false; this.chatSearchShown = false;
    const section = row.kind === 'session' ? 1 : 0;
    if (this.model.section !== section) this.changeSection(section);
    this.stack.clear();
    this.openRow(row, true);
  }
  @Builder phoneSearch() {
    PhoneConversationSearch({model:this.model,initialAgents:this.startSearchInAgents,
      onCancel:() => this.chatSearchShown = false, onOpen:(row:ResourceRow,hit:string) => this.openPickedChat(row,hit)})
  }
  @Builder selectedPeerMenu(index:number) {
    Menu() {
      if (index === 0) {
        MenuItem({content:tr('main_chat')}).onClick(() => { this.stack.clear(); this.openMain(); })
        MenuItem({content:tr('flow_all_chats')}).onClick(() => this.showAllChats(false)).id('shell.menu.chats')
        MenuItem({content:tr('new_chat')}).onClick(() => this.newConversation())
      } else {
        if (this.model.section === 4) {
          MenuItem({content:tr('agents')}).onClick(() => this.browsePeer(1)).id('shell.menu.agents')
        } else {
          ForEach(['all','needsYou','unread','working'],(value:string) => {
            MenuItem({content:tr(value === 'needsYou' ? 'needs_you' : value === 'unread' ? 'agent_group_unread' : value)})
              .selected(this.agentWorkspace.quickFilter === value).selectIcon(true).onClick(() => this.agentWorkspace.setQuickFilter(value))
          },(value:string) => value)
        }
        MenuItem({content:tr('search_sessions')}).onClick(() => this.showAllChats(true))
      }
      MenuItem({content:tr('projects')}).selected(this.model.section === 4).selectIcon(true)
        .onClick(() => this.openProjects()).id('shell.menu.projects')
    }
  }
  @Builder placeButton(label:string,index:number,identity:string,action:() => void) {
    if (index === this.compactPeerIndex()) {
      Button(tr(label),{type:ButtonType.Capsule}).height(30).padding({left:13,right:13}).fontSize(14).maxFontScale(Tokens.navigationTextMaxScale)
        .responseRegion({x:0,y:-7,width:'100%',height:44})
        .fontWeight(FontWeight.Bold).fontColor(Colors.ink).backgroundColor(Colors.surface)
        .accessibilitySelected(true).bindMenu(this.selectedPeerMenu(index)).id(identity)
    } else {
      Button(tr(label),{type:ButtonType.Capsule}).height(30).padding({left:13,right:13}).fontSize(14).maxFontScale(Tokens.navigationTextMaxScale)
        .responseRegion({x:0,y:-7,width:'100%',height:44})
        .fontWeight(FontWeight.Medium).fontColor(Colors.secondary).backgroundColor(Color.Transparent)
        .accessibilitySelected(false).onClick(action).id(identity)
    }
  }
  private drawerWidth(): number { return Math.min(380, Math.max(1,this.phoneWidth * 0.82)); }
  @Monitor('spacesShown') drawerStateChanged(): void {
    if (this.wide()) { this.drawerFraction = 0; this.drawerMounted = false; return; }
    this.settlePhoneDrawer(this.spacesShown);
  }
  private settlePhoneDrawer(open:boolean): void {
    const revision = ++this.drawerAnimation;
    if (this.environment.reduceMotion) {
      this.drawerFraction = open ? 1 : 0;
      this.drawerMounted = open;
      return;
    }
    if (open) this.drawerMounted = true;
    this.getUIContext().animateTo({duration:230,curve:Curve.EaseOut,onFinish:() => {
      if (revision === this.drawerAnimation && !this.spacesShown) this.drawerMounted = false;
    }},() => this.drawerFraction = open ? 1 : 0);
  }
  private startPhonePan(event:GestureEvent): void {
    this.gestureKind = '';
    if (this.wide() || !this.model.profile.id || this.showSheet || this.phoneSettingsShown || this.readerShown || this.chatSearchShown) return;
    const finger = event.fingerList[0];
    if (!finger) return;
    const x = finger.localX - event.offsetX, y = finger.localY - event.offsetY;
    this.gestureKind = phonePanIntent(this.phonePeer() ? this.compactPeerIndex() : this.model.section,this.phoneAtRoot,this.spacesShown,x,y,this.phoneWidth,this.phoneHeight,event.offsetX,event.offsetY);
    this.gestureScope = this.model.scope(); this.gesturePlace = this.model.section;
    this.drawerFrom = this.drawerFraction;
    if (this.mergedPhone() && (this.gestureKind === 'persona' || this.gestureKind === 'agents')) this.gestureKind = '';
    if (this.gestureKind === 'open' || this.gestureKind === 'close') {
      this.drawerAnimation++; this.drawerMounted = true;
    }
  }
  private drawerAcceptsTap():boolean {
    return this.wide() || this.spacesShown && this.gestureKind !== 'close' && Date.now() >= this.drawerTapBlockedUntil;
  }
  private movePhonePan(dx:number): void {
    if (this.environment.reduceMotion) return;
    if (this.gestureKind === 'open' || this.gestureKind === 'close')
      this.drawerFraction = Math.max(0,Math.min(1,this.drawerFrom + dx / this.drawerWidth()));
  }
  private endPhonePan(dx:number,vx:number): void {
    const kind = this.gestureKind; this.gestureKind = '';
    if (!kind) return;
    if (this.gestureScope !== this.model.scope() || this.model.section !== this.gesturePlace) {
      this.settlePhoneDrawer(this.spacesShown); return;
    }
    const done = finishesPhonePan(dx,vx,this.phoneWidth);
    if (kind === 'close') this.drawerTapBlockedUntil = Date.now() + 200;
    if (kind === 'open' || kind === 'close') {
      const open = done ? kind === 'open' && dx > 0 || kind === 'close' && dx >= 0 : this.spacesShown;
      if (open !== this.spacesShown) { this.drawerSearching = false; this.spacesShown = open; }
      else this.settlePhoneDrawer(open);
    } else if (done && this.phonePeer()) this.browsePeer(kind === 'agents' ? 1 : 0);
  }
  @Computed get phoneToolParent(): string {
    const parent = this.phonePath.length - 2;
    if (this.phoneTop !== 'tools' || parent < 0 || this.phonePath[parent] !== 'session') return '';
    const value: Object = this.stack.getParamByIndex(parent) as Object;
    if (typeof value !== 'string') return '';
    return this.routeRow(value).key() === this.routeRow(this.phoneParam).key() ? value : '';
  }
  private phonePageName():string {
    if (this.phoneToolParent) return this.chatTitle(this.phoneToolParent);
    if (this.phoneTop) return this.destinationTitleText(this.phoneTop,this.phoneParam);
    return tr(this.model.section === 4 ? 'projects' : this.model.section === 1 ? 'agents' : 'persona');
  }
  @Computed get phoneSessionSpace():SessionSpace {
    const name = this.phoneTop;
    if ((name !== 'session' && !this.phoneToolParent) || this.activeChild !== this.childKey(name,this.phoneParam) ||
      !this.targetReady(name,this.phoneParam) || !this.detailModel.connected) return new SessionSpace();
    const owner = this.detailModel;
    return sessionSpace(owner.selected,owner.sessions,this.projectCatalog.projects,owner.profile.id,owner.profile.manager);
  }
  private switchSessionTab(key:string,from:string,id:string):void {
    const space = this.phoneSessionSpace;
    if (!key || key !== space.key || from !== space.current || id === from || !this.stack.size()) return;
    const tab = space.tabs.find((item:SessionSpaceTab) => item.id === id);
    if (!tab) return;
    const top = this.stack.size()-1;
    const parent = this.phoneToolParent, name = parent ? 'tools' : 'session';
    if (this.stack.getAllPathName()[top] !== name || this.stack.getParamByIndex(top) !== this.phoneParam) return;
    const old = this.routeRow(parent || this.phoneParam), target = this.routeTarget(tab.row,Wire.bool(old.data(),'navigation_root'));
    target.group = this.detailModel.profile.id; target.manager = this.detailModel.profile.manager;
    if (this.model.section === 4) this.projectCatalog.browser.selectedSession = target.key();
    this.pendingSearchTarget = ''; this.pendingSearchHit = ''; this.chatReady = false; this.detailRoute = ''; this.tool = 0;
    if (parent) this.stack.pop(false);
    this.stack.replacePath({name:'session',param:JSON.stringify(target)},false);
    this.savePath();
  }
  private phoneLargeTitle():boolean {
    if (!this.phoneTop) return this.mergedPhone() && (this.model.section === 1 || this.model.section === 4);
    return ['project','applications'].includes(this.phoneTop) ||
      this.phoneTop === 'resources' && this.phoneParam === 'health' ||
      this.phoneTop === 'settings' && this.phonePath.length === 1 && ['machines','schedules'].includes(this.settingsKind(this.phoneParam));
  }
  private returnToPhoneSettings(): boolean {
    if (this.phoneSettingsDepth < 0 || this.stack.size() !== this.phoneSettingsDepth + 1 || this.stack.getAllPathName()[this.stack.size() - 1] !== 'settings' ||
      this.phoneSettingsOwner !== this.model.scope() || this.wide()) return false;
    this.phoneSettingsDepth = -1; this.stack.pop(); this.savePath();
    this.spacesShown = true; this.phoneSettingsShown = true; return true;
  }
  private phoneBack(): void {
    if (!this.stack.size() || this.returnToPhoneSettings()) return;
    this.detailModel.releaseTerminal();
    if (this.model.section >= 2 && this.model.section !== 4 && this.stack.size() === 1) this.changeSection(this.previousPrimary);
    else { this.stack.pop(); this.savePath(); }
  }
  private startFromPlace(): void {
    if (this.projectPlace()) {
      const list = this.stack.getAllPathName(), pos = list.lastIndexOf('project_tree');
      const tree = pos >= 0 ? this.projectCatalog.tree(this.stack.getParamByIndex(pos) as string) : undefined;
      this.prepareAgent(undefined, tree?.canStart ? tree : undefined);
    } else if (this.peer() === 1) this.openSheet('newagent');
    else this.newConversation();
  }
  @Builder compactPlaces() {
    Column() {
      Row({space: 6}) {
        ForEach([this.phoneAtRoot ? 'root' : 'back'],(role:string) => {
          if (role === 'root') {
            if (this.phonePeer()) {
              Button({type:ButtonType.Circle}) {
                Image(this.look.avatarUri || $r('app.media.foreground')).width(29).height(29).objectFit(ImageFit.Contain).borderRadius(15)
              }.width(36).height(36).padding(0).backgroundColor(Colors.surface).border({width:0.5,color:Colors.divider,radius:18})
                .responseRegion({x:-4,y:-4,width:44,height:44}).accessibilityText(tr('conversation_spaces'))
                .onClick(() => { this.drawerSearching = false; this.spacesShown = true; }).id('shell.spaces')
            } else {
              PhoneIconButton({symbol:Symbols.spaces,label:tr('conversation_spaces'),identity:'shell.spaces',
                action:() => { this.drawerSearching = false; this.spacesShown = true; }})
            }
          } else {
            PhoneIconButton({symbol:Symbols.collapse,label:tr('back'),identity:'shell.back',action:() => this.phoneBack()})
              .bindContextMenu(this.backHistory(),ResponseType.LongPress)
          }
        },(role:string) => role)
        if (this.phonePeer()) {
          Blank().layoutWeight(1)
          Row({space: 1}) {
            this.placeButton('persona', 0, 'shell.persona', () => this.browsePeer(0))
            this.placeButton('agents', 1, 'shell.agents', () => this.browsePeer(1))
          }.padding(3).borderRadius(18).backgroundColor(Colors.control)
          Blank().layoutWeight(1)
        } else if ((this.phoneTop === 'session' || this.phoneToolParent) && this.phoneSessionSpace.title) {
          Column({space:2}) {
            Text(this.phonePageName()).fontSize(17).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
              .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
            Row({space:4}) {
              SymbolGlyph(Symbols.folder).fontSize('11vp').fontColor([Colors.secondary])
              Text(this.phoneSessionSpace.title).fontSize(12).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary)
                .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
            }.id('session.space.title')
          }.layoutWeight(1).alignItems(HorizontalAlign.Center)
        } else if (this.phoneTop && !this.phoneLargeTitle()) {
          Text(this.phonePageName()).layoutWeight(1).fontSize(17).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
            .textAlign(TextAlign.Center).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
        } else Blank().layoutWeight(1)
        if (this.phonePeer() || this.projectPlace() && (!this.phoneTop || this.phoneTop === 'project' || this.phoneTop === 'project_tree') || this.mergedPhone() && this.phoneAtRoot && (this.model.section === 0 || this.model.section === 1 && !this.phoneTop)) {
          PhoneIconButton({symbol: this.phonePeer() || this.model.section === 0 ? Symbols.edit : Symbols.plus, label: tr(this.model.section === 0 ? 'new_chat' : 'new_agent'), identity:'shell.new', action:() => this.startFromPlace()})
        } else if (this.activeChild && this.destinationMenuItems(this.shownName, this.shownParam).length) {
          this.destinationMenus(this.shownName, this.shownParam)
        } else if (this.phoneTop && !this.phoneLargeTitle()) Blank().width(36)
      }.width('100%').constraintSize({minHeight:46}).padding({left:12,right:12,top:3,bottom:3}).alignItems(VerticalAlign.Center)
      if ((this.phoneTop === 'session' || this.phoneToolParent) && this.activeChild && this.targetReady(this.phoneTop,this.phoneParam) && this.detailModel.selected.machine) {
        if (this.phoneSessionSpace.tabs.length) SessionSpaceTabs({space:this.phoneSessionSpace,
          onPick:(key:string,from:string,id:string) => this.switchSessionTab(key,from,id)})
        if (this.phoneTop === 'session') Row({space:3}) {
          this.sessionMode(tr('chat'),Symbols.persona,-1)
          this.sessionMode(tr('terminal'),Symbols.device,2)
          this.sessionMode(tr('git'),Symbols.apps,1)
          this.sessionMode(tr('files'),Symbols.folder,0)
        }.width('100%').padding({left:12,right:12,top:1,bottom:5})
      }
      if (this.phoneLargeTitle()) Text(this.phonePageName()).fontSize(PhoneStyle.title).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
        .width('100%').maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).padding({left:PhoneStyle.page,right:PhoneStyle.page,top:4,bottom:8}).id('shell.page_title')
    }.width('100%').backgroundColor(this.phonePeer() && this.model.section === 0 ? Colors.canvas : Colors.grouped)
  }
  private parentIndexes():number[] {
    const levels:number[] = [];
    for (let i=this.stack.size()-2; i>=0; i--) levels.push(i);
    return levels;
  }
  @Builder backHistory() {
    Menu() {
      ForEach(this.parentIndexes(),(i:number) => {
        MenuItem({content:this.destinationTitleText(this.stack.getAllPathName()[i],this.stack.getParamByIndex(i) || '')})
          .onClick(() => { if (i < this.stack.size()-1) { this.stack.popToIndex(i); this.savePath(); } }).id('shell.back.level')
      },(i:number) => String(i))
      if (this.model.section === 1 || this.model.section === 4) MenuItem({content:tr(this.model.section === 4 ? 'projects' : 'agents')})
        .onClick(() => { this.stack.clear(); this.savePath(); }).id('shell.back.root')
    }
  }
  @Builder sessionMode(label:string,symbol:Resource,index:number) {
    Button({type:ButtonType.Capsule}) {
      Row({space:4}) {
        SymbolGlyph(symbol).fontSize('14vp').fontColor([index < 0 ? Colors.link : Colors.secondary])
        Text(label).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1).fontWeight(index < 0 ? FontWeight.Bold : FontWeight.Normal).fontColor(index < 0 ? Colors.link : Colors.secondary)
      }
    }.layoutWeight(1).constraintSize({minHeight:Tokens.touch}).padding({left:2,right:2}).backgroundColor(index < 0 ? Colors.surface : Color.Transparent)
      .accessibilityText(label).bindTips(label).accessibilitySelected(index < 0).onClick(() => { if (index >= 0 && this.phoneTop === 'session' && this.targetReady(this.phoneTop,this.phoneParam)) this.openTools('',index); })
      .id(index < 0 ? 'session.mode.chat' : index === 2 ? 'session.mode.terminal' : index === 1 ? 'session.mode.git' : 'session.mode.files')
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
  private openSheet(kind: string): void { this.navigationIntent++; if (kind === 'connect') this.pairingRevision++; this.readerShown = false; if (kind === 'newagent') { this.prepareAgent(); return; } this.sheetKind = kind; this.showSheet = true; }
  private sheetTitle(): string { return tr(this.sheetKind === 'hub' ? 'hub' : this.sheetKind === 'newagent' ? 'new_agent' : 'welcome'); }
  @Builder sheetContent() {
    if (this.sheetKind === 'hub') this.workspaceSheet()
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
  aboutToDisappear(): void { this.agentWorkspace.hide(); this.applications.dispose(); this.dismissChatActions(); }
  @Monitor('model.profile', 'model.connected', 'model.features') applicationOwnerChanged(): void {
    this.applications.bind(this.model); this.applications.connectionChanged();
  }
  @Monitor('model.profile') scopeChanged(): void { if (this.activeScope !== this.model.scope()) { this.pendingSearchHit = ''; this.pendingSearchTarget = ''; this.chatSearchShown = false; this.drawerSearching = false; this.phoneSettingsQuery = ''; this.phoneSettingsShown = false; this.phoneSettingsDepth = -1; this.pendingInboxHandoff = ''; this.projectCatalog.clear(); this.agentWorktree = undefined; this.pickedAgentTree = ''; this.agentWorkspace.hide(); this.spacesShown = false; this.dismissChatActions(); this.showSheet = false; this.agentFormRevision++; this.creatingAgent = false; this.agentTodo = undefined; this.readerShown = false; this.readerAddress = ''; this.readerTitle = ''; this.detailModel.hideConversation(); this.detailModel = this.model; this.chatReady = false; this.detailRoute = ''; this.routeRevision++; this.bindDeviceOwner(this.model); } this.syncConversationRows(); this.restoreScope(); deviceFeatures.context(); }
  @Monitor('model.conversations', 'model.mainId', 'model.mainMachineId') conversationsChanged(): void { this.syncConversationRows(); }
  @Monitor('model.appearance') appearanceChanged(): void {
    this.getUIContext().getHostContext()?.getApplicationContext().setColorMode(this.model.appearance === 1 ? ConfigurationConstant.ColorMode.COLOR_MODE_LIGHT : this.model.appearance === 2 ? ConfigurationConstant.ColorMode.COLOR_MODE_DARK : ConfigurationConstant.ColorMode.COLOR_MODE_NOT_SET);
  }
  @Monitor('detailModel.connected') chatMenuConnectionChanged(): void { if (!this.detailModel.connected && (this.chatTitleMenuVisible || this.chatBodyMenuVisible)) this.dismissChatActions(); }
  @Monitor('model.directorySessions', 'model.directoryMachines') projectDirectoryChanged(): void {
    if (this.model.section === 4) this.projectCatalog.syncRows(this.model);
  }
  @Monitor('model.connected') connectionChanged(): void { if (this.model.section === 4) this.projectCatalog.refresh(this.model); deviceFeatures.context(); if (this.model.connected && this.shownName && this.activeChild === this.childKey(this.shownName, this.shownParam)) this.restoreTarget(this.shownName, this.shownParam); }
  @Monitor('model.forgottenEpoch') forgetNavigation(): void {
    for (const key of this.paths.keys()) if (isHubStateKey('view:' + key, this.model.forgottenProfile)) this.paths.delete(key);
    this.rowVisibility.clear(); this.visibilityEpoch++;
  }
  private async restoreScope(): Promise<void> {
    const scope = this.model.scope(); if (scope === this.activeScope) return;
    this.activeScope = scope; this.stack = this.paths.get(scope + ':' + this.model.section) || new NavPathStack(); this.navigationRestoredScope = ''; this.rowVisibility.clear(); const epoch = ++this.restoring, intent = this.navigationIntent; this.syncPhonePath();
    let view: Record<string, Object>;
    try { view = Wire.parse(await this.model.loadView('navigation')); }
    catch (_) { if (epoch === this.restoring && scope === this.model.scope()) { this.activeScope = ''; this.model.error = 'cache_unavailable'; } return; }
    if (epoch !== this.restoring || scope !== this.model.scope()) return;
    if (intent === this.navigationIntent) this.model.section = Math.max(0, Math.min(11, Wire.number(view, 'section')));
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
    this.paths.set(scope + ':' + this.model.section, this.stack); this.syncPhonePath();
    this.navigationRestoredScope = scope;
    if (this.model.projectsRequested && this.model.ready) { this.consumeProjectLaunch(); return; }
    if (intent !== this.navigationIntent) this.savePath();
    if (this.model.section === 2 && !this.stack.size()) this.openResources('inbox');
    else if (this.model.section === 3 && !this.stack.size()) this.openSettingsRoot();
    else if (this.model.section === 4) this.projectCatalog.refresh(this.model);
    else if (this.model.section >= 5 && !this.stack.size()) this.showPlaceRoot(this.model.section);
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
    if (!this.model.pairingLink) return; this.address = this.model.pairingLink; this.pairCode = ''; this.model.pairingLink = ''; this.openSheet('connect');
  }
  private savePath(): void {
    if (this.activeScope !== this.model.scope()) this.restoreScope();
    this.syncPhonePath(); this.navigationIntent++;
    const key = this.model.scope() + ':' + this.model.section;
    if (this.navigationRestoredScope === this.model.scope() || this.stack.size() || this.paths.has(key)) this.paths.set(key, this.stack);
    const tabs: Record<string, Object>[] = [];
    for (let index = 0; index < 12; index++) { const stack = this.paths.get(this.model.scope() + ':' + index);
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
    this.chatReady = false; this.detailRoute = '';
    if (row.kind === 'session') { this.stack.clear(); this.tool = 0; }
    this.stack.pushPath({ name: name, param: JSON.stringify(this.routeTarget(row, root || !this.stack.size())) }); this.savePath();
  }
  private async openLinkedResource(row: ResourceRow): Promise<void> {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), source = this.activeChild;
    try {
      if (!['inbox', 'todo', 'knowledge', 'file', 'todo_source', 'todo_thread'].includes(row.kind)) await this.model.viewFor(row);
      if (!this.model.owns(generation, scope) || this.activeChild !== source) return;
      this.chatReady = false; this.detailRoute = '';
      const target = this.routeTarget(row, false), name = NavigationContext.destination(target), data = target.data();
      const handoff = target.kind === 'inbox' && Wire.bool(data, 'open_handoff');
      if (Wire.has(data, 'open_handoff')) data['open_handoff'] = false; target.raw = JSON.stringify(data);
      const param = JSON.stringify(target);
      if (handoff) this.pendingInboxHandoff = this.childKey(name, param);
      this.stack.pushPath({ name: name, param: param }); this.savePath();
    } catch (error) { if (this.model.owns(generation, scope) && this.activeChild === source) this.model.reportError(error as Error); }
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
  private openSettings(page: string, contextual: boolean = false): void { if (page === 'appearance' || page === 'phone_agent_layout') this.ensureAgentWorkspace(); this.settingsPage = page; this.stack.pushPath({ name: 'settings', param: NavigationContext.settings(page, contextual ? this.detailModel.selected : undefined) }); this.savePath(); }
  private settingsFamily(page: string): string { return ['extension_detail', 'extension_catalog'].includes(page) ? 'extensions' : page === 'connector_detail' ? 'connectors' : page === 'channel_detail' ? 'channels' : page; }
  private isAdminPage(page: string): boolean { return ['assistant', 'memory', 'managers', 'phone_new_assistant', 'launchers', 'engine', 'hub_update', 'node_update'].includes(page); }
  private settingsLabel(page: string): string { return page === 'phone_new_assistant' ? 'phone_new_assistant' : page === 'phone_agent_options' ? 'agent' : page === 'phone_language_options' ? 'settings_language_options_title' : page === 'ssh' ? 'ssh_settings' : page === 'assistant' ? 'admin_identity' : page === 'memory' ? 'admin_memory' : page === 'managers' ? 'admin_managers' : page === 'launchers' ? 'admin_launchers' : page === 'engine' ? 'admin_machine_engine' : page === 'hub_update' ? 'admin_hub_update' : page === 'node_update' ? 'admin_node_update' : page; }
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
  private async connect(): Promise<void> {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), address = this.address, code = this.pairCode, revision = this.pairingRevision;
    const current = (): boolean => this.showSheet && this.sheetKind === 'connect' && revision === this.pairingRevision && this.model.owns(generation, scope) && this.address === address && this.pairCode === code;
    this.model.busy = true; this.model.error = '';
    try {
      const pairing = isPairingAddress(this.address.trim()) ? Pairing.parse(this.address.trim()) : Pairing.manual(this.address.trim(), this.pairCode.trim());
      const target = await pairing.inspect(); if (!current()) return; this.model.busy = false;
      this.getUIContext().showAlertDialog({ title: tr('verify'), message: target.name + '\n' + target.baseUrl + '\n' + target.hubId,
        primaryButton: { value: tr('cancel'), action: () => {} },
        secondaryButton: { value: tr('trust'), action: () => { if (current()) this.redeem(pairing, target, current); } } });
    } catch (error) {
      if (!current()) return;
      const detail = error as Error as Object as Record<string, Object>; const code = detail['code'];
      this.model.diagnosticCode = error instanceof Error && /^[a-z_0-9]{1,64}$/.test(error.message) ? error.message : typeof code === 'number' ? String(code) : 'pairing_failed';
      hilog.error(0x0000, 'PersonaPair', '%{public}s', this.model.diagnosticCode);
      this.model.error = 'connection_failed';
    } finally { if (this.model.owns(generation, scope)) this.model.busy = false; }
  }
  private async redeem(pairing: Pairing, target: PairingTarget, admit: () => boolean): Promise<void> {
    const revision = this.pairingRevision, address = this.address, code = this.pairCode;
    try {
      await this.model.pair(pairing, target, admit);
      if (this.sheetKind === 'connect' && revision === this.pairingRevision && this.address === address && this.pairCode === code && this.model.profile.id === target.hubId) { this.pairCode = ''; this.address = ''; this.showSheet = false; }
    } catch (_) { if (admit()) this.model.error = 'connection_failed'; }
  }
  private menus(): NavigationMenuItem[] {
    if (this.rail()) return [];
    return [
      { value: tr(this.peer() === 1 ? 'new_agent' : 'new_chat'), symbolIcon: new SymbolGlyphModifier(Symbols.plus), action: () => this.peer() === 1 ? this.openSheet('newagent') : this.newConversation() },
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
    }.height(this.headerHeight()).alignItems(VerticalAlign.Center).padding({ right: this.wide() ? Tokens.page : 0 })
  }
  private openSettingsRoot(): void { this.stack.pushPath({ name: 'settings_root', param: '' }); this.savePath(); }
  private openSpace(kind: string): void {
    this.phoneSettingsDepth = -1;
    if (kind === 'settings' && !this.wide() && this.spacesShown) {
      this.phoneSettingsQuery = '';
      this.phoneSettingsShown = true; return;
    }
    this.spacesShown = false;
    if (kind === 'projects') this.openProjects();
    else if (kind === 'agents') {
      if (this.model.section === 1) { this.stack.clear(); this.savePath(); }
      this.browsePeer(1);
    }
    else if (kind === 'persona') { this.selectPeer(0); this.stack.clear(); this.openMain(); }
    else if (kind === 'hub') this.openSheet('hub');
    else if (kind === 'collapse') this.collapsed = true;
    else {
      const index = ['persona', 'agents', 'inbox', 'settings', 'projects', 'todo', 'knowledge', 'health', 'devices', 'schedules', 'recordings', 'applications'].indexOf(kind);
      if (index < 2) return;
      const before = this.model.section;
      if (before === 0 || before === 1 || before === 4) this.previousPrimary = before;
      if (before !== index) this.changeSection(index);
      else { this.stack.clear(); this.savePath(); }
      if (!this.stack.size()) this.showPlaceRoot(index);
    }
  }
  private showPlaceRoot(index: number): void {
    if (index === 2) this.openResources('inbox');
    else if (index === 3) this.openSettingsRoot();
    else if (index === 5) this.openResources('todo');
    else if (index === 6) this.openResources('knowledge');
    else if (index === 7) this.openResources('health');
    else if (index === 8) this.openSettings('machines');
    else if (index === 9) this.openSettings('schedules');
    else if (index === 10) this.pushPlace('recordings', '');
    else if (index === 11) this.openApplications();
  }
  private spaceSymbol(kind: string): Resource {
    if (kind === 'schedules') return Symbols.calendar;
    if (kind === 'recordings') return $r('sys.symbol.mic');
    if (kind === 'projects') return Symbols.folder;
    if (kind === 'agents') return Symbols.agents;
    if (kind === 'inbox') return Symbols.inbox;
    if (kind === 'todo') return Symbols.check;
    if (kind === 'knowledge') return Symbols.document;
    if (kind === 'health') return Symbols.health;
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
    if (kind === 'devices') return Colors.working;
    return Colors.secondary;
  }
  private spaceTitle(kind: string): string {
    if (kind === 'hub') return this.model.profile.name || tr('hub');
    if (kind === 'persona') return tr('main_chat');
    if (kind === 'todo') return tr('todos');
    if (kind === 'health') return tr('health_home_title');
    if (kind === 'devices') return tr('phone_machines');
    if (kind === 'recordings') return tr('knowledge_kind_meetings');
    return tr(kind);
  }
  private matchesDrawer(text: string): boolean { return !this.drawerSearch.trim() || text.toLowerCase().includes(this.drawerSearch.trim().toLowerCase()); }
  @Computed get drawerManagers(): ResourceRow[] {
    return this.model.listedManagers();
  }
  @Computed get drawerManager(): ResourceRow | undefined {
    return this.drawerManagers.find((row: ResourceRow) => row.id === this.model.profile.manager && !row.isInstallingManager());
  }
  private managerLine(row: ResourceRow): string {
    if (row.isInstallingManager()) return tr('admin_manager_installing');
    const data = row.data();
    if (!Wire.has(data, 'online')) return '';
    if (!Wire.bool(data, 'online')) return tr('offline');
    const host = Wire.text(data, 'host');
    return host ? tr('online') + ' · ' + host : tr('online');
  }
  @Builder drawerManagerMenu(scope: string) {
    Menu() {
      ForEach(this.drawerManagers, (row: ResourceRow) => {
        MenuItem({ content: row.title || row.id, labelInfo: this.managerLine(row) })
          .selected(row.id === this.model.profile.manager).selectIcon(true).enabled(!row.isInstallingManager())
          .onClick(() => { if (this.drawerAcceptsTap()) this.model.selectListedManager(row.id, scope); }).id('spaces.assistant.' + row.id)
      }, (row: ResourceRow) => row.key())
      MenuItem({ content: tr('phone_new_assistant') }).onClick(() => {
        if (this.model.scope() !== scope || !this.drawerAcceptsTap()) return;
        this.spacesShown = false; this.openSettings('phone_new_assistant');
      }).id('spaces.new_assistant')
      MenuItem({ content: tr('phone_manage_assistants') }).onClick(() => {
        if (this.model.scope() !== scope || !this.drawerAcceptsTap()) return;
        this.spacesShown = false; this.openSettings('managers');
      }).id('spaces.manage_assistants')
    }
  }
  @Builder assistantHeading(row: ResourceRow) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 10 }) {
        Stack({ alignContent: Alignment.BottomEnd }) {
          Image(this.look.avatarUri || $r('app.media.foreground')).width(36).height(36).objectFit(ImageFit.Contain).borderRadius(18)
          if (Wire.has(row.data(), 'online')) Circle().width(10).height(10)
            .fill(Wire.bool(row.data(), 'online') ? Colors.positive : Colors.secondary)
            .stroke(Colors.surface).strokeWidth(2)
        }
        Column({ space: 1 }) {
          Row({ space: 5 }) {
            Text(row.title || row.id).fontSize(22).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
              .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).flexShrink(1)
            SymbolGlyph(Symbols.down).fontSize('12vp').fontColor([Colors.secondary])
          }
          if (this.managerLine(row)) Text(this.managerLine(row)).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        }.alignItems(HorizontalAlign.Start).layoutWeight(1)
      }.width('100%').constraintSize({ minHeight: 44 })
    }.padding(0).backgroundColor(Color.Transparent).layoutWeight(1)
      .accessibilityText([tr('phone_switch_assistant'), row.title || row.id, this.managerLine(row)].filter((s: string) => !!s).join(' · '))
      .bindMenu(this.drawerManagerMenu(this.model.scope())).id('spaces.assistant_switcher')
  }
  @Computed get drawerAgentRows():ResourceRow[] {
    if (!this.mergedPhone()) return [];
    return this.model.directorySessions.filter((r:ResourceRow) => !Wire.bool(r.data(),'assistant') && !Wire.bool(r.data(),'archived') && !Wire.bool(r.data(),'is_subagent'))
      .sort((a:ResourceRow,b:ResourceRow) => (Date.parse(Wire.text(b.data(),'updated_at')) || 0) - (Date.parse(Wire.text(a.data(),'updated_at')) || 0));
  }
  private placeCount(kind:string): number {
    const rows = kind === 'todo' ? this.model.todos : this.model.inbox;
    return rows.filter((r:ResourceRow) => !r.isDone() && !Wire.bool(r.data(),'handled') && !['done','handled','archived'].includes(r.state)).length;
  }
  @Builder spaceRow(kind: string) {
    if (this.matchesDrawer(this.spaceTitle(kind))) ListItem() {
      Row({ space: 16 }) {
        if (kind === 'persona') Image(this.look.avatarUri || $r('app.media.foreground')).width(24).height(24).objectFit(ImageFit.Contain).borderRadius(12)
        else SymbolGlyph(this.spaceSymbol(kind)).fontSize('22vp').fontColor([Colors.ink]).width(24)
        Column({space:1}) {
          Text(kind === 'persona' ? tr('persona') : this.spaceTitle(kind)).fontSize(17).fontWeight(kind === 'persona' ? FontWeight.Medium : FontWeight.Regular).fontColor(Colors.ink)
          if (kind === 'persona') Text(tr('main_chat')).fontSize(12).fontColor(Colors.secondary)
        }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        if ((kind === 'inbox' || kind === 'todo') && this.placeCount(kind) > 0) Text(String(this.placeCount(kind))).fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.link)
      }.padding({left:12,right:12,top:5,bottom:5}).constraintSize({minHeight:44}).width('100%')
    }.borderRadius(10).backgroundColor(this.activeModule() === kind ? Colors.control : Color.Transparent)
      .selectable(true).selected(this.activeModule() === kind).onClick(() => { if (this.drawerAcceptsTap()) this.openSpace(kind); }).id('spaces.' + kind)
  }
  @Builder spacesContent() {
    Column() {
      if (!this.wide()) {
        Row({ space: 8 }) {
          if (this.drawerManager) this.assistantHeading(this.drawerManager)
          else Text(tr('persona')).fontSize(27).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
          PhoneIconButton({symbol: $r('sys.symbol.magnifyingglass'), label:tr('search'), identity:'spaces.search_toggle',
            action:() => { if (!this.drawerAcceptsTap()) return; if (this.mergedPhone()) this.showAllChats(this.model.section === 1); else this.drawerSearching = !this.drawerSearching; this.drawerSearch = ''; }})
        }.width('100%').constraintSize({minHeight:56}).padding({left:16,right:12,top:6,bottom:4})
      }
      if (!this.wide() && this.drawerSearching) {
        PhoneConversationSearch({model:this.model,inline:true,showScopes:this.mergedPhone() || this.model.section === 1 || this.model.section === 4,
          onCancel:() => this.drawerSearching = false,onOpen:(row:ResourceRow,hit:string) => this.openPickedChat(row,hit)}).layoutWeight(1)
      } else {
      List() {
        this.spaceRow('persona')
        ListItem() { Divider().color(Colors.divider).strokeWidth(0.5).margin({left:12,right:12,top:4,bottom:6}) }
        this.spaceRow('agents')
        this.spaceRow('projects')
        this.spaceRow('recordings')
        this.spaceRow('inbox')
        this.spaceRow('todo')
        this.spaceRow('schedules')
        this.spaceRow('knowledge')
        this.spaceRow('health')
        if (this.applications.visible) this.spaceRow('applications')
        this.spaceRow('devices')
        if (this.wide()) this.spaceRow('settings')
        if (this.conversationRowBindings.length) ListItem() {
          Row() {
            Text(tr('recent')).fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).layoutWeight(1)
            SymbolGlyph(this.recentExpanded ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
          }.width('100%').constraintSize({minHeight:44}).padding({left:13,right:14,top:6,bottom:4}).onClick(() => { if (this.drawerAcceptsTap()) this.recentExpanded = !this.recentExpanded; }).id('spaces.recent')
        }
        if (this.recentExpanded || this.drawerSearch) ForEach(this.conversationRowBindings, (binding: BoundRow<ResourceRow>) => {
          if (this.matchesDrawer(binding.value.title)) ListItem() { this.conversationRow(binding, true) }
            .onClick(() => { if (this.drawerAcceptsTap()) this.openPickedChat(binding.value); })
        }, (binding: BoundRow<ResourceRow>) => binding.key)
        ListItem() {
          Row() { Text(tr('flow_all_chats')).fontSize(16).fontColor(Colors.ink).layoutWeight(1); SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary]) }
            .width('100%').height(44).padding({left:13,right:14})
        }.onClick(() => { if (this.drawerAcceptsTap()) this.showAllChats(false); }).id('spaces.all_chats')
        if (this.mergedPhone() && this.drawerAgentRows.length) {
          ListItem() {
            Row() {
              Text(tr('agents')).fontSize(14).fontWeight(FontWeight.Bold).fontColor(Colors.secondary).layoutWeight(1)
              Text(String(this.drawerAgentRows.length)).fontSize(12).fontColor(Colors.secondary)
              SymbolGlyph(this.drawerAgentsExpanded ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.width('100%').constraintSize({minHeight:44}).padding({left:13,right:14}).onClick(() => { if (this.drawerAcceptsTap()) this.drawerAgentsExpanded = !this.drawerAgentsExpanded; }).id('spaces.agent_group')
          }
          if (this.drawerAgentsExpanded) ForEach(this.drawerAgentRows.slice(0,12),(r:ResourceRow) => {
            ListItem() {
              Row({space:8}) {
                SymbolGlyph(Symbols.agent).fontSize('16vp').fontColor([Colors.secondary])
                Column({space:2}) {
                  Text(r.title || tr('agents')).fontSize(15).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
                  Text(Wire.text(r.data(),'last_text') || r.path).fontSize(12).fontColor(Colors.secondary).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
                }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              }.padding({left:15,right:12,top:5,bottom:5}).width('100%').constraintSize({minHeight:42})
            }.onClick(() => { if (this.drawerAcceptsTap()) this.openPickedChat(r); }).accessibilityGroup(true).id('spaces.agent')
          },(r:ResourceRow) => JSON.stringify([r.key(),r.title,r.state,Wire.text(r.data(),'last_text')]))
        }
        this.spaceRow('hub')
        if (this.wide() && !this.rail()) this.spaceRow('collapse')
      }.width('100%').layoutWeight(1).padding({left:5,right:5})
      }
      if (!this.wide()) {
        Row({space:8}) {
          Button({type:ButtonType.Capsule}) {
            Row({space:6}) {
              SymbolGlyph(Symbols.edit).fontSize('16vp').fontColor([Color.White])
              Text(tr('new_chat')).fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Medium).fontColor(Color.White)
            }
          }.constraintSize({minHeight:Tokens.touch}).padding({left:14,right:14}).backgroundColor(Colors.link).accessibilityText(tr('new_chat'))
            .onClick(() => { if (!this.drawerAcceptsTap()) return; this.spacesShown = false; if (this.model.section !== 0) this.changeSection(0); this.newConversation(); }).id('spaces.new')
          Blank().layoutWeight(1)
          PhoneIconButton({symbol:Symbols.gear,label:tr('settings'),identity:'spaces.settings',action:() => { if (this.drawerAcceptsTap()) this.openSpace('settings'); }})
            .bindSheet($$this.phoneSettingsShown, this.phoneSettings(), {height: '92%',showClose:false,dragBar:false,backgroundColor:Colors.grouped})
        }.width('100%').padding({left:16,right:14,top:6})
      }
    }.width('100%').height('100%').padding({bottom:this.spacesInset + 12}).backgroundColor(Colors.surface)
      .id('shell.spaces.content').onAreaChange(() => this.measureSpaces())
  }
  private measureSpaces(): void {
    if (!this.spacesLayoutReady) return;
    const value = nativeSheetInset(this.getUIContext(), 'shell.spaces.content', this.environment.safeBottomPx);
    if (Math.abs(value - this.spacesInset) > 0.1) this.spacesInset = value;
  }
  @Monitor('environment.safeBottomPx') safeBottomChanged(): void { this.measureSpaces(); }
  @Monitor('showSheet', 'spacesShown') modalChanged(): void { if (!this.showSheet && !this.spacesShown) this.ensureMainDestination(); }
  private openHistory(): void { this.stack.pushPath({ name: 'history', param: JSON.stringify(this.detailModel.selected) }); this.savePath(); }
  private openTools(path: string = '', tab: number = 0): void { const data = Wire.record(JSON.parse(JSON.stringify(this.detailModel.selected)) as Object); if (path) data['open_file'] = path; if (tab) data['entry_tab'] = tab; this.stack.pushPath({ name: 'tools', param: JSON.stringify(data) }); this.savePath(); }
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
      ListItem() { Text(trError(this.model.error)).fontSize(Tokens.meta).padding(Tokens.page) }
    }
  }
  private retryRoot(): void {
    if (this.navigationRestoredScope !== this.model.scope()) { this.activeScope = ''; this.model.error = ''; this.restoreScope(); }
    else if (this.model.connected) { this.model.error = ''; this.defaultMainAttempt = ''; this.model.refresh().then(() => this.ensureMainDestination()); }
    else this.model.foregrounded();
  }
  @Builder connectionState() {
    Row({ space: Tokens.gap }) { Text(trError(this.model.error)).fontSize(Tokens.meta).layoutWeight(1)
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
      if (name === 'settings') return this.settingsKind(param) === 'machines' ? 'devices' : 'settings';
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
    AgentListPage({ compactRoot:!this.wide() && !this.rail(), model: this.model, workspace: this.agentWorkspace, retainWorkspace: true, active: this.model.section === 1, selectedRow: this.detailModel.selected, selectionVisible: this.chatSelectionShown('session'), rail: this.rail(), readingDetail: this.readingAgentDetail(),
      onOpen: (row: ResourceRow) => this.openRow(row), onVisible: (row: ResourceRow, ratio: number) => {
        this.rowVisibility.set(this.model.scope() + row.key(), ratio); if (row.key() === this.detailModel.selected.key()) this.visibilityEpoch++;
      } })
  }
  @Builder inboxList() {
    InboxWorkspacePage({ model: this.model, visible: this.model.section === 2, refreshRevision: this.inboxRefreshRevision, onChanged: () => this.inboxRefreshRevision++, onOpen: (row: ResourceRow) => this.openRow(row), onLink: (address: string) => this.openReader(address),
      onHandoff: (row: ResourceRow) => this.openLinkedResource(row) })
  }
  @Builder phoneSettings() {
    Column() {
      Row() {
        Blank().width(36)
        Text(tr('settings')).layoutWeight(1).textAlign(TextAlign.Center).fontSize(18).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
        PhoneIconButton({symbol:IconActions.symbol('close'),label:tr('close'),identity:'settings.close',tint:Colors.link,
          action:() => { this.phoneSettingsQuery = ''; this.phoneSettingsShown = false; }})
      }.width('100%').padding({left:14,right:14,top:7,bottom:5})
      SettingsDirectory({model:this.model,ownerName:this.model.profile.name || tr('persona'),avatar:this.look.avatarUri,
        initialQuery:this.phoneSettingsQuery,onQueryChange:(value:string) => this.phoneSettingsQuery = value,
        label:(key:string):string => this.settingsLabel(key),onOpen:(key:string) => {
          this.phoneSettingsDepth = this.stack.size(); this.phoneSettingsOwner = this.model.scope();
          this.phoneSettingsShown = false; this.spacesShown = false; this.openSettings(key);
        }}).layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('settings.sheet')
  }
  @Builder settingsList() {
    SettingsDirectory({model:this.model,ownerName: this.model.profile.name || tr('persona'), avatar: this.look.avatarUri,
      label: (key: string): string => this.settingsLabel(key), onOpen: (key: string) => this.openSettings(key)})
  }
  private async worktreeChanged(tree: ProjectWorktree, change: WorktreeChange): Promise<void> {
    const root = this.model, scope = root.scope(), epoch = root.connectionGeneration(), stack = this.stack;
    const names = stack.getAllPathName(), intent = this.navigationIntent;
    const source = new ResourceRow(); source.group = tree.group; source.manager = tree.manager;
    const owner = root.directoryOwnerFor(source);
    const live = (): boolean => root.owns(epoch, scope) && this.model === root;
    const stay = (): boolean => live() && this.stack === stack && this.navigationIntent === intent && this.model.section === 4;
    if (owner && (change.action === 'create' || change.action === 'clean')) {
      try { await this.projectCatalog.saveParent(owner, tree, change.id, change.action === 'create' ? change.path : '', change.parent); }
      catch (_) { /* The host mutation succeeded; its inventory remains authoritative. */ }
    }
    if (!live()) return;
    if (change.action === 'clean' && stay()) {
      const index = names.findIndex((name: string, i: number) => name === 'project_tree' && stack.getParamByIndex(i) === tree.key);
      if (index === 0) stack.clear(); else if (index > 0) stack.popToIndex(index - 1);
      this.savePath();
    }
    this.projectCatalog.notice = tr(change.action === 'create' ? 'tree_created' : change.action === 'rename' ? 'tree_renamed' : change.branchRetained ? 'tree_cleaned_branch' : 'tree_cleaned');
    await this.projectCatalog.refresh(root);
    if (change.action === 'create' && stay()) {
      const key = JSON.stringify([tree.group, tree.manager, tree.machine, tree.source, change.id]);
      if (this.projectCatalog.tree(key)) this.pushPlace('project_tree', key);
    }
  }
  @Builder projectPage(name: string, param: string) {
    ProjectsPage({ model: this.model, catalog: this.projectCatalog, page: name, target: param as string,
          onProject: (key: string) => this.pushPlace('project', key),
          onTree: (key: string) => this.pushPlace('project_tree', key),
          onSession: (row: ResourceRow) => this.openLinkedResource(row),
          onStart: (tree: ProjectWorktree) => this.prepareAgent(undefined, tree),
          onChanged: (tree: ProjectWorktree, change: WorktreeChange) => { this.worktreeChanged(tree, change).catch((err: Error) => this.model.reportError(err)); },
          onRefresh: () => this.projectCatalog.refresh(this.model) })
  }
  @Builder primaryContent() {
    if (this.projectPlace()) {
      ForEach([this.projectRootRevision], (_revision: number) => { this.projectPage('projects', '') }, (revision: number) => String(revision))
    }
    else if (this.peer() === 1) this.agentsList()
    else this.personaList()
  }
  @Builder workspaceSheet() {
    List() {
      ForEach(this.model.profiles, (profile: HubProfile) => { ListItem() { Text(profile.name).padding(Tokens.page) }
        .onClick(() => { this.model.selectProfile(profile); this.showSheet = false; }) }, (profile: HubProfile) => profile.id)
      ForEach(this.model.managers, (manager: ResourceRow) => { ListItem() { Text(manager.title).padding(Tokens.page) }
        .onClick(() => { this.model.selectManager(manager.id); this.showSheet = false; }) }, (manager: ResourceRow) => manager.id)
      ListItem() { Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('connect')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('connect', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('connect')).bindTips(tr('connect')).onClick(() => { this.showSheet = false; this.openSheet('connect'); }) }
    }.width('100%')
  }
  private async scanPairing(): Promise<void> {
    const host = this.getUIContext().getHostContext(); if (!host || this.scanning) return;
    const revision = ++this.scanRevision, generation = this.model.connectionGeneration(), scope = this.model.scope(); this.scanning = true;
    try {
      const result = await scanBarcode.startScanForResult(host, { scanTypes: [scanCore.ScanType.QR_CODE], enableAlbum: true, enableMultiMode: false });
      if (revision !== this.scanRevision || !this.showSheet || this.sheetKind !== 'connect' || !this.model.owns(generation, scope)) return;
      Pairing.parse(result.originalValue); this.address = result.originalValue; this.pairCode = ''; this.model.error = '';
    } catch (error) { if (revision === this.scanRevision && (error as BusinessError).code !== 1000500002) this.model.error = 'connection_failed'; }
    finally { if (revision === this.scanRevision) this.scanning = false; }
  }
  @Builder pairingForm() {
    Column({ space: Tokens.page }) {
      Row({ space: Tokens.gap }) {
        TextInput({ text: this.address, placeholder: tr('hub_address') }).onChange((value: string) => this.address = value).id('pair.address').layoutWeight(1)
        if (canIUse('SystemCapability.Multimedia.Scan.ScanBarcode')) Button({ type: ButtonType.Circle }) {
          SymbolGlyph($r('sys.symbol.qrcode')).fontSize(Tokens.symbolFont).fontColor([Colors.link])
        }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('scan_qr')).enabled(!this.scanning && !this.model.busy).onClick(() => this.scanPairing()).id('pair.scan')
      }.width('100%')
      TextInput({ text: this.pairCode, placeholder: tr('pair_code') }).type(InputType.Password).onChange((value: string) => this.pairCode = value).id('pair.code')
      if (this.model.error) Text(trError(this.model.error)).fontColor($r('app.color.persona_danger'))
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('verify')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('verify', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('verify')).bindTips(tr('verify')).enabled(!!this.address.trim() && (!!this.pairCode.trim() || isPairingAddress(this.address.trim())) && !this.model.busy).onClick(() => this.connect()).id('pair.verify').alignSelf(ItemAlign.End)
    }.padding(Tokens.page).width('100%')
  }
  private createAgentKey(): string {
    if (this.creatingAgent) return 'sending';
    return this.agentModel.operationBlocked('/api/v1/sessions') ? 'result_unknown' : 'create';
  }

  private agentPickerTitle(): string {
    const keys: Record<string,string> = {'workspace':'flow_pick_tree','folder':'directory','launcher':'launcher',
      'model':'flow_model','effort':'flow_effort','preset':'phone_preset'};
    return tr(keys[this.agentPanel] || 'new_agent');
  }
  private effectiveLauncher(): string { return this.launcher || this.defaultLauncherName; }
  private chosenLauncher(): Record<string,Object> {
    return this.launcherData.find((v:Record<string,Object>) => Wire.text(v,'name') === this.effectiveLauncher()) || {};
  }
  private promptOptional(): boolean { return Wire.bool(this.chosenLauncher(),'prompt_optional'); }
  private canStartAgent(): boolean {
    const machine = this.chosenAgentMachine(), tree = this.selectedAgentTree();
    return !!machine && this.agentOwnsMachine(machine) && this.agentMachineOnline(machine) && !this.creatingAgent && !this.switchingAgentMachine &&
      this.agentModel.connected && !this.agentModel.operationBlocked('/api/v1/sessions') &&
      (!!this.newTask.trim() || this.promptOptional()) && (!(this.agentWorktree || this.pickedAgentTree) || !!tree && tree.canStart && tree.cwd === this.directory) &&
      !this.blockedLaunchers.some((v:LaunchChoice) => v.id === this.effectiveLauncher()) &&
      (!this.launchListReady || !this.launcher || this.agentLauncherOptions.includes(this.launcher));
  }
  private pickerOptions(): LaunchChoice[] {
    if (this.agentPanel === 'workspace') return [new LaunchChoice('',tr('flow_choose_folder'))].concat(
      this.availableAgentTrees().filter((t:ProjectWorktree) => t.boundStart).map((t:ProjectWorktree) => new LaunchChoice(t.key,t.title,[t.branch,t.cwd].filter((v:string) => !!v).join(' · '))));
    if (this.agentPanel === 'launcher') return [new LaunchChoice('',tr('phone_launcher_default'),this.machineDefaultLabel)].concat(
      this.agentLauncherOptions.map((value:string,i:number) => new LaunchChoice(value,this.agentLauncherLabels[i] || value,
        Wire.text(this.launcherData.find((v:Record<string,Object>) => Wire.text(v,'name') === value) || {},'agent_type')))).concat(this.blockedLaunchers);
    if (this.agentPanel === 'preset') return [new LaunchChoice('',tr('phone_default_preset'))].concat(
      this.agentPresets.map((v:Record<string,Object>) => new LaunchChoice(Wire.text(v,'name'),Wire.text(v,'name'),
        [Wire.text(v,'launcher'),Wire.text(v,'model'),Wire.text(v,'effort')].filter((label:string) => !!label).join(' · '))));
    if (this.agentPanel === 'model') return [new LaunchChoice('',tr('flow_use_default'))].concat(this.launchChoices.locked ? [] : this.launchChoices.models);
    if (this.agentPanel === 'effort') return [new LaunchChoice('',tr('flow_use_default'))].concat(
      this.launchChoices.effortNames.map((v:string) => new LaunchChoice(v,v)));
    return [];
  }
  private selectedPickerId(): string {
    if (this.agentPanel === 'workspace') return this.selectedAgentTree()?.key || '';
    if (this.agentPanel === 'launcher') return this.launcher;
    if (this.agentPanel === 'preset') return this.agentPreset;
    return this.agentPanel === 'model' ? this.agentModelId : this.agentEffort;
  }
  private openAgentPicker(kind:string): void {
    if (this.switchingAgentMachine || this.creatingAgent || !this.agentOwnsMachine(this.chosenAgentMachine())) return;
    this.startFailure = ''; this.agentPanel = kind;
    if (kind === 'workspace' && !this.projectCatalog.loading) this.projectCatalog.refresh(this.model);
    else if (kind === 'folder') this.browseAgentFolder(this.directory);
    else if (kind === 'model') {
      const m = this.chosenAgentMachine(); if (!m) return;
      const form = this.agentFormRevision, owner = this.agentModel;
      this.launchChoices.loadModels(owner,m.id,this.effectiveLauncher()).then(() => {
        if (form === this.agentFormRevision && owner === this.agentModel && this.chosenAgentMachine()?.key() === m.key())
          this.launchChoices.updateEfforts(this.agentModelId);
      });
    }
  }
  private browseAgentFolder(path:string): void {
    const machine = this.chosenAgentMachine();
    if (machine && this.agentOwnsMachine(machine) && this.agentPanel === 'folder') this.launchChoices.folder(this.agentModel,machine.id,path);
  }
  private chooseAgentOption(value:string): void {
    if (this.agentPanel === 'workspace') {
      const tree = this.availableAgentTrees().find((t:ProjectWorktree) => t.key === value);
      if (value && (!tree || !tree.boundStart)) return;
      this.pickedAgentTree = value; this.directory = tree?.cwd || '';
    } else if (this.agentPanel === 'folder') {
      if (value !== this.launchChoices.path || this.launchChoices.loading) return;
      this.pickedAgentTree = ''; this.directory = value;
    } else if (this.agentPanel === 'launcher') {
      this.launcher = value; this.agentPreset = ''; this.agentModelId = ''; this.agentEffort = ''; this.launchChoices.clear();
    } else if (this.agentPanel === 'preset') {
      const preset = this.agentPresets.find((v:Record<string,Object>) => Wire.text(v,'name') === value);
      this.agentPreset = value;
      if (preset) this.launcher = Wire.text(preset,'launcher');
      this.agentModelId = preset ? Wire.text(preset,'model') : ''; this.agentEffort = preset ? Wire.text(preset,'effort') : '';
      this.launchChoices.clear();
    } else if (this.agentPanel === 'model') {
      if (this.launchChoices.locked && value) return;
      this.agentPreset = ''; this.agentModelId = this.launchChoices.selectedModelId(value); this.launchChoices.updateEfforts(value);
      if (!this.launchChoices.effortNames.includes(this.agentEffort)) this.agentEffort = '';
    } else if (this.agentPanel === 'effort') {
      if (value && !this.launchChoices.effortNames.includes(value)) return;
      this.agentPreset = ''; this.agentEffort = value;
    }
    this.closeAgentPanel(); this.saveAgentDraft();
  }
  private closeAgentPanel(): void {
    if (this.startWorktree.busy) return;
    this.launchChoices.cancelRead(); this.startWorktree.close(); this.startTreeSource = undefined; this.agentPanel = '';
  }
  private makeChildFromAgent(): void {
    const tree = this.selectedAgentTree();
    if (!tree || !tree.permits('create')) return;
    this.startTreeSource = tree; this.agentPanel = 'worktree'; this.startWorktree.open(this.model,tree,'create');
  }
  private async selectNewAgentTree(check:boolean = false, replay:boolean = false): Promise<void> {
    const source = this.startTreeSource, owner = this.agentModel, form = this.agentFormRevision, root = this.model;
    if (!source) return;
    const change = check ? await this.startWorktree.reconcile(replay) : await this.startWorktree.submit();
    if (!change || form !== this.agentFormRevision || this.agentPanel !== 'worktree' || this.startTreeSource !== source) return;
    await this.projectCatalog.saveParent(owner,source,change.id,change.path,change.parent).catch(() => {});
    await this.projectCatalog.refresh(root);
    if (form !== this.agentFormRevision || this.agentPanel !== 'worktree' || this.startTreeSource !== source) return;
    const key = JSON.stringify([source.group,source.manager,source.machine,source.source,change.id]);
    this.pickedAgentTree = key; this.directory = change.path; this.closeAgentPanel(); this.saveAgentDraft();
    if (!this.projectCatalog.tree(key)) this.startFailure = 'project_gone';
  }
  @Builder agentOptionRow(label:string,value:string,symbol:Resource,key:string,disclose:boolean = true) {
    Row({space:11}) {
      SymbolGlyph(symbol).fontSize('18vp').fontColor([Colors.link]).width(24)
      Column({space:2}) {
        Text(label).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        if (value) Text(value).fontSize(13).fontColor(Colors.secondary).maxLines(this.environment.fontScale >= 1.5 ? 4 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
      if (disclose) SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').constraintSize({minHeight:47}).padding({left:13,right:12,top:7,bottom:7})
      .onClick(() => { if (disclose) this.openAgentPicker(key); }).accessibilityGroup(true).id('agent.' + key)
  }
  @Builder launchDivider() { Divider().strokeWidth(0.5).color(Colors.divider).margin({left:48}) }
  @Builder machineChoice(row:ResourceRow) {
    Button({type:ButtonType.Normal}) {
      Column({space:5}) {
        Row() {
          if (machineBrandMark(row)) Image(machineBrandMark(row)!).width(19).height(19).objectFit(ImageFit.Contain).fillColor(Colors.ink)
          else SymbolGlyph(Symbols.device).fontSize('19vp').fontColor([Colors.ink])
          Blank().layoutWeight(1)
          if (this.sameMachine(row,this.chosenAgentMachine())) SymbolGlyph(Symbols.check).fontSize('13vp').fontColor([Colors.link])
        }.width('100%')
        Text(row.title || row.id).fontSize(15).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(this.environment.fontScale >= 1.5 ? 3 : 1).textOverflow({overflow:TextOverflow.Ellipsis})
        Row({space:4}) {
          Circle().width(5).height(5).fill(this.agentMachineOnline(row) ? Colors.positive : Colors.secondary)
          Text(tr(this.agentMachineOnline(row) ? 'online' : 'offline')).fontSize(12).fontColor(Colors.secondary)
        }
      }.width('100%').alignItems(HorizontalAlign.Start).padding(10)
    }.width(this.environment.fontScale >= 1.5 ? Math.min(250,130 * this.environment.fontScale) : 122).padding(0).borderRadius(12).backgroundColor(Colors.surface)
      .border({width:this.sameMachine(row,this.chosenAgentMachine()) ? 1.5 : 0.5,color:this.sameMachine(row,this.chosenAgentMachine()) ? Colors.link : Colors.divider,radius:12})
      .enabled(this.agentMachineOnline(row) && (!this.agentWorktree || this.sameMachine(row,this.chosenAgentMachine())))
      .accessibilityGroup(true).accessibilityText(row.title || row.id).accessibilitySelected(this.sameMachine(row,this.chosenAgentMachine())).onClick(() => this.pickAgentMachine(row)).id('agent.machine')
  }
  @Builder newAgentForm() {
    if (this.agentPanel === 'worktree') {
      WorktreeSheet({form:this.startWorktree,onCancel:() => this.closeAgentPanel(),onRetry:() => this.makeChildFromAgent(),onSubmit:() => this.selectNewAgentTree(),onReconcile:(replay:boolean) => this.selectNewAgentTree(true,replay)})
    } else if (this.agentPanel) {
      AgentStartPicker({kind:this.agentPanel,choices:this.launchChoices,heading:this.agentPickerTitle(),options:this.pickerOptions(),
        selected:this.selectedPickerId(),manual:this.agentPanel === 'launcher' && !this.launchListReady || this.agentPanel === 'model' && this.launchChoices.acceptsTypedModel,
        back:() => this.closeAgentPanel(),choose:(v:string) => this.chooseAgentOption(v),browse:(path:string) => this.browseAgentFolder(path)})
    } else {
      Column() {
        Row() {
          Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(16).maxFontScale(Tokens.navigationTextMaxScale)
            .padding({left:6,right:6}).constraintSize({minHeight:44,maxWidth:'34%'}).fontColor(Colors.link).onClick(() => this.showSheet = false)
            .enabled(!this.creatingAgent).id('agent.cancel')
          Text(tr('new_agent')).fontSize(18).maxFontScale(Tokens.navigationTextMaxScale).maxLines(2).fontWeight(FontWeight.Bold).fontColor(Colors.ink).textAlign(TextAlign.Center).layoutWeight(1)
          Blank().width(48)
        }.width('100%').constraintSize({minHeight:48}).padding({left:4,right:4})
        Scroll() {
          Column() {
            if (!this.agentWorktree && !this.agentTodo && this.recentSetups.length) {
              PhoneSectionTitle({text:tr('flow_recent_setups')})
              Column() { ForEach(this.recentSetups.slice(0,3),(r:RecentAgentSetup) => {
                Row({space:9}) {
                  SymbolGlyph(Symbols.refresh).fontSize('16vp').fontColor([Colors.secondary])
                  Column({space:2}) {
                    Text(this.recentMachineTitle(r)).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
                    Text([r.cwd,r.launcher,r.model,r.effort].filter((v:string) => !!v).join(' · ')).fontSize(12).fontColor(Colors.secondary).maxLines(2)
                  }.layoutWeight(1).alignItems(HorizontalAlign.Start)
                  SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
                }.width('100%').padding(12).onClick(() => this.reuseSetup(r)).accessibilityGroup(true).id('agent.recent')
              },(r:RecentAgentSetup) => r.key) }.width('100%').backgroundColor(Colors.surface).borderRadius(14)
            }
            PhoneSectionTitle({text:tr('machines')})
            Scroll() { Row({space:7}) {
              ForEach(this.agentMachines(),(m:ResourceRow) => this.machineChoice(m),(m:ResourceRow) => JSON.stringify([m.group,m.manager,m.id,m.title,m.state]))
            } }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off).align(Alignment.Start).width('100%')
            PhoneSectionTitle({text:tr('workspace')})
            Column() {
              this.agentOptionRow(tr('workspace'),this.selectedAgentTree()?.title || tr('flow_choose_folder'),Symbols.folder,'workspace',!this.agentWorktree)
              if (this.selectedAgentTree()) {
                Text(this.selectedAgentTree()!.cwd).fontSize(13).fontColor(Colors.secondary).padding({left:48,right:13,bottom:6}).width('100%').id('agent.worktree')
                if (this.selectedAgentTree()!.permits('create')) {
                  this.launchDivider()
                  Button(tr('tree_create'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(15).fontColor(Colors.link).onClick(() => this.makeChildFromAgent()).id('agent.child')
                }
              } else {
                this.launchDivider()
                this.agentOptionRow(tr('directory'),this.directory || Wire.text(this.chosenAgentMachine()?.data() || {},'home') || '~',Symbols.folder,'folder')
              }
            }.width('100%').alignItems(HorizontalAlign.Start).backgroundColor(Colors.surface).borderRadius(14)
            PhoneSectionTitle({text:tr('flow_start_options')})
            Column() {
              if (this.agentPresets.length) {
                this.agentOptionRow(tr('phone_preset'),this.agentPreset || tr('phone_default_preset'),Symbols.apps,'preset')
                this.launchDivider()
              }
              this.agentOptionRow(tr('launcher'),this.launcher || this.machineDefaultLabel || tr('phone_launcher_default'),Symbols.agent,'launcher')
              if (this.effectiveLauncher()) {
                this.launchDivider()
                this.agentOptionRow(tr('flow_model'),this.agentModelId || tr('flow_use_default'),Symbols.apps,'model')
              }
              if (this.launchChoices.effortNames.length || this.agentEffort) {
                this.launchDivider()
                this.agentOptionRow(tr('flow_effort'),this.agentEffort || tr('flow_use_default'),Symbols.gear,'effort')
              }
            }.width('100%').backgroundColor(Colors.surface).borderRadius(14)
            PhoneSectionTitle({text:tr('flow_first_message')})
            TextArea({text:this.newTask,placeholder:tr(this.promptOptional() ? 'flow_draft_optional' : 'task')}).minLines(2).height(Math.max(91, 46 * this.environment.fontScale + 24))
              .fontSize(16).padding(12).fontColor(Colors.ink).backgroundColor(Colors.surface).borderRadius(14)
              .onChange((v:string) => { this.newTask = v; this.saveAgentDraft(); }).id('agent.task')
            if ((this.agentWorktree || this.pickedAgentTree) && !this.selectedAgentTree()) Text(tr('project_gone')).fontSize(14).fontColor(Colors.danger).padding(8)
            if (this.startFailure) Text(trError(this.startFailure)).fontSize(14).fontColor(Colors.danger).padding(8).id('agent.error')
          }.width('100%').alignItems(HorizontalAlign.Start).padding({left:16,right:16,bottom:14})
        }.width('100%').layoutWeight(1).align(Alignment.TopStart)
        Button(tr(this.creatingAgent || this.agentModel.operationBlocked('/api/v1/sessions') ? this.createAgentKey() : 'flow_start')).height(Math.max(46, 23 * this.environment.fontScale + 16))
          .width('91%').margin({top:7,bottom:12}).backgroundColor(Colors.action).fontColor(Colors.onAction).fontSize(17)
          .enabled(this.canStartAgent()).onClick(() => this.createAgent()).id('agent.create')
      }.width('100%').height('100%').backgroundColor(Colors.grouped)
    }
  }
  private async loadAgentLaunchers(): Promise<void> {
    const rev = ++this.launcherRevision, form = this.agentFormRevision, owner = this.agentModel, machine = this.chosenAgentMachine()?.id;
    this.agentLauncherOptions = []; this.agentLauncherLabels = []; this.agentPresets = []; this.machineDefaultLabel = ''; this.launchListReady = false; this.launcherData = []; this.blockedLaunchers = []; this.defaultLauncherName = '';
    if (!machine || !owner.connected) return;
    const scope = owner.scope(), epoch = owner.connectionGeneration();
    try {
      const reply = await owner.repo().call('/api/v1/machines/' + encodeURIComponent(machine) + '/launchers?purpose=agent.new');
      if (rev !== this.launcherRevision || form !== this.agentFormRevision || !owner.owns(epoch,scope) || this.chosenAgentMachine()?.id !== machine) return;
      const standard = Wire.object(reply,'default');
      this.defaultLauncherName = Wire.text(standard,'provider').replace(/^launcher\./,'');
      this.machineDefaultLabel = [Wire.text(standard,'provider').replace(/^launcher\./,''),Wire.text(standard,'model')].filter((v:string) => !!v).join(' · ');
      const allowed = Wire.rows(reply, 'launchers').filter((item: Record<string,Object>) => !!Wire.text(item,'name') && !Object.keys(Wire.object(item,'block')).length);
      this.launcherData = allowed; this.launchListReady = Array.isArray(reply['launchers']);
      const blocked = Wire.rows(reply,'launchers').filter((v:Record<string,Object>) => !!Object.keys(Wire.object(v,'block')).length)
        .concat(Wire.rows(reply,'unavailable'));
      this.blockedLaunchers = blocked.filter((v:Record<string,Object>) => !!Wire.text(v,'name')).map((v:Record<string,Object>) => {
        const reason = Wire.text(Wire.object(v,'block'),'reason') || Wire.text(v,'reason') || tr('offline');
        return new LaunchChoice(Wire.text(v,'name'),Wire.text(v,'display_name') || Wire.text(v,'name'),reason,true);
      });
      this.agentLauncherOptions = allowed.map((item: Record<string,Object>) => Wire.text(item,'name'));
      this.agentPresets = Wire.rows(reply,'tiers').filter((v:Record<string,Object>) => !!Wire.text(v,'name') && this.agentLauncherOptions.includes(Wire.text(v,'launcher')));
      this.agentLauncherLabels = allowed.map((item: Record<string,Object>) => Wire.text(item,'display_name') || Wire.text(item,'name'));
    } catch (_) { /* Older nodes support entering the existing launcher name directly. */ }
  }
  private agentDraftKey(): string { return 'new-agent:' + (this.agentWorktree?.key || this.agentTodo?.id || 'standalone'); }
  private async prepareAgent(todo?: ResourceRow, tree?: ProjectWorktree): Promise<void> {
    const revision = ++this.agentFormRevision, generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.startTreeSource = undefined; this.startWorktree.close(); this.startFailure = ''; this.agentPanel = '';
    this.launchChoices.clear(); this.recentSetups = []; this.agentModelId = ''; this.agentEffort = '';
    this.machineIntent++; this.switchingAgentMachine = false; this.agentMachineKey = ''; this.agentPreset = ''; this.projectCatalog.startError = false; this.readerShown = false; this.creatingAgent = false; this.newTask = ''; this.directory = ''; this.launcher = ''; this.agentTodo = todo; this.agentWorktree = undefined; this.pickedAgentTree = '';
    try {
      let owner = todo ? await this.model.viewFor(todo) : this.model;
      if (tree) {
        const machine = this.model.directoryMachines.find((row: ResourceRow) => row.group === tree.group && row.manager === tree.manager && row.id === tree.machine);
        if (!tree.canStart || !machine) throw new Error('request_updated');
        owner = await this.model.viewFor(machine);
      }
      if (revision !== this.agentFormRevision || !this.model.owns(generation, scope)) return;
      this.agentModel = owner; this.agentWorktree = tree;
      if (tree) {
        const index = this.agentMachines().findIndex((row: ResourceRow) => row.id === tree.machine);
        if (index < 0 || !owner.connected) throw new Error('request_updated');
        this.agentMachineKey = this.machineKey(this.agentMachines()[index]);
      }
      if (!tree && !this.projectCatalog.projects.length && !this.projectCatalog.loading) this.projectCatalog.refresh(this.model);
      const raw = await owner.loadView(this.agentDraftKey());
      if (revision !== this.agentFormRevision || !this.model.owns(generation, scope)) return;
      const saved = Wire.parse(raw); this.newTask = Wire.text(saved, 'task', todo ? todo.title + (Wire.text(todo.data(), 'notes') ? '\n\n' + Wire.text(todo.data(), 'notes') : '') : '');
      if (!tree) {
        const index = this.agentMachines().findIndex((row: ResourceRow) => row.id === Wire.text(saved, 'machine') && (!row.group || row.group === owner.profile.id) && (!row.manager || row.manager === owner.profile.manager));
        if (index >= 0) this.agentMachineKey = this.machineKey(this.agentMachines()[index]);
        this.pickedAgentTree = Wire.text(saved, 'tree');
      }
      this.directory = tree ? tree.cwd : Wire.text(saved, 'directory'); this.launcher = Wire.text(saved, 'launcher'); this.agentPreset = Wire.text(saved,'preset');
      this.agentModelId = Wire.text(saved,'model'); this.agentEffort = Wire.text(saved,'effort');
      if (!this.agentMachineKey) {
        const local = this.agentMachines().filter((m:ResourceRow) => (!m.group || m.group === owner.profile.id) && (!m.manager || m.manager === owner.profile.manager));
        const m = local.find((v:ResourceRow) => v.id === Wire.text(saved,'machine')) || local.find((v:ResourceRow) => this.agentMachineOnline(v));
        if (m) this.agentMachineKey = this.machineKey(m);
      }
      this.sheetKind = 'newagent'; this.showSheet = true;
      const first = this.chosenAgentMachine();
      if (first && first.group && (first.group !== owner.profile.id || first.manager !== owner.profile.manager)) this.pickAgentMachine(first,true);
      else { this.loadAgentLaunchers(); this.loadRecentSetups(); }
    } catch (error) { if (revision === this.agentFormRevision && this.model.owns(generation, scope)) {
        if (tree) this.projectCatalog.startError = true;
        this.model.reportError(error as Error);
      } }
  }
  private saveAgentDraft(): void {
    if (this.sheetKind !== 'newagent' || !this.showSheet) return;
    const owner = this.agentModel, generation = owner.connectionGeneration(), scope = owner.scope();
    owner.saveView(this.agentDraftKey(), JSON.stringify(Wire.payload(['task', 'directory', 'launcher', 'machine', 'tree', 'preset','model','effort'], [this.newTask, this.directory, this.launcher, this.chosenAgentMachine()?.id || '', this.pickedAgentTree,this.agentPreset,this.agentModelId,this.agentEffort])))
      .catch(() => { if (owner.owns(generation, scope)) owner.error = 'cache_unavailable'; });
  }
  private machineKey(row:ResourceRow): string {
    return JSON.stringify([row.group || this.agentModel.profile.id,row.manager || this.agentModel.profile.manager,row.id]);
  }
  private sameMachine(a:ResourceRow,b:ResourceRow|undefined):boolean { return !!b && this.machineKey(a) === this.machineKey(b); }
  private agentOwnsMachine(row:ResourceRow|undefined):boolean {
    return !!row && (!row.group || row.group === this.agentModel.profile.id) && (!row.manager || row.manager === this.agentModel.profile.manager);
  }
  private agentMachineOnline(row:ResourceRow):boolean {
    const data = row.data();
    return row.state !== 'offline' && Wire.bool(data,'online',true) && Wire.text(data,'trust') !== 'pending' && !Wire.bool(data,'pending_confirm') && !Wire.bool(data,'pending');
  }
  private agentMachines():ResourceRow[] {
    const owner = this.agentModel;
    if (this.agentWorktree || this.agentTodo) return owner.machines.filter((v:ResourceRow) => !v.manager || v.manager === owner.profile.manager);
    return this.model.directoryMachines.length ? this.model.directoryMachines : owner.machines;
  }
  private async pickAgentMachine(row:ResourceRow,force:boolean = false):Promise<void> {
    if (!this.agentMachineOnline(row) || !force && this.sameMachine(row,this.chosenAgentMachine()) && this.agentOwnsMachine(row) && this.agentModel.connected || this.agentWorktree && row.id !== this.agentWorktree.machine) return;
    const intent = ++this.machineIntent, form = this.agentFormRevision, root = this.model, epoch = root.connectionGeneration(), scope = root.scope();
    this.agentMachineKey = this.machineKey(row); this.switchingAgentMachine = true; this.startFailure = ''; this.launcherRevision++;
    this.launchChoices.clear(); this.recentSetups = []; this.pickedAgentTree = ''; this.directory = ''; this.launcher = ''; this.agentPreset = ''; this.agentModelId = ''; this.agentEffort = '';
    try {
      const owner = await root.viewFor(row);
      if (intent !== this.machineIntent || form !== this.agentFormRevision || !root.owns(epoch,scope)) return;
      if (!owner.connected) throw new Error('offline');
      this.agentModel = owner; this.saveAgentDraft(); this.loadAgentLaunchers(); this.loadRecentSetups();
    } catch (error) { if (intent === this.machineIntent && form === this.agentFormRevision) this.startFailure = (error as Error).message || 'offline'; }
    finally { if (intent === this.machineIntent && form === this.agentFormRevision) this.switchingAgentMachine = false; }
  }
  private async loadRecentSetups():Promise<void> {
    const owner = this.agentModel, form = this.agentFormRevision, epoch = owner.connectionGeneration(), scope = owner.scope();
    try {
      const list = loadAgentSetups(await owner.loadView('recent-agent-setups'));
      if (form === this.agentFormRevision && owner === this.agentModel && owner.owns(epoch,scope)) this.recentSetups = list;
    } catch (_) { /* Recent entries are optional. */ }
  }
  private recentMachineTitle(setup:RecentAgentSetup):string {
    return this.agentMachines().find((m:ResourceRow) => this.machineKey(m) === setup.machine)?.title || tr('machines');
  }
  private async reuseSetup(setup:RecentAgentSetup):Promise<void> {
    const row = this.agentMachines().find((m:ResourceRow) => this.machineKey(m) === setup.machine);
    if (!row || !this.agentMachineOnline(row)) { this.startFailure = 'offline'; return; }
    await this.pickAgentMachine(row);
    if (!this.sameMachine(row,this.chosenAgentMachine()) || this.startFailure || !this.showSheet) return;
    this.pickedAgentTree = setup.tree; this.directory = setup.cwd; this.launcher = setup.launcher;
    this.agentModelId = setup.model; this.agentEffort = setup.effort; this.agentPreset = setup.preset; this.saveAgentDraft();
  }
  private availableAgentTrees(): ProjectWorktree[] {
    const machine = this.chosenAgentMachine(), owner = this.agentModel, rows: ProjectWorktree[] = [];
    if (!machine || !this.agentOwnsMachine(machine)) return rows;
    for (const project of this.projectCatalog.projects) {
      for (const tree of project.trees) {
        if (tree.canStart && tree.machine === machine.id && tree.group === owner.profile.id && tree.manager === owner.profile.manager) rows.push(tree);
      }
    }
    return rows;
  }
  private selectedAgentTree():ProjectWorktree|undefined {
    if (this.pickedAgentTree) return this.availableAgentTrees().find((tree:ProjectWorktree) => tree.key === this.pickedAgentTree);
    if (!this.agentWorktree) return undefined;
    const current = this.projectCatalog.tree(this.agentWorktree.key);
    return current && current.cwd === this.agentWorktree.cwd ? current : undefined;
  }
  private chosenAgentMachine():ResourceRow|undefined {
    const machines = this.agentMachines();
    if (this.agentMachineKey) return machines.find((row:ResourceRow) => this.machineKey(row) === this.agentMachineKey);
    if (this.agentWorktree) return machines.find((row:ResourceRow) => row.id === this.agentWorktree?.machine);
    return machines.find((row:ResourceRow) => this.agentMachineOnline(row));
  }
  private agentFingerprint(): string { return JSON.stringify([this.agentModel.scope(), this.agentTodo?.id || '', this.chosenAgentMachine()?.id || '', this.newTask, this.directory, this.launcher, this.agentModelId, this.agentEffort, this.agentWorktree?.key || this.pickedAgentTree, this.selectedAgentTree()?.key || '', this.selectedAgentTree()?.cwd || '']); }
  private async createAgent(): Promise<void> {
    const machine = this.chosenAgentMachine(), tree = this.selectedAgentTree(); if (!machine || !this.canStartAgent()) return;
    const owner = this.agentModel, generation = owner.connectionGeneration(), scope = owner.scope(), rootGeneration = this.model.connectionGeneration(), rootScope = this.model.scope(), revision = this.agentFormRevision, fingerprint = this.agentFingerprint(), draftKey = this.agentDraftKey();
    const valid = (): boolean => owner.owns(generation, scope) && this.model.owns(rootGeneration, rootScope) && revision === this.agentFormRevision && this.showSheet && this.sheetKind === 'newagent' && fingerprint === this.agentFingerprint();
    this.creatingAgent = true;
    try {
      const row = await owner.createAgent(this.newTask, machine, this.directory, this.launcher, this.agentTodo?.id || '', valid, tree?.boundStart ? tree.sourceId : '', tree?.boundStart ? tree.source : '', this.agentModelId, this.agentEffort);
      if (!valid()) return;
      const setup = new RecentAgentSetup(); setup.machine = this.machineKey(machine); setup.cwd = this.directory; setup.tree = tree?.key || '';
      setup.launcher = this.launcher; setup.model = this.agentModelId; setup.effort = this.agentEffort; setup.preset = this.agentPreset;
      const updated = rememberAgentSetup(this.recentSetups,setup);
      owner.saveView('recent-agent-setups',JSON.stringify(Wire.payload(['entries'],[updated]))).catch(() => {});
      this.showSheet = false; this.newTask = ''; owner.saveView(draftKey, '').catch(() => {});
      row.group = owner.profile.id; row.manager = owner.profile.manager;
      if (this.projectPlace() || this.agentTodo) this.openLinkedResource(row); else this.openPickedChat(row);
    } catch (error) { if (valid()) this.startFailure = (error as Error).message || 'failed'; }
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
    const keepContent = this.targetReady(name, param);
    if (!keepContent) { this.chatReady = false; this.detailRoute = ''; }
    try {
      let row = descriptor.row; const view = await this.model.viewFor(row);
      if (['session', 'conversation'].includes(row.kind) && !row.machine) {
        let resolved = view.conversations.concat(view.sessions).find((item: ResourceRow) => item.id === row.id || item.session === row.session && !!row.session);
        if (!resolved && view.connected) { await view.refresh(); resolved = view.conversations.concat(view.sessions).find((item: ResourceRow) => item.id === row.id || item.session === row.session && !!row.session); }
        if (!resolved) throw new Error('request_updated'); row = resolved;
      }
      if (revision !== this.routeRevision || this.activeChild !== key || !this.model.owns(generation, scope)) return;
      if (this.detailModel !== view) this.detailModel.hideConversation();
      this.detailModel = view;
      if (row.key() !== view.selected.key()) await view.select(row);
      else if (name === 'session' || name === 'conversation') { if (view.connected) view.showConversation(row); }
      if (revision !== this.routeRevision || this.activeChild !== key || !this.model.owns(generation, scope) || view.selected.key() !== row.key()) return;
      this.markDetailReady(key);
      if (name === 'assistant' || name === 'settings' && descriptor.page === 'voice') view.watchDecisions(true);
    } catch (error) { if (revision === this.routeRevision && this.activeChild === key && this.model.owns(generation, scope)) this.model.reportError(error as Error); }
  }
  private routeShown(name: string, param: Object): void {
    const last = this.stack.size()-1;
    if (last < 0 || this.stack.getAllPathName()[last] !== name || this.stack.getParamByIndex(last) !== param) return;
    this.syncPhonePath();
    if (this.activeChild !== this.childKey(name, param)) this.dismissChatActions();
    this.activeChild = this.childKey(name, param); if (name === 'extension') this.extensionTitle = this.routeRow(param).title; if (name === 'todo') this.todoTitle = this.routeRow(param).title; this.shownName = name; this.shownParam = param as string;
    this.detailModel.watchDecisions(false);
    if (!['session', 'conversation'].includes(name)) this.detailModel.hideConversation(name !== 'tools');
    if (name === 'applications') this.applications.refresh();
    if ((name === 'projects' || name === 'project' || name === 'project_tree') && !this.projectCatalog.projects.length && !this.projectCatalog.loading) this.projectCatalog.refresh(this.model);
    if (name === 'settings' && this.settingsKind(param) === 'appearance') this.ensureAgentWorkspace();
    const descriptor = NavigationContext.from(name, param as string);
    if (descriptor.scoped) this.restoreTarget(name, param);
    else { this.routeRevision++; this.markDetailReady(this.activeChild); if (name === 'resources') this.model.listResources(param as string); }
  }
  @Builder targetLoading(name: string, param: Object) {
    Column({ space: Tokens.gap }) {
      if (this.model.error) {
        Text(trError(this.model.error)).fontSize(Tokens.meta)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.restoreTarget(name, param))
      } else LoadingProgress().width(Tokens.touch).height(Tokens.touch)
    }.width('100%').height('100%').justifyContent(FlexAlign.Center)
  }
  @Builder destinationTitle(name: string, param: Object) {
    if (this.mainDestination(name, param) && !this.wide()) this.peerTitle(true)
    else Text(this.destinationTitleText(name, param)).height(this.headerHeight()).fontSize(Tokens.title).fontWeight(FontWeight.Medium).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
      .padding({ left: this.destinationBackHidden(name, param) ? Tokens.page : 0 })
  }
  private destinationBackHidden(name: string, param: Object): boolean {
    if (this.mainDestination(name, param) || name === 'session' && Wire.bool(this.routeRow(param).data(), 'navigation_root') && this.wide() && this.model.section === 1) return true;
    return this.wide() && this.stack.size() === 1 && this.stack.getAllPathName()[0] === name && this.stack.getParamByIndex(0) === param;
  }
  private destinationTitleText(name: string, param: Object): string {
    if (name === 'machine_agents') return this.routeRow(param).title || tr('phone_machines');
    if (name === 'machine_settings') return tr('phone_machine_detail');
    if (name === 'settings' && this.settingsKind(param) === 'machines') return tr('phone_machines');
    if (name === 'recordings') return tr('knowledge_kind_meetings');
    if (name === 'knowledge_meetings') return tr('knowledge');
    if (name === 'knowledge' && this.routeRow(param).path === 'recordings') return tr('knowledge_kind_recordings');
    if (name === 'projects') return tr('projects');
    if (name === 'project') return this.projectCatalog.projects.find((row: PersonaProject) => row.key === param)?.title || tr('projects');
    if (name === 'project_tree') return this.projectCatalog.tree(param as string)?.title || tr('project_worktree');
    if (name === 'settings_root') return tr('settings');
    if (name === 'assistant') return tr(Wire.text(this.routeRow(param).data(), 'assistant_item_kind') === 'plan' ? 'assistant_feed_plan' : 'assistant_feed_receipt');
    return name === 'session' || name === 'conversation' ? this.chatTitle(param) : name === 'resources' ? tr(param === 'todo' ? 'todos' : param === 'inbox' ? 'inbox' : param === 'health' ? 'health_home_title' : 'knowledge') : name === 'applications' ? tr('applications') : name === 'extension' ? this.extensionTitle : name === 'todo' ? this.todoTitle || this.routeRow(param).title || this.routeRow(param).id : name === 'settings' ? this.settingsTitle(param) : name === 'tools' ? tr('workspace') : name === 'history' ? tr('interaction_history') : this.routeRow(param).title || this.routeRow(param).id;
  }
  @Builder destination(name: string, param: Object) {
    NavDestination() {
      if (name === 'projects' || name === 'project' || name === 'project_tree') {
        this.projectPage(name, param as string)
      } else if (name === 'session' || name === 'conversation') {
        if (this.targetReady(name, param)) Conversation({ searchHitId:this.searchHitFor(param), model: this.detailModel, onPlace: (kind: string) => { if (this.activeChild === this.childKey(name, param)) this.openSpace(kind); }, admission: (): boolean => this.activeChild === this.childKey(name, param) && this.targetReady(name, param), onOpenFile: (path: string) => this.openTools(path), active: this.activeChild === this.childKey(name, param), onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onLink: (address: string) => this.openReader(address), showContextMenu: this.mainDestination(name, param) || this.hideChatTitle(name, param), onOpenCard: (itemId: string, kind: string) => { if (this.activeChild === this.childKey(name, param) && this.targetReady(name, param)) this.openAssistantCard(itemId, kind); }, onPrimaryStop: (shown: boolean) => { if (this.activeChild === this.childKey(name, param) && this.targetReady(name, param)) this.inlineStopIdentity = shown ? JSON.stringify([this.detailModel.scope(), this.detailModel.connectionGeneration(), this.detailModel.target()]) : ''; }, onShowActions: () => this.showChatActions(name, param), actionItems: this.chatMenuActions, actionsVisible: this.chatBodyMenuVisible && this.activeChild === this.childKey(name, param), actionsRequest: this.chatMenuRevision, onHideActions: (request: number) => this.closeChatActions(request) })
        else this.targetLoading(name, param)
      } else if (name === 'history') {
        if (this.targetReady(name, param)) InteractionHistoryPage({ model: this.detailModel, active: this.activeChild === this.childKey(name, param), onLink: (address: string) => this.openReader(address) })
        else this.targetLoading(name, param)
      } else if (name === 'tools') {
        if (this.targetReady(name, param)) SessionTools({ model: this.detailModel, initialTab:Wire.number(Wire.parse(param as string),'entry_tab'), onOpenLink: (address: string) => this.openReader(address), initialFile: Wire.text(Wire.parse(param as string), 'open_file'), onWorkspaceReady: (workspace: SessionWorkspace | undefined) => this.detailModel.setWorkspace(workspace) })
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
      } else if (name === 'machine_agents' || name === 'settings' && this.settingsKind(param) === 'machines') {
        MachineDirectory({model:this.model,settingsScope:this.model.section === 3 || this.phoneSettingsDepth >= 0, selected:name === 'machine_agents' ? this.routeRow(param) : undefined,
          active:this.activeChild === this.childKey(name,param),
          onMachine:(row:ResourceRow) => this.pushPlace('machine_agents',JSON.stringify(row)),
          onDetails:(row:ResourceRow) => this.pushPlace('machine_settings',JSON.stringify(row)),
          onManage:() => this.pushPlace('machine_settings',''),
          onSession:(row:ResourceRow) => this.openFrom(name,param,row)})
      } else if (name === 'machine_settings') {
        if (!this.routeRow(param).id || this.targetReady(name,param)) SettingsPage({model:this.routeRow(param).id ? this.detailModel : this.model, initialMachine:this.routeRow(param).id ? this.routeRow(param) : undefined,
          page:'machines',active:this.activeChild === this.childKey(name,param),onComputer:(row:ResourceRow) => this.openComputer(row),
          onChat:(row:ResourceRow) => this.openFrom(name,param,row)})
        else this.targetLoading(name,param)
      } else if (name === 'computer') {
        if (this.targetReady(name, param)) ComputerScreenPage({ model: this.detailModel, row: this.routeRow(param), active: this.activeChild === this.childKey(name, param), onClose: () => { if (this.activeChild === this.childKey(name, param)) { this.stack.pop(); this.savePath(); } } })
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
      } else if (name === 'knowledge' || name === 'recordings' || name === 'knowledge_meetings' || name === 'resources' && param === 'knowledge') {
        if (name !== 'knowledge' || this.targetReady(name, param)) KnowledgeWorkspacePage({ initialKind: name === 'recordings' ? 'recordings' : name === 'knowledge_meetings' ? 'meetings' : '', onAllMeetings:() => { if (this.activeChild === this.childKey(name,param)) this.pushPlace('knowledge_meetings',''); }, model: name !== 'knowledge' ? this.model : this.detailModel, row: name === 'knowledge' ? this.routeRow(param) : undefined, visible: this.activeChild === this.childKey(name, param),
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
      } else if (name === 'settings' && ['phone_agent_options','phone_language_options'].includes(this.settingsKind(param))) {
        PhoneSettingsOptions({model:this.model,page:this.settingsKind(param),label:(k:string):string => this.settingsLabel(k),onOpen:(k:string) => this.openSettings(k)})
      } else if (name === 'settings' && ['phone_agent_folders', 'phone_claude_config', 'phone_codex_config', 'phone_agy_config', 'phone_agent_followup'].includes(this.settingsKind(param))) {
        PhoneAgentSettingsPage({model:this.settingsModel(param), page:this.settingsKind(param)})
      } else if (name === 'settings' && this.isAdminPage(this.settingsKind(param))) {
        if (!NavigationContext.from(name, param as string).scoped || this.targetReady(name, param)) AdminWorkspacePage({ model: this.settingsModel(param), page: this.settingsKind(param) === 'phone_new_assistant' ? 'managers' : this.settingsKind(param), createManagerOnly:this.settingsKind(param) === 'phone_new_assistant', row: NavigationContext.from(name, param as string).scoped ? NavigationContext.from(name, param as string).row : undefined, active: this.activeChild === this.childKey(name, param), refreshRevision: this.adminRefreshRevision,
          onOpenResource: (row: ResourceRow) => this.openFrom(name, param, row), onOpenMachine: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openSettingsDetail('machines', row); } })
        else this.targetLoading(name, param)
      } else if (name === 'settings' && this.settingsKind(param) === 'ssh') {
        SshSettingsPage({ tunnel: this.settingsModel(param).ssh, active: this.activeChild === this.childKey(name, param),
          onSaved: (): void => { const owner = this.settingsModel(param); if (this.activeChild === this.childKey(name, param)) owner.selectProfile(owner.profile, true); } })
      } else if (name === 'settings') {
        if (!NavigationContext.from(name, param as string).scoped || this.targetReady(name, param)) SettingsPage({ model: ['appearance', 'phone_agent_layout'].includes(this.settingsKind(param)) ? this.model : this.settingsModel(param), agentLayoutOnly:this.settingsKind(param) === 'phone_agent_layout', agentWorkspace: this.agentWorkspace, onChat: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openLinkedResource(row); }, active: this.activeChild === this.childKey(name, param), page: this.settingsKind(param) === 'phone_agent_layout' ? 'appearance' : this.settingsKind(param), onDetail: (page: string, row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openSettingsDetail(page, row); }, onExtension: (extensionId: string, uiId: string, title: string) => { if (this.activeChild === this.childKey(name, param)) this.openExtension(this.settingsModel(param), extensionId, uiId, title); }, onBack: () => { if (this.activeChild === this.childKey(name, param)) { this.stack.pop(); this.savePath(); } }, onComputer: (row: ResourceRow) => { if (this.activeChild === this.childKey(name, param)) this.openComputer(row); }, onOpen: (page: string) => this.openSettings(page, NavigationContext.from(name, param as string).scoped) })
        else this.targetLoading(name, param)
      }
    }.title({ builder: () => this.destinationTitle(name, param), height: this.headerHeight() }, { systemMaterial: this.titleMaterial })
      .hideTitleBar(!this.wide() || this.hideChatTitle(name, param)).hideBackButton(this.destinationBackHidden(name, param))
      .backgroundColor(name === 'resources' && param === 'inbox' ? Colors.grouped : Colors.canvas)
      .menus(this.destinationMenus(name, param))
      .onShown(() => this.routeShown(name, param))
      .onHidden(() => { if (this.pendingInboxHandoff === this.childKey(name, param)) this.pendingInboxHandoff = ''; if (this.activeChild === this.childKey(name, param)) { this.activeChild = ''; this.dismissChatActions(); } if ((name === 'session' || name === 'conversation') && this.routeRow(param).key() === this.detailModel.selected.key()) this.detailModel.hideConversation(false); if (name === 'assistant') this.detailModel.watchDecisions(false); if (name === 'tools') this.detailModel.releaseTerminal(); })
      .onBackPressed((): boolean => { if (!this.spacesShown && this.returnToPhoneSettings()) return true; if (this.spacesShown) { this.spacesShown = false; return true; } if (this.pendingInboxHandoff === this.childKey(name, param)) this.pendingInboxHandoff = ''; this.phoneBack(); return true; })
  }
  onBackPress(): boolean {
    if (this.chatSearchShown) { this.chatSearchShown = false; return true; }
    if (this.phoneSettingsShown) { this.phoneSettingsQuery = ''; this.phoneSettingsShown = false; return true; }
    if (!this.spacesShown) return false;
    this.spacesShown = false; return true;
  }
  build() {
    Stack() {
    WithTheme({ theme: nativeLookTheme(this.look, this.model.style, this.environment.dark), colorMode: this.environment.dark ? ThemeColorMode.DARK : ThemeColorMode.LIGHT }) {
    ThemeScope() {
    Stack({alignContent:Alignment.TopStart}) {
      if (this.model.profile.id && !this.wide() && (this.spacesShown || this.drawerMounted)) {
        Column() { this.spacesContent() }.width(this.drawerWidth()).height('100%').backgroundColor(Colors.canvas)
          .accessibilityLevel(this.spacesShown ? 'auto' : 'no-hide-descendants')
      }
      Stack() {
    Column() {
      if (!this.model.profile.id) {
        Column({ space: 12 }) {
          Image($r('app.media.foreground')).width(72).height(72).objectFit(ImageFit.Contain).borderRadius(18)
          Text(tr('persona')).fontSize(34).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
          Text(tr('phone_welcome_intro')).fontSize(16).fontColor(Colors.secondary).textAlign(TextAlign.Center).margin({bottom:24})
          if (this.model.error) Text(trError(this.model.error)).fontSize(14).fontColor(Colors.danger)
          Button(tr('phone_connect_hub')).width('100%').height(48).fontSize(17).backgroundColor(Colors.action).fontColor(Colors.onAction)
            .onClick(() => this.openSheet('connect')).id('welcome.connect')
          Button(tr('phone_local_demo')).width('100%').height(46).fontSize(16).backgroundColor(Colors.surface).fontColor(Colors.link)
            .onClick(() => this.model.useDemo()).id('welcome.demo')
          if (this.model.pendingCleanupCount > 0) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry_cleanup')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry_cleanup', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry_cleanup')).bindTips(tr('retry_cleanup')).onClick(() => this.model.retryCleanup())
        }.justifyContent(FlexAlign.Center).width('100%').height('100%').padding({left:32,right:32}).backgroundColor(Colors.grouped)
          .bindSheet($$this.showSheet, this.sheetContent(), { title: this.sheetKind === 'newagent' ? undefined : {title:this.sheetTitle()}, height: SheetSize.LARGE, showClose: this.sheetKind !== 'newagent', onWillDismiss:(d:DismissSheetAction) => { if (this.sheetKind === 'newagent' && this.agentPanel) this.closeAgentPanel(); else if (!this.creatingAgent) d.dismiss(); } })
      } else {
        if (!this.wide()) this.compactPlaces()
        Navigation(this.stack) { this.primaryContent() }.title({ builder: this.peerTitle, height: this.headerHeight() }, { systemMaterial: this.titleMaterial }).titleMode(NavigationTitleMode.Mini)
          .hideBackButton(true).hideTitleBar(this.rail() || !this.wide()).menus([])
          .backgroundColor(Colors.canvas)
          .toolbarConfiguration(this.rail() ? [{ value: tr('expand'), icon: $r('sys.media.ohos_ic_public_arrow_right'), action: () => this.collapsed = false }] : [])
          .mode(this.wide() ? NavigationMode.Split : NavigationMode.Stack)
          .navBarWidth(this.rail() ? Tokens.collapsed : Tokens.master).navBarWidthRange(this.rail() ? [Tokens.collapsed, Tokens.collapsed] : [Tokens.masterMin, Tokens.masterMax])
          .minContentWidth(Tokens.detailMin).enableDragBar(!this.rail()).navDestination(this.destination)
          .onAreaChange(() => this.updateBreakpoint()).width('100%').layoutWeight(1)

          .bindSheet($$this.showSheet, this.sheetContent(), { title: this.sheetKind === 'newagent' ? undefined : {title:this.sheetTitle()}, height: SheetSize.LARGE, showClose: this.sheetKind !== 'newagent', onWillDismiss:(d:DismissSheetAction) => { if (this.sheetKind === 'newagent' && this.agentPanel) this.closeAgentPanel(); else if (!this.creatingAgent) d.dismiss(); } })
      }
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
      .accessibilityLevel(this.chatSearchShown || this.spacesShown && !this.wide() ? 'no-hide-descendants' : 'auto')
      if (this.drawerFraction > 0 && !this.wide()) Row().width('100%').height('100%')
        .backgroundColor(this.environment.dark ? '#73000000' : '#33000000')
        .border({width:0.5,color:Colors.divider,radius:55}).opacity(this.drawerFraction)
        .onClick(() => this.spacesShown = false).accessibilityText(tr('close')).id('spaces.dismiss')
      }.width('100%').height('100%').backgroundColor(Colors.canvas)
        .borderRadius(this.drawerFraction > 0 && !this.wide() ? 55 : 0).clip(this.drawerFraction > 0 && !this.wide())
        .shadow({radius:this.drawerFraction > 0 && !this.wide() ? 24 : 0,color:'#2E000000'})
        .translate({x:this.wide() ? 0 : this.drawerFraction * this.drawerWidth()})
      if (this.chatSearchShown) Column() {
        this.phoneSearch()
      }.width('100%').height('100%').backgroundColor(Colors.grouped).zIndex(2).id('shell.all_chats')
    }.width('100%').height('100%').clip(true)
      .onAreaChange((_old:Area,now:Area) => { this.phoneWidth = Number(now.width); this.phoneHeight = Number(now.height); })
      .parallelGesture(PanGesture({fingers:1,direction:PanDirection.Horizontal,distance:12})
        .onActionStart((event:GestureEvent) => this.startPhonePan(event))
        .onActionUpdate((event:GestureEvent) => this.movePhonePan(event.offsetX))
        .onActionEnd((event:GestureEvent) => this.endPhonePan(event.offsetX,event.velocityX))
        .onActionCancel(() => this.endPhonePan(0,0)))
      .bindSheet(this.wide() && this.spacesShown, this.spacesContent(), { title: { title: tr('conversation_spaces') }, height: SheetSize.LARGE, showClose: true,
        onAppear: () => { this.spacesLayoutReady = true; this.measureSpaces(); }, onDisappear: () => { this.spacesLayoutReady = false; if (this.wide()) this.spacesShown = false; } })
    }
    }
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
      .bindSheet($$this.readerShown, this.readerContent(), { title: { title: this.readerTitle }, height: Tokens.readerFull, width: Tokens.readerFull, preferType: SheetType.BOTTOM,
        detents: [Tokens.readerFull, Tokens.readerPartial], enableOutsideInteractive: true, showClose: true })
  }
}
