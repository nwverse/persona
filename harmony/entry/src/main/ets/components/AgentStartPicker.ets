import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Tokens } from '../design/Tokens';
import { AgentLaunchChoices, LaunchChoice } from '../models/AgentLaunchChoices';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';

@ComponentV2
export struct AgentStartPicker {
  @Require @Param kind:string;
  @Require @Param choices:AgentLaunchChoices;
  @Param options: LaunchChoice[] = [];
  @Param selected:string = '';
  @Param heading:string = '';
  @Param manual:boolean = false;
  @Local query:string = '';
  @Local address:string = '';
  private searchField:SearchController = new SearchController();
  @Event back:() => void = () => {};
  @Event choose:(id:string) => void = () => {};
  @Event browse:(path:string) => void = () => {};
  aboutToAppear():void { this.address = this.choices.path; }
  @Monitor('choices.path') resolved():void { if (this.choices.path) this.address = this.choices.path; }
  @Computed get filtered():LaunchChoice[] {
    const term = this.query.trim().toLowerCase();
    return this.options.filter((o:LaunchChoice) => !term || [o.title,o.detail,o.description,o.id,
      this.kind === 'model' && !o.id ? this.choices.defaultModelName() : ''].join(' ').toLowerCase().includes(term));
  }
  private folderName():string {
    const path=this.choices.path.replace(/\/+$/,'');
    return path.split('/').pop() || this.choices.path;
  }
  @Builder divider(inset:number) { Divider().strokeWidth(0.5).color(Colors.divider).margin({left:inset}) }
  @Builder item(o:LaunchChoice,folder:boolean) {
    Row({space:11}) {
      if (this.kind !== 'model') SymbolGlyph(folder || this.kind === 'workspace' ? Symbols.folder : this.kind === 'launcher' ? Symbols.agent : Symbols.apps)
        .fontSize('18vp').fontColor([folder && o.detail !== 'Git' ? Colors.secondary : Colors.link])
      Column({space:3}) {
        Text(o.title).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2)
        if (o.detail || this.kind === 'model' && !o.id && this.choices.defaultModelName()) Text(o.detail || this.choices.defaultModelName())
          .fontSize(13).fontColor(Colors.secondary).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
        if (o.description) Text(o.description).fontSize(13).fontColor(Colors.secondary).maxLines(nativeEnvironment.fontScale >= 1.5 ? 5 : 3).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (folder || o.id === this.selected) SymbolGlyph(folder ? Symbols.expand : Symbols.check).fontSize('15vp').fontColor([folder ? Colors.secondary : Colors.link])
    }.width('100%').constraintSize({minHeight:48}).padding({left:14,right:13,top:8,bottom:8})
      .backgroundColor(Colors.surface).borderRadius(folder || this.kind === 'model' ? 0 : 10)
      .enabled(!o.disabled).opacity(o.disabled ? 0.65 : 1).onClick(() => { if (folder) this.browse(o.id); else this.choose(o.id); })
      .accessibilityGroup(true).accessibilitySelected(!folder && o.id === this.selected).id(folder ? 'agent.folder.item' : 'agent.option')
  }
  @Builder pickerSearch() {
    Search({value:this.query,placeholder:this.kind === 'model' && this.manual ? tr('search')+' · '+tr('model_id') :
      tr(this.kind === 'workspace' ? 'flow_workspace_search' : 'search'),controller:this.searchField}).searchIcon({size:'16vp'}).height(Math.max(42, 22 * nativeEnvironment.fontScale + 12))
      .onChange((v:string) => this.query = v).margin({left:14,right:14,bottom:8}).id('agent.picker.search')
  }
  @Builder chooseFolder() {
    Column() {
      Row({space:6}) {
        TextInput({text:this.address,placeholder:tr('flow_folder_search')}).layoutWeight(1).height(Math.max(46, 22 * nativeEnvironment.fontScale + 14)).fontSize(15)
          .fontFamily('monospace').backgroundColor(Color.Transparent).padding(0)
          .onChange((v:string) => this.address = v).onSubmit(() => this.browse(this.address)).id('agent.folder.address')
        if (this.address.trim() && this.address.trim() !== this.choices.path) Button(tr('open'),{buttonStyle:ButtonStyleMode.TEXTUAL})
          .fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).onClick(() => this.browse(this.address)).id('agent.folder.go')
      }.width('100%').padding({left:14,right:8})
      this.divider(14)
      Button({type:ButtonType.Normal}) {
        Row({space:8}) {
          SymbolGlyph(Symbols.check).fontSize('17vp').fontColor([Colors.link])
          Text(this.folderName() ? tr('flow_use_named_folder').replace('{name}',this.folderName()) : tr('flow_use_folder'))
            .fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.link).layoutWeight(1).textAlign(TextAlign.Start)
        }.width('100%').constraintSize({minHeight:49}).padding({top:7,bottom:7})
      }.width('100%').padding({left:14,right:14}).backgroundColor(Color.Transparent)
        .enabled(!this.choices.loading && !!this.choices.path && !this.choices.error)
        .onClick(() => this.choose(this.choices.path)).id('agent.folder.use')
    }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).clip(true)
  }
  private groupItems(group:string):LaunchChoice[] {
    if (group === 'folder') return this.choices.directories;
    if (group === 'recent') return this.choices.recentDirectories.slice(0,6);
    return this.filtered.filter((o:LaunchChoice) => group === 'default' ? !o.id : !!o.id);
  }
  @Builder optionGroup(group:string,folder:boolean) {
    Column() {
      ForEach(this.groupItems(group),(o:LaunchChoice,i:number) => {
        if (i) this.divider(folder ? 41 : 14)
        this.item(o,folder)
      },(o:LaunchChoice,i:number) => JSON.stringify([i,o.id,o.title,o.detail,o.description,o.disabled]))
    }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).clip(true)
  }
  build() {
    Column() {
      Row({space:9}) {
        Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.collapse).fontSize('18vp').fontColor([Colors.link]) }
          .width(36).height(36).padding(0).backgroundColor(Colors.surface).responseRegion({x:-4,y:-4,width:44,height:44}).accessibilityText(tr('back')).onClick(() => this.back()).id('agent.picker.back')
        Text(this.kind === 'folder' && this.folderName() ? this.folderName() : this.heading).layoutWeight(1).textAlign(TextAlign.Center)
          .fontSize(18).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
        Blank().width(36)
      }.width('100%').padding({left:14,right:14,top:7,bottom:8})
      if (this.kind !== 'folder' && this.kind !== 'model' && (this.kind === 'workspace' || this.options.length > 5 || this.manual)) this.pickerSearch()
      List({space:this.kind === 'folder' || this.kind === 'model' ? 12 : 3}) {
        if (this.kind === 'folder') ListItem() { this.chooseFolder() }
        if (this.choices.loading && (this.kind === 'folder' || this.kind === 'model'))
          ListItem() { LoadingProgress().width(25).height(25).margin(12) }
        if (this.choices.error && (this.kind === 'folder' || this.kind === 'model')) ListItem() {
          Text(trError(this.choices.error)).fontSize(14).fontColor(Colors.secondary).padding(12)
        }
        if (this.kind === 'folder') {
          if (this.choices.parent && this.choices.parent !== this.choices.path) ListItem() {
            Button(tr('session_tools_parent_folder'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).constraintSize({minHeight:44}).onClick(() => this.browse(this.choices.parent)).id('agent.folder.up')
          }
          if (this.choices.directories.length) ListItem() { this.optionGroup('folder',true) }
          if (this.choices.path && !this.choices.directories.length) ListItem() { Text(tr('flow_folder_empty')).fontSize(14).fontColor(Colors.secondary).padding(14) }
          if (this.choices.recentDirectories.length) ListItem() {
            Column() { PhoneSectionTitle({text:tr('flow_recent_folders')}); this.optionGroup('recent',true) }
          }
        } else {
          if (this.choices.locked && this.kind === 'model') ListItem() { Text(this.choices.locked).fontSize(14).fontColor(Colors.secondary).padding(12) }
          if (this.kind === 'model') {
            if (this.filtered.some((o:LaunchChoice) => !o.id)) ListItem() { this.optionGroup('default',false) }
            if (this.filtered.some((o:LaunchChoice) => !!o.id)) ListItem() { this.optionGroup('models',false) }
          } else ForEach(this.filtered,(o:LaunchChoice) => { ListItem() { this.item(o,false) } },(o:LaunchChoice) => JSON.stringify([o.id,o.title,o.detail,o.disabled]))
          if (this.kind === 'model' && this.manual && !this.query.trim() && !this.choices.locked) ListItem() {
            Button(tr('flow_other_model'),{type:ButtonType.Normal}).fontSize(16).fontColor(Colors.link)
              .width('100%').height(Math.max(48, 24 * nativeEnvironment.fontScale + 14)).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
              .onClick(() => this.getUIContext().getFocusController().requestFocus('agent.picker.search')).id('agent.model.other')
          }
          if (this.manual && this.query.trim() && !this.options.some((o:LaunchChoice) => o.id === this.query.trim()) && !this.choices.locked) ListItem() {
            this.item(new LaunchChoice(this.query.trim(),this.query.trim()),false)
          }
          if (!this.filtered.length && !this.manual && !this.choices.loading) ListItem() { Text(tr('no_results')).fontSize(14).fontColor(Colors.secondary).padding(12) }
        }
      }.width('100%').layoutWeight(1).padding({left:14,right:14,top:7,bottom:10})
      if (this.kind === 'model' && (this.options.length > 5 || this.manual)) this.pickerSearch()
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('agent.picker')
  }
}
