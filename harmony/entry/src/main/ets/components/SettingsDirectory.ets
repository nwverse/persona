import { nativeEnvironment } from '../platform/NativeEnvironment';
import { inputMethod } from '@kit.IMEKit';
import { i18n } from '@kit.LocalizationKit';
import { PhoneStyle, PhoneSectionTitle, PhoneSettingsRow } from '../design/PhoneStyle';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow, Wire } from '../models/Wire';
import { machineOnline } from '../models/MachineDashboard';

const AGENT_OPTIONS:string[] = ['phone_agent_folders','phone_claude_config','phone_codex_config','phone_agy_config','triage'];
const SEARCH_ROUTES:string[] = ['phone_agent_followup','phone_agent_layout','launchers','engine','schedules'];
const ASSISTANT_OPTIONS:string[] = ['models','assistant','channels','connectors','extensions','memory','phone_agent_options','machines'];
const DEVICE_OPTIONS:string[] = ['appearance','phone_language_options','notifications','translation','phone','privacy'];
const HUB_OPTIONS:string[] = ['managers','ssh','hub_update','node_update','ledger','diagnostics'];
const LANGUAGES:string[] = ['default','zh-CN','en-US','ja-JP'];

function languageName(tag:string):string {
  const locale = tag.toLowerCase();
  if (locale.startsWith('zh')) return '简体中文';
  if (locale.startsWith('ja')) return '日本語';
  if (locale.startsWith('en')) return 'English';
  return tag;
}

function currentLanguage(model:ClientModel):string {
  return model.language === 'default' ? tr('system')+' · '+languageName(i18n.getSystemLanguage()) : languageName(model.language);
}

function settingIcon(id:string):Resource {
  switch (id) {
    case 'models': return $r('sys.symbol.square_stack_3d');
    case 'assistant': return Symbols.persona;
    case 'channels': return Symbols.inbox;
    case 'connectors': return $r('sys.symbol.link');
    case 'extensions': return Symbols.apps;
    case 'memory': return $r('sys.symbol.book');
    case 'phone_agent_folders':
    case 'phone_claude_config':
    case 'phone_codex_config':
    case 'phone_agy_config':
    case 'phone_agent_options':
    case 'triage': return $r('sys.symbol.slider_horizontal_2');
    case 'phone_agent_followup': return Symbols.inbox;
    case 'phone_agent_layout': return Symbols.spaces;
    case 'launchers': return $r('sys.symbol.play_fill');
    case 'engine': return $r('sys.symbol.computer');
    case 'machines': return $r('sys.symbol.desktop');
    case 'appearance': return Symbols.appearance;
    case 'phone_language_options': return $r('sys.symbol.textformat');
    case 'notifications': return $r('sys.symbol.bell');
    case 'translation': return $r('sys.symbol.translate');
    case 'phone': return $r('sys.symbol.phone_doc');
    case 'privacy': return $r('sys.symbol.checkmark_shield');
    case 'managers': return Symbols.agents;
    case 'ssh': return $r('sys.symbol.link');
    case 'ledger': return $r('sys.symbol.doc_plaintext');
    case 'hub_update':
    case 'node_update': return Symbols.refresh;
    case 'diagnostics': return $r('sys.symbol.info_circle');
    case 'schedules': return Symbols.calendar;
    default: return Symbols.gear;
  }
}

function settingLabel(id:string, label:(page:string)=>string):string {
  if (id === 'phone_agent_options') return tr('settings_agent_options_title');
  if (id === 'phone_language_options') return tr('settings_language_options_title');
  if (id === 'machines') return tr('phone_machines');
  if (AGENT_OPTIONS.includes(id) && id!=='triage' || id==='phone_agent_followup' || id==='phone_agent_layout') return tr(id);
  return tr(label(id));
}

const ALIASES:Record<string,string> = {
  'models':'settings_alias_models', 'appearance':'settings_alias_appearance', 'phone_language_options':'settings_alias_language',
  'machines':'settings_alias_machines', 'phone_agent_options':'settings_alias_agents'
};

@ComponentV2
export struct SettingsDirectory {
  @Require @Param model: ClientModel;
  @Param ownerName: string = '';
  @Param avatar: string = '';
  @Param initialQuery: string = '';
  @Local query: string = '';
  @Require @Param label: (page: string) => string;
  @Event onOpen: (page: string) => void = () => {};
  @Event onQueryChange: (query: string) => void = () => {};
  private field: TextInputController = new TextInputController();

  aboutToAppear(): void { this.query = this.initialQuery; }

