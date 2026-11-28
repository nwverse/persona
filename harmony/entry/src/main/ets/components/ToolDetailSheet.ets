import { pasteboard } from '@kit.BasicServicesKit';
import type { ToolInspectionStep, TranscriptItem } from '../core/Domain';
import { accessibleToolState, activityKind, toolMotion, hasWorkArguments, inspectTool, InspectedTool, ToolInspectorView, workMetrics, workStatusFailed, workToolArguments, WorkDetail } from '../core/WorkPresentation';
import { agentEventDisplay, eventClock, recordedDuration } from '../core/AgentEvents';
import { BoundRow } from '../models/RowBinding';
import { Colors } from '../design/Colors';
import { ToolGlyph } from '../design/ToolGlyph';
import type { ToolGlyphName } from '../design/ToolGlyphPaths';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ToolDetailContent } from './ToolDetailContent';
import { AgentEventLine } from './AgentEventLine';

@ComponentV2
export struct ToolDetailSheet {
  @Require @Param selection: BoundRow<InspectedTool>;
  @BuilderParam images: (value: ToolInspectionStep) => void;
  @Event onClose: () => void = () => {};
  @Local input: boolean = false;
  @Local wrap: boolean = true;
  private scroller: Scroller = new Scroller();
  @Computed get step(): ToolInspectionStep { return this.selection.value.step; }
  @Computed get view(): ToolInspectorView { return inspectTool(this.selection.value, tr); }
  @Computed get failed(): boolean { return workStatusFailed(this.view.status); }
  @Computed get metrics(): string { return workMetrics(this.step, tr, recordedDuration); }
  @Computed get when(): string { return eventClock(this.step.use.ts); }
  @Computed get raw(): string { return this.input ? workToolArguments(this.step.use) : ''; }
  @Computed get sections(): WorkDetail[] {
    return this.input ? [{key:'work_arguments',style:'code',text:this.raw}] : this.view.fields;
  }
  @Computed get reports(): TranscriptItem[] {
    return this.step.results.filter((r: TranscriptItem) => !!r.task).concat(this.step.reports).sort((a: TranscriptItem,b: TranscriptItem) => a.seq-b.seq);
  }
  @Computed get empty(): string {
    if (this.input || this.view.status === 'working' || this.view.hasOutput || this.step.images.length || this.reports.length || this.view.fields.some(f =>
      f.style === 'diff' || ['ev_answers','ev_git','ev_proposed_content','ev_output_file'].includes(f.key))) return '';
    return tr(this.step.result ? 'tool_inspect_empty' : 'work_no_result');
  }
  @Computed get copyTargets(): WorkDetail[] {
    return this.sections.filter(field => field.key !== 'ev_output_excerpt' && field.key !== 'ev_patch_cut');
  }
  private copyLabel(field: WorkDetail): string {
    return tr('tool_inspect_copy').replace('{part}', tr(field.key));
  }
  private async copy(text: string): Promise<void> {
    try {
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,text));
      this.getUIContext().getPromptAction().showToast({message:tr('ev_code_copied')});
    } catch (_) { this.getUIContext().getPromptAction().showToast({message:tr('ev_code_copy_failed')}); }
  }
  private caption(field: WorkDetail): string {
    if (this.input) return '';
    if (field.key === 'work_output') {
      if (this.failed) return tr('tool_inspect_error');
      return this.view.fields[0]?.key === field.key ? '' : tr(field.key);
    }
    if (field.key === 'ev_patch' && this.view.tab === 'tool_inspect_changes') return '';
    return tr(field.key);
  }
  private tabLabel(input: boolean): string { return tr(input ? 'tool_inspect_input' : this.view.tab); }
  @Builder tab(input: boolean) {
    Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
      Text(this.tabLabel(input)).fontSize(14).fontWeight(this.input === input ? FontWeight.Medium : FontWeight.Normal)
        .fontColor(this.input === input ? Colors.ink : Colors.activity).lineHeight('21fp').padding({top:10,bottom:9})
        .border({width:{bottom:2},color:this.input === input ? Colors.ink : Color.Transparent})
    }.padding(0).backgroundColor(Color.Transparent).accessibilityText(this.tabLabel(input)).accessibilitySelected(this.input === input)
      .onClick(() => { if (this.input !== input) { this.input = input; this.scroller.scrollTo({xOffset:0,yOffset:0}); } })
      .id(input ? 'chat.tool.input' : 'chat.tool.result')
  }
  private actionLabel(name: ToolGlyphName): string {
    if (name === 'wrap') return tr(this.wrap ? 'ev_no_wrap' : 'ev_wrap');
    if (name === 'copy' && this.copyTargets.length) return this.copyLabel(this.copyTargets[0]);
    return tr('close');
  }
  @Builder action(name: ToolGlyphName) {
    Button({ type: ButtonType.Circle }) {
      ToolGlyph({ name, glyphSize: 19, ink: name === 'wrap' && this.wrap ? Colors.link : Colors.secondary })
    }.width(44).height(44).padding(0).backgroundColor(Color.Transparent)
      .accessibilityText(this.actionLabel(name)).bindTips(this.actionLabel(name)).onClick(() => {
        if (name === 'wrap') this.wrap = !this.wrap;
        else if (name === 'close') this.onClose();
        else if (this.copyTargets.length) this.copy(this.copyTargets[0].text);
      })
  }
  @Builder copyMenu() {
    Menu() {
      ForEach(this.copyTargets, (field: WorkDetail) => {
        MenuItem({ content: this.copyLabel(field) }).onClick(() => this.copy(field.text))
      }, (field: WorkDetail) => field.key)
    }
  }
  build() {
    Column() {
      Row({space:8}) {
        Stack() {
          ToolGlyph({ name: activityKind(this.step), glyphSize: 22, ink: this.view.status === 'working' ? Colors.working : Colors.activity,
            motion: toolMotion(this.step), running: this.view.status === 'working' })
        }
          .width(32).height(32).borderRadius(8).backgroundColor(Colors.grouped)
        Text(this.view.name).fontSize(this.view.title === this.view.name ? 21 : Tokens.toolLabel)
          .fontFamily(this.view.title === this.view.name ? 'sans-serif' : 'monospace')
          .fontWeight(this.view.title === this.view.name ? FontWeight.Medium : FontWeight.Normal)
          .fontColor(this.view.title === this.view.name ? Colors.ink : Colors.activity)
          .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
        this.action('close')
      }.width('100%').padding({left:20,right:8})
      Column({space:5}) {
        if (this.view.title !== this.view.name) Text(this.view.title).fontSize(21).fontWeight(FontWeight.Medium).lineHeight('28fp')
          .fontColor(Colors.ink).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).wordBreak(WordBreak.BREAK_WORD)
        if (this.view.path && this.view.path !== this.view.title) Text(this.view.path).fontSize(Tokens.toolMeta)
          .fontFamily('monospace').fontColor(Colors.activity).wordBreak(WordBreak.BREAK_ALL).maxLines(2)
          .textOverflow({overflow:TextOverflow.Ellipsis}).copyOption(CopyOptions.LocalDevice)
        Row({space:6}) {
          Text([this.metrics,this.when].filter(Boolean).join('   ·   ')).fontSize(Tokens.toolMeta)
            .fontColor(Colors.activity).lineHeight('18fp').layoutWeight(1)
        }.width('100%').padding({top:3}).accessibilityGroup(true)
          .accessibilityText([accessibleToolState(this.view.status,tr),this.metrics,this.when].filter(Boolean).join(' · '))
      }.width('100%').alignItems(HorizontalAlign.Start).padding({left:20,right:20,bottom:10})
      Row({space:18}) {
        this.tab(false)
        if (hasWorkArguments(this.step.use)) this.tab(true)
        Blank()
        Row() {
          if (this.sections.some(f => f.style !== 'text')) this.action('wrap')
          if (this.copyTargets.length === 1) {
            this.action('copy')
          } else if (this.copyTargets.length > 1) {
            Button({ type: ButtonType.Circle }) { ToolGlyph({ name: 'copy', glyphSize: 19 }) }
              .width(44).height(44).padding(0).backgroundColor(Color.Transparent)
              .accessibilityText(tr('copy')).bindTips(tr('copy')).bindMenu(this.copyMenu).id('chat.tool.copy.menu')
          }
        }
      }.width('100%').padding({left:20,right:8}).alignItems(VerticalAlign.Bottom)
      Divider().strokeWidth(0.5).color(Colors.divider)
      Scroll(this.scroller) {
        Column({space:16}) {
          Repeat(this.sections).each((row: RepeatItem<WorkDetail>) => {
            ToolDetailContent({field:row.item, fileResult:this.view.fileResult, fileSearch:this.view.fileSearch, failure:this.failed,
              wrap:this.wrap,firstLine:this.view.firstLine,file:this.view.path,caption:this.caption(row.item)})
          }).key((f: WorkDetail) => this.step.id + ':' + f.key)
          if (!this.input) {
            if (this.step.images.length) this.images(this.step)
            ForEach(this.reports, (report: TranscriptItem) => {
              AgentEventLine({identity:report.id,event:agentEventDisplay(report,tr),clock:eventClock(report.ts)})
            }, (report: TranscriptItem) => report.id)
            if (this.empty) Text(this.empty).fontSize(Tokens.toolLabel).fontColor(Colors.secondary).lineHeight('20fp')
          }
        }.width('100%').alignItems(HorizontalAlign.Start).padding({left:18,right:18,top:16,bottom:24})
      }.width('100%').layoutWeight(1).align(Alignment.TopStart).scrollBar(BarState.Auto)
    }.width('100%').height('100%').backgroundColor(Colors.canvas).alignItems(HorizontalAlign.Start).id('chat.tool.inspector')
  }
}
