import { ClientModel } from '../models/ClientModel';
import { SessionRelations } from '../models/SessionRelations';
import { ResourceRow } from '../models/Wire';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { Symbols } from '../design/Symbols';
import { ToolGlyph } from '../design/ToolGlyph';
import { tr } from '../design/Strings';

@ComponentV2
export struct SessionLinks {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Event navigate: (row: ResourceRow) => void = () => {};
  @Local sheet: boolean = false;
  private sheetOwner: string = '';

  @Computed get graph(): SessionRelations { return this.model.sessionFamily(this.subject); }
  @Computed get subject(): ResourceRow {
    const row = this.model.selected;
    return row.group ? row : row.copy(this.model.profile.id);
  }
  @Computed get parent(): ResourceRow | undefined { return this.graph.root(this.subject); }
  @Computed get children(): ResourceRow[] { return this.graph.children(this.subject); }
  @Computed get needs(): number { return this.children.filter(child => child.state === 'needs_you').length; }
  private owner(): string { return JSON.stringify([this.model.connectionGeneration(),this.model.target()]); }
  @Monitor('active','model.selected') changed(): void { if (!this.active || this.sheetOwner !== this.owner()) this.sheet = false; }

  private pick(row: ResourceRow): void {
    if (!this.active || !this.sheet || this.sheetOwner !== this.owner()) return;
    const live = this.children.find(child => child.key() === row.key());
    this.sheet = false;
    if (live) this.navigate(live);
  }

  @Builder section(label: string, entries: ResourceRow[]) {
    if (entries.length) {
      Text(tr(label) + '  ' + entries.length).fontSize(Tokens.caption).fontColor(Colors.secondary)
        .width('100%').margin({top:Tokens.inset,bottom:Tokens.fine})
      ForEach(entries,(child: ResourceRow) => {
        Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
          Row({space:Tokens.gap}) {
            if (label === 'relation_task') {
              ToolGlyph({name:'agent', glyphSize:Tokens.body, ink:Colors.teal})
            } else {
              SymbolGlyph(Symbols.persona).fontSize(Tokens.body).fontColor([Colors.reference])
            }
            Column({space:Tokens.fine}) {
              Text(child.title || child.id).fontSize(Tokens.body).fontColor(Colors.ink).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
              if (child.state) Text(tr(child.state)).fontSize(Tokens.caption).fontColor(child.state === 'needs_you' ? Colors.needs : Colors.secondary)
            }.alignItems(HorizontalAlign.Start).layoutWeight(1)
            SymbolGlyph(Symbols.expand).fontSize(Tokens.meta).fontColor([Colors.secondary])
          }.width('100%').padding({top:Tokens.gap,bottom:Tokens.gap})
        }.backgroundColor(Color.Transparent).width('100%').constraintSize({minHeight:Tokens.touch}).padding(0)
          .onClick(() => this.pick(child)).id('chat.related.item.' + child.key())
      },(child: ResourceRow) => JSON.stringify([child.key(), child.title, child.state]))
    }
  }
  @Builder list() {
    Scroll() {
      Column() {
        this.section('relation_side',this.children.filter(row => this.graph.kind(row) === 'side'))
        this.section('relation_task',this.children.filter(row => this.graph.kind(row) === 'subtask'))
      }.width('100%').padding({left:Tokens.page,right:Tokens.page,bottom:Tokens.section})
    }.width('100%').align(Alignment.Top)
  }
  build() {
    if (this.parent || this.children.length) {
      Row() {
        if (this.parent) {
          Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
            Row({space:Tokens.gap}) {
              SymbolGlyph(Symbols.collapse).fontSize(Tokens.meta).fontColor([Colors.reference])
              Text(tr('relation_root') + ' · ' + (this.parent!.title || this.parent!.id)).fontSize(Tokens.meta).fontColor(Colors.reference)
                .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
            }.width('100%')
          }.width('100%').backgroundColor(Color.Transparent).padding(0)
            .onClick(() => { if (this.active && this.parent) this.navigate(this.parent!); }).id('chat.related.parent')
        } else {
          Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
            Row({space:Tokens.gap}) {
              if (this.children.every(row => this.graph.kind(row) === 'subtask')) {
                ToolGlyph({name:'agent', glyphSize:Tokens.body, ink:Colors.teal})
              } else {
                SymbolGlyph(Symbols.agents).fontSize(Tokens.body).fontColor([Colors.secondary])
              }
              Text(tr('relation_chats') + '  ' + this.children.length).fontSize(Tokens.meta).fontColor(Colors.ink)
              if (this.needs) Text(tr('relation_needs_count').replace('{n}',String(this.needs))).fontSize(Tokens.caption).fontColor(Colors.needs)
              SymbolGlyph(Symbols.down).fontSize(Tokens.caption).fontColor([Colors.secondary])
            }
          }.backgroundColor(Color.Transparent).padding(0).onClick(() => { this.sheetOwner = this.owner(); this.sheet = this.active; }).id('chat.related.open')
        }
      }.width('100%').padding({left:Tokens.page,right:Tokens.page}).alignItems(VerticalAlign.Center)
        .bindSheet($$this.sheet,this.list(),{title:{title:tr('relation_chats')},height:SheetSize.MEDIUM,showClose:true})
    }
  }
}