  @Computed get managers():ResourceRow[] {
    return this.model.listedManagers().filter((r:ResourceRow) => !r.isInstallingManager());
  }
  private currentManager():ResourceRow | undefined {
    return this.managers.find((r:ResourceRow) => r.id === this.model.profile.manager);
  }
  private account():string { return this.currentManager()?.title || this.ownerName || tr('persona'); }
  private subtitle(id:string):string {
    if (id === 'phone_language_options') return currentLanguage(this.model);
    if (id === 'machines') {
      const profile = this.model.profile;
      const roster = this.model.directoryMachines.filter((r:ResourceRow) => r.group === profile.id && (!profile.manager || r.manager === profile.manager));
      const machines = roster.length ? roster : this.model.machines;
      if (!machines.length && !this.model.connected) return '';
      return tr('machine_online_ratio').replace('{online}',String(machines.filter((r:ResourceRow) => machineOnline(r)).length))
        .replace('{total}',String(machines.length));
    }
    return '';
  }
  @Computed get results():string[] {
    const query = this.query.trim().toLowerCase();
    if (!query) return [];
    const words = query.split(/\s+/);
    const direct = ASSISTANT_OPTIONS.concat(SEARCH_ROUTES,DEVICE_OPTIONS,HUB_OPTIONS);
    const agentDestinations = AGENT_OPTIONS.filter((id:string) => id!=='phone_agent_folders');
    return direct.concat(agentDestinations).filter((id:string) => {
      const aliases = ALIASES[id] ? tr(ALIASES[id]) : '';
      const terms = [settingLabel(id,this.label), this.subtitle(id), aliases].join(' ').toLocaleLowerCase();
      return words.every((word:string) => terms.includes(word));
    });
  }
  private openEntry(id:string):void {
    this.field.stopEditing();
    this.onOpen(id);
  }
  @Builder managerMenu(scope:string) {
    Menu() {
      ForEach(this.managers,(m:ResourceRow) => {
        MenuItem({content:m.title || m.id}).selected(m.id === this.model.profile.manager).selectIcon(true)
          .onClick(() => this.model.selectListedManager(m.id,scope))
      },(m:ResourceRow) => m.id)
    }
  }
  @Builder identity() {
    Row({space:9}) {
      Image(this.avatar || $r('app.media.foreground')).width(31).height(31).objectFit(ImageFit.Contain).borderRadius(8)
      if (this.currentManager()) Button({type:ButtonType.Normal}) {
        Row({space:5}) {
          Text(this.account()).fontSize(16).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(1)
          SymbolGlyph(Symbols.down).fontSize('12vp').fontColor([Colors.secondary])
        }
      }.padding(0).constraintSize({minHeight:44}).backgroundColor(Color.Transparent).accessibilityText(tr('settings_this_assistant')+' · '+this.account())
        .bindMenu(this.managerMenu(this.model.scope())).id('settings.assistant_switch')
      else Text(this.account()).fontSize(16).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(1)
    }.width('100%').padding({left:5,right:5,top:13,bottom:0})
  }
  @Builder section(title:string, entries?:string[]) {
    ListItem() {
      Column() {
        PhoneSectionTitle({text:title})
        Column() {
          ForEach(entries ?? this.results,(id:string,ix:number) => {
            PhoneSettingsRow({label:settingLabel(id,this.label),detail:this.subtitle(id),symbol:settingIcon(id),tint:Colors.link,
              identity:'settings.'+id,last:ix+1===(entries ?? this.results).length,action:() => this.openEntry(id)})
          },(id:string,ix:number) => JSON.stringify([id,ix,this.subtitle(id),(entries ?? this.results).length]))
          if (!(entries ?? this.results).length) Text(tr('no_results')).fontSize(16).fontColor(Colors.secondary).width('100%').padding(15).id('settings.no_results')
        }.width('100%').clip(true).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
      }.width('100%')
    }
  }
  build() {
    List() {
      ListItem() {
        Row({space:7}) {
          SymbolGlyph($r('sys.symbol.magnifyingglass')).fontSize('16vp').fontColor([Colors.secondary])
          TextInput({text:this.query,placeholder:tr('phone_settings_search'),controller:this.field}).fontSize(16)
            .height(Math.max(36,Math.ceil(24*nativeEnvironment.fontScale)+8)).padding(0)
            .layoutWeight(1).backgroundColor(Color.Transparent).cancelButton({style:CancelButtonStyle.INPUT})
            .enterKeyType(EnterKeyType.Search).onSubmit(() => this.field.stopEditing())
            .onChange((v:string) => { this.query = v; this.onQueryChange(v); }).id('settings.search')
        }.constraintSize({minHeight:38}).padding({left:12,right:8,top:1,bottom:1}).width('100%').borderRadius(17).backgroundColor(Colors.control)
      }
      if (this.query.trim()) {
        this.section(tr('settings_search_results'))
      } else {
        ListItem() { this.identity() }
        this.section(this.currentManager() ? tr('settings_assistant_named').replace('{name}',this.account()) : tr('phone_settings_assistant'),ASSISTANT_OPTIONS)
        this.section(tr('phone_settings_device'),DEVICE_OPTIONS)
        this.section(tr('phone_settings_hub'),HUB_OPTIONS)
      }
    }.width('100%').height('100%').padding({left:PhoneStyle.page,right:PhoneStyle.page,bottom:24}).backgroundColor(Colors.grouped)
  }
}

