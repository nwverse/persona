import { nativeEnvironment } from '../platform/NativeEnvironment';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow } from '../models/Wire';
import { PhoneSearchIndex, PhoneChatMatch, localChatMatches, mergeChatMatches } from '../models/PhoneChatSearch';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';

@ComponentV2
export struct PhoneConversationSearch {
  @Require @Param model: ClientModel;
  @Param initialAgents: boolean = false;
  @Param inline: boolean = false;
  @Param showScopes: boolean = true;
  @Event onCancel: () => void = () => {};
  @Event onOpen: (row: ResourceRow,hit:string) => void = () => {};
  @Local query: string = '';
  @Local agents: boolean = false;
  @Local search: PhoneSearchIndex = new PhoneSearchIndex();
  private field: TextInputController = new TextInputController();
  aboutToAppear(): void { this.agents = this.initialAgents; }
  aboutToDisappear(): void { this.search.close(); }
  @Monitor('model.profile') newOwner(): void { this.query = ''; this.search.close(); }
  @Monitor('query','agents','model.connected') update(): void { this.search.schedule(this.model, this.query, this.agents); }
  @Computed get rows(): PhoneChatMatch[] {
    const rows = this.agents ? this.model.directorySessions : this.model.conversations;
    return mergeChatMatches(localChatMatches(rows,this.query,this.agents),this.agents ? [] : this.search.matches);
  }
  private main(hit:PhoneChatMatch):boolean {
    return !this.agents && !!this.model.mainId && hit.row.id===this.model.mainId &&
      (!this.model.mainMachineId || hit.row.machine===this.model.mainMachineId);
  }
  @Builder fieldBar() {
    Row({space:8}) {
      Row({space:5}) {
        SymbolGlyph($r('sys.symbol.magnifyingglass')).fontSize('16vp').fontColor([Colors.secondary])
        TextInput({text:this.query,placeholder:tr(this.agents ? 'search_sessions' : 'flow_search_chats'),controller:this.field})
          .height(Math.max(38,Math.ceil(24*nativeEnvironment.fontScale)+8)).padding({left:2,right:1}).layoutWeight(1).fontSize(16).fontColor(Colors.ink)
          .backgroundColor(Color.Transparent).cancelButton({style:CancelButtonStyle.INPUT})
          .onChange((v:string) => this.query = v).enterKeyType(EnterKeyType.Search).defaultFocus(true).id('phone_search.field')
      }.layoutWeight(1).alignItems(VerticalAlign.Center).padding({left:10,right:5}).backgroundColor(Colors.control).borderRadius(12)
      Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).height(Math.max(38,Math.ceil(22*nativeEnvironment.fontScale)+10)).fontSize(15).fontColor(Colors.link).padding({left:3,right:2})
        .onClick(() => this.onCancel()).id('phone_search.cancel')
    }.width('100%').padding({left:12,right:12,top:6,bottom:6})
  }
  @Builder scopeButton(isAgent:boolean,label:string) {
    Button(tr(label),{type:ButtonType.Capsule}).layoutWeight(1).height(Math.max(31,Math.ceil(21*nativeEnvironment.fontScale)+8)).fontSize(14)
      .fontWeight(this.agents === isAgent ? FontWeight.Bold : FontWeight.Normal)
      .fontColor(this.agents === isAgent ? Colors.ink : Colors.secondary)
      .backgroundColor(this.agents === isAgent ? Colors.surface : Color.Transparent)
      .accessibilitySelected(this.agents === isAgent).onClick(() => this.agents = isAgent).id(isAgent ? 'phone_search.agents' : 'phone_search.chats')
  }
  build() {
    Column() {
      if (this.inline) this.fieldBar()
      else Row() {
        Text(tr('flow_all_chats')).fontSize(29).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
      }.width('100%').padding({left:16,right:16,top:12,bottom:8})
      if (this.showScopes) Row() { this.scopeButton(false,'persona'); this.scopeButton(true,'agents') }
        .constraintSize({minHeight:35}).padding(2).margin({left:12,right:12,top:3,bottom:5}).width('92%').backgroundColor(Colors.control).borderRadius(18)
      List() {
        if (this.rows.length && this.query.trim()) ListItem() {
          Text(tr(this.agents ? 'project_agent_count' : 'phone_search_conversation_count').replace('{count}',String(this.rows.length)))
            .fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:15,right:12,top:7,bottom:5})
        }
        if (this.search.busy) ListItem() { LoadingProgress().width(23).height(23).margin(10) }
        if (!this.rows.length && !this.search.busy) ListItem() { Text(tr(this.query ? 'no_results' : 'empty')).fontSize(15).fontColor(Colors.secondary).padding(16) }
        ForEach(this.rows,(hit:PhoneChatMatch) => {
          ListItem() {
            Row({space:10}) {
              SymbolGlyph(this.agents ? Symbols.agent : this.main(hit) ? $r('sys.symbol.house') : Symbols.persona).fontSize('19vp').fontColor([Colors.secondary])
              Column({space:3}) {
                Text(this.main(hit) ? tr('main_chat') : hit.row.title || tr('persona')).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
                if (hit.subtitle) Text(hit.subtitle).fontSize(14).fontColor(Colors.secondary).maxLines(this.inline ? 1 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
              }.alignItems(HorizontalAlign.Start).layoutWeight(1)
              if (!this.inline) SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.width('100%').constraintSize({minHeight:48}).padding({left:13,right:12,top:8,bottom:8})
              .backgroundColor(this.inline ? Colors.subtle : Colors.surface).borderRadius(this.inline ? 12 : 0)
          }.onClick(() => this.onOpen(hit.row,hit.hitId)).accessibilityGroup(true).id('phone_search.result')
        },(hit:PhoneChatMatch) => JSON.stringify([hit.row.key(),hit.row.title,hit.subtitle]))
        if (this.search.partial || this.search.failed) ListItem() {
          Text(tr(this.search.failed ? 'flow_search_local' : 'flow_search_partial')).fontSize(13).fontColor(Colors.secondary).padding(12)
        }
      }.layoutWeight(1).width('100%').divider({strokeWidth:0.5,startMargin:43,endMargin:12,color:Colors.divider})
      if (!this.inline) this.fieldBar()
    }.width('100%').height('100%').backgroundColor(this.inline ? Colors.canvas : Colors.grouped).id('phone_search.results')
  }
}