@ComponentV2
export struct PhoneSettingsOptions {
  @Require @Param model:ClientModel;
  @Require @Param page:string;
  @Require @Param label:(page:string)=>string;
  @Event onOpen:(page:string)=>void = () => {};
  private mounted:boolean = false;
  aboutToAppear():void { this.mounted = true; }
  aboutToDisappear():void { this.mounted = false; }
  private choose(language:string):void {
    const owner=this.model;
    if (language===owner.language || owner.languageChanging) return;
    const scope=owner.scope(), generation=owner.connectionGeneration();
    owner.setLanguage(language).then(() => {
      if (this.mounted && this.model===owner && owner.owns(generation,scope) && language==='default' && owner.language==='default')
        this.getUIContext().getPromptAction().showToast({message:tr('language_after_restart')});
    }).catch(() => {});
  }
  build() {
    List() {
      if (this.page === 'phone_agent_options') {
        ListItem() {
        Column() {
          ForEach(AGENT_OPTIONS,(id:string,ix:number) => {
            PhoneSettingsRow({label:settingLabel(id,this.label),symbol:settingIcon(id),tint:Colors.link,
              identity:'settings.'+id,last:ix === AGENT_OPTIONS.length-1,action:() => this.onOpen(id)})
          },(id:string) => id)
        }.width('100%').clip(true).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        }
        ListItem() { Text(tr('phone_agent_tools_note')).fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:7,right:7,top:7,bottom:10}) }
      } else {
        ListItem() {
        Column() {
          ForEach(LANGUAGES,(lang:string,ix:number) => {
            Row({space:7}) {
              if (lang==='default' && nativeEnvironment.fontScale>=1.4) Column({space:2}) {
                Text(tr('system')).fontSize(17).fontColor(Colors.ink)
                Text(languageName(i18n.getSystemLanguage())).fontSize(15).fontColor(Colors.secondary)
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              else Text(lang==='default' ? tr('system')+' · '+languageName(i18n.getSystemLanguage()) : languageName(lang))
                .fontSize(17).fontColor(Colors.ink).layoutWeight(1)
              if (this.model.language===lang) SymbolGlyph(Symbols.check).fontSize('17vp').width(21).flexShrink(0).fontColor([Colors.link])
            }.width('100%').constraintSize({minHeight:46}).padding({left:15,right:14,top:6,bottom:6})
              .enabled(!this.model.languageChanging).accessibilitySelected(this.model.language===lang).accessibilityGroup(true)
              .onClick(() => this.choose(lang)).id('settings.language.'+lang)
            if (ix<LANGUAGES.length-1) Divider().strokeWidth(0.5).color(Colors.divider).margin({left:15})
          },(lang:string) => lang)
        }.width('100%').clip(true).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        }
        ListItem() { Text(tr('phone_language_system_note')).fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:7,right:7,top:7,bottom:20}) }
        ListItem() {
          Row({space:8}) {
            if (nativeEnvironment.fontScale>=1.4) Column({space:2}) {
              Text(tr('phone_dictation')).fontSize(16).fontColor(Colors.ink)
              Text(tr('phone_system_input')).fontSize(15).fontColor(Colors.link)
            }.layoutWeight(1).alignItems(HorizontalAlign.Start)
            else {
              Text(tr('phone_dictation')).fontSize(16).fontColor(Colors.ink).layoutWeight(1)
              Text(tr('phone_system_input')).fontSize(15).fontColor(Colors.link)
            }
            SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
          }.constraintSize({minHeight:48}).width('100%').padding({left:15,right:15,top:7,bottom:7})
            .backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).accessibilityGroup(true)
            .onClick(() => inputMethod.getSetting().showOptionalInputMethods().catch(() => {
              if (this.mounted) this.getUIContext().getPromptAction().showToast({message:tr('failed')});
            })).id('settings.dictation.system')
        }
        ListItem() { Text(tr('phone_dictation_os_note')).fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:7,right:7,top:7,bottom:10}) }
      }
    }.width('100%').height('100%').padding({left:16,right:16,top:12,bottom:24}).backgroundColor(Colors.grouped)
  }
}
