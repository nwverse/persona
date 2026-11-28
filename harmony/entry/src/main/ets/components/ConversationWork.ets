import type { AnimatorResult } from '@kit.ArkUI';
import type { ToolInspectionStep, ToolTurnProjection, WorkInspectionStep } from '../core/Domain';
import { activityHeading, activityKind, ActivityKind, workToolName, recordedAction, ActivityHeading, toolMotion, accessibleToolState, workRowExcerpt, hasWorkDetails, InspectedTool, workStatusFailed, workToolStatus, workTarget, workKind, WorkKind, workMetrics } from '../core/WorkPresentation';
import { agentEventDisplay, eventClock, recordedDuration } from '../core/AgentEvents';
import { MarkdownText } from './MarkdownText';
import { ToolDetailSheet } from './ToolDetailSheet';
import { RunningToolTitle } from './RunningToolTitle';
import { AgentEventLine } from './AgentEventLine';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { ToolGlyph } from '../design/ToolGlyph';
import { Tokens } from '../design/Tokens';

const largeToolFontScale: number = 1.25;

@ComponentV2
struct WorkToolRow {
  @Param animateRunning: boolean = true;
  @Require @Param binding: BoundRow<WorkInspectionStep>;
  @Param showLogPreview: boolean = false;
  @Event inspect: (step: ToolInspectionStep) => void = (_step: ToolInspectionStep): void => {};
  @Computed get step(): ToolInspectionStep {
    return this.binding.value as ToolInspectionStep;
  }
  @Param live: boolean = false;
  @Computed get action(): string { return recordedAction(this.step, tr); }
  @Computed get subject(): string { return workTarget(this.step.use); }
  @Computed get status(): string { return workToolStatus(this.step, this.live); }
  @Computed get kind(): WorkKind { return workKind(this.step.use); }
  @Computed get statusText(): string {
    return accessibleToolState(this.status, tr);
  }
  @Computed get metric(): string {
    return workMetrics(this.step, tr, recordedDuration);
  }
  @Computed get shortOutput(): string {
    return this.showLogPreview ? workRowExcerpt(this.step, this.status) : '';
  }
  @Computed get facts(): string {
    return [this.statusText, this.metric].filter(Boolean).join(' · ');
  }
  @Computed get hasDetails(): boolean { return this.step.use.live_tool_preview !== true && (this.status === 'working' || hasWorkDetails(this.step)); }
  @Computed get minimumRowHeight(): number {
    return nativeEnvironment.fontScale <= largeToolFontScale ? 28 : 44;
  }
  @Computed get labelLines(): number {
    if (nativeEnvironment.fontScale > largeToolFontScale) return 2;
    return 1;
  }
  @Builder header() {
    Row({ space: 5 }) {
      ToolGlyph({ name: activityKind(this.step), ink: this.status === 'working' ? Colors.working : Colors.activity,
        motion: toolMotion(this.step), running: this.animateRunning && this.status === 'working' })
      Text() {
        if (this.subject && this.kind === 'shell') {
          Span(this.subject).fontFamily('monospace')
        } else {
          Span(this.action).fontColor(Colors.activity)
          if (this.subject && this.kind === 'read') Span(' ' + this.subject).decoration({ type: TextDecorationType.Underline, color: Colors.divider })
          else if (this.subject) Span(' ' + this.subject)
        }
      }.fontSize(Tokens.toolLabel).lineHeight(Tokens.toolLeading).fontColor(this.status === 'working' ? Colors.ink : Colors.activity)
        .maxLines(this.labelLines)
        .ellipsisMode(this.labelLines === 1 && (this.kind === 'read' || this.kind === 'edit') ? EllipsisMode.CENTER : EllipsisMode.END)
        .wordBreak(this.labelLines === 1 ? WordBreak.BREAK_ALL : WordBreak.BREAK_WORD).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
      if (this.hasDetails) ToolGlyph({ name: 'chevron', glyphSize: 12, ink: Colors.activity })
    }.width('100%').constraintSize({ minHeight: this.minimumRowHeight }).id('chat.work.line.' + this.step.id)
  }
  build() {
    Column({ space: Tokens.fine }) {
      if (this.hasDetails) {
        Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) { this.header() }
          .width('100%').height('auto').constraintSize({ minHeight: this.minimumRowHeight }).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText([this.action, this.subject, this.facts].filter(Boolean).join(' · '))
          .accessibilityDescription(tr('details')).onClick(() => this.inspect(this.step))
          .id('chat.work.tool.' + this.step.id)
      } else { this.header() }
      if (this.shortOutput || (this.showLogPreview && workStatusFailed(this.status) && this.metric)) {
        Text([workStatusFailed(this.status) ? this.metric : '', this.shortOutput].filter(Boolean).join(' · '))
          .fontSize(Tokens.toolMeta).lineHeight('17fp').fontColor(Colors.secondary)
          .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).width('100%')
          .padding({left:21,right:Tokens.gap,bottom:Tokens.fine}).id('chat.work.output.' + this.step.id)
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}

@ComponentV2
export struct ConversationWork {
  @Require @Param work: ToolTurnProjection;
  @Require @Param ownerTarget: string;
  @Param liveSummary: string = '';
  @Param providerRetry: boolean = false;
  @BuilderParam images: (value: ToolInspectionStep) => void;
  @Param expanded: boolean = false;
  @Param batched: boolean = false;
  @Param batch: WorkInspectionStep[] | undefined = undefined;
  @Param continuation: boolean = false;
  @Param joinsNext: boolean = false;
  @Param ready: boolean = true;
  @Param active: boolean = true;
  @Local visible: boolean = false;
  @Local now: number = 0;
  private clockTimer: number = -1;
  @Event hold: (change: () => void) => void = (change: () => void): void => change();
  @Event onToggle: () => void = () => {};
  @Event onLoad: () => void = () => {};
  @Local steps: BoundRow<WorkInspectionStep>[] = [];
  @Local reveal: number = 0;
  @Local bodyHeight: number = 0;
  private disclosureAnimator: AnimatorResult | undefined;
  private userToggled: boolean = false;
  private appliedOpen: boolean = false;
  @Local detailOpen: boolean = false;
  @Local selection: BoundRow<InspectedTool> | undefined = undefined;
  private detailOwner: string = '';
  private stepScope: string = '';

  aboutToAppear(): void { this.syncSteps(); }
  aboutToDisappear(): void { this.stopClock(); this.finishDisclosure(); this.dismissInspector(); }
  private stopDisclosure(): void {
    const animator = this.disclosureAnimator;
    this.disclosureAnimator = undefined;
    animator?.cancel();
  }
  private finishDisclosure(): void {
    this.stopDisclosure();
    this.reveal = this.expanded ? 1 : 0;
    if (!this.expanded && this.steps.length) this.steps = [];
  }
  @Computed get allowDisclosureMotion(): boolean { return this.active && !nativeEnvironment.reduceMotion; }
  @Monitor('allowDisclosureMotion') disclosurePreferenceChanged(): void {
    if (!this.allowDisclosureMotion && this.disclosureAnimator) this.finishDisclosure();
  }
  private moveDisclosure(): void {
    this.stopDisclosure();
    const end = this.expanded ? 1 : 0;
    if (this.reveal === end) { this.finishDisclosure(); return; }
    const duration = Math.max(70, Math.round((this.expanded ? 275 : 215) * Math.abs(end - this.reveal)));
    const animator = this.getUIContext().createAnimator({ duration, easing: this.expanded ?
      'cubic-bezier(0.22,0.68,0.22,1)' : 'cubic-bezier(0.35,0,0.65,1)', delay: 0,
      fill: 'forwards', direction: 'normal', iterations: 1, begin: this.reveal, end });
    this.disclosureAnimator = animator;
    animator.setExpectedFrameRateRange({ min: 30, max: 60, expected: 60 });
    animator.onFrame = (fraction: number): void => {
      if (this.disclosureAnimator === animator) this.reveal = fraction;
    };
    animator.onFinish = (): void => {
      if (this.disclosureAnimator !== animator) return;
      this.disclosureAnimator = undefined;
      this.finishDisclosure();
    };
    animator.play();
  }
  private stopClock(): void {
    if (this.clockTimer !== -1) { clearInterval(this.clockTimer); this.clockTimer = -1; }
  }
  @Monitor('work', 'active', 'visible') syncClock(): void {
    if (this.continuation || !this.active || !this.visible || !this.group.startedAt || !this.group.active) { this.stopClock(); return; }
    if (this.clockTimer !== -1) return;
    this.now = Date.now();
    this.clockTimer = setInterval(() => { this.now = Date.now(); }, 1000);
  }
  @Computed get elapsed(): string {
    const ms = this.now - this.group.startedAt;
    return this.group.active && this.group.startedAt > 0 && ms >= 1000 ? recordedDuration(Math.floor(ms / 1000) * 1000) : '';
  }
  @Monitor('work', 'expanded', 'ownerTarget', 'batch') syncSteps(): void {
    const scope = JSON.stringify([this.ownerTarget, this.work.workKey]);
    const newScope = scope !== this.stepScope;
    if (newScope) this.bodyHeight = 0;
    if (this.expanded) {
      this.steps = reconcileBoundRows(this.steps, this.batched ? (this.batch ?? []) : this.work.inspectionSteps, (step: WorkInspectionStep) => step.kind + ':' + step.id,
        this, this, this.stepScope, scope);
    }
    if (newScope || this.appliedOpen !== this.expanded) {
      const animate = !newScope && this.userToggled && this.allowDisclosureMotion && this.steps.length > 0;
      this.userToggled = false;
      this.appliedOpen = this.expanded;
      if (animate) this.moveDisclosure();
      else this.finishDisclosure();
    }
    this.stepScope = scope;
  }

  @Computed get group(): ActivityHeading { return activityHeading(this.work, tr); }
  private inspectTool(step: ToolInspectionStep): void {
    if (!this.active || !this.expanded) return;
    this.detailOwner = JSON.stringify([this.ownerTarget, this.work.workKey]);
    this.selection = new BoundRow<InspectedTool>(step.id, { step, live: this.work.running });
    this.detailOpen = true;
  }
  private dismissInspector(): void {
    this.detailOpen = false; this.selection = undefined; this.detailOwner = '';
  }
  @Monitor('expanded', 'active', 'ownerTarget', 'work') syncInspector(): void {
    const selection = this.selection;
    if (!this.detailOpen || !selection) return;
    const step = this.work.inspectionSteps.find(item => item.kind === 'tool' && item.id === selection.identity);
    if (!this.expanded || !this.active || this.detailOwner !== JSON.stringify([this.ownerTarget, this.work.workKey]) || step?.kind !== 'tool') {
      this.dismissInspector(); return;
    }
    if (selection.value.step !== step || selection.value.live !== this.work.running)
      selection.value = {step, live:this.work.running};
  }
  @Builder inspectSheet() {
    if (this.selection) {
      ToolDetailSheet({ selection: this.selection!,
        images: this.images, onClose: () => { this.detailOpen = false; } })
    }
  }
  @Computed get tally(): string {
    return this.group.active ? tr('work_progress_calls')
      .replace('{n}', String(this.group.returned)).replace('{total}', String(this.group.total)) : '';
  }
  @Computed get displayTally(): string {
    return this.group.active ? String(this.group.returned) + '/' + String(this.group.total) : '';
  }
  @Computed get phaseGlyph(): ActivityKind {
    const steps = this.work.inspectionSteps;
    for (let i = steps.length - 1; i >= 0; i--) {
      const step = steps[i];
      if (step.kind === 'tool' && !workToolName(step.use).endsWith('codex_app__update_running_summary')) {
        return toolMotion(step) === 'command' ? 'shell' : activityKind(step);
      }
    }
    return this.group.kind;
  }
  @Builder header() {
    Row({ space: 5 }) {
      if (this.group.active || this.liveSummary) {
        if (this.providerRetry) ToolGlyph({ name:'wifi', ink:Colors.activity }).id('chat.provider.retry')
        else if (this.group.active) {
          ToolGlyph({ name:this.group.kind, glyphSize:18, ink:Colors.working, motion:this.group.motion, running:this.active && !this.detailOpen })
            .id('chat.tool.activeIcon')
        } else {
          ToolGlyph({ name:this.phaseGlyph, glyphSize:18, ink:Colors.activity }).id('chat.turn.phaseIcon')
        }
        Row({ space:4 }) {
          RunningToolTitle({ title: this.liveSummary || this.group.title, animate: this.active && !this.providerRetry && !this.detailOpen && !nativeEnvironment.reduceMotion })
            .constraintSize({ maxWidth: '100%' }).flexShrink(1).id(this.liveSummary ? 'chat.live.summary' : 'chat.work.title.' + this.work.workKey)
          if (this.elapsed || this.tally) {
            Row({ space: 4 }) {
              if (this.elapsed) Text(this.elapsed).fontSize(Tokens.toolMeta).lineHeight('17fp').fontColor(Colors.activity)
                .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
              if (this.tally) Text(this.displayTally).accessibilityText(this.tally).fontSize(Tokens.toolMeta).lineHeight('17fp').fontColor(Colors.activity)
                .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
            }.constraintSize({ maxWidth:'42%' }).flexShrink(0)
          }
        }.layoutWeight(1)
      } else {
        ToolGlyph({ name:this.group.kind, ink:Colors.activity })
        Text(this.group.title).fontSize(Tokens.toolLabel).fontWeight(FontWeight.Normal).lineHeight(Tokens.toolLeading).fontColor(Colors.activity)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).flexShrink(1).id('chat.work.title.' + this.work.workKey)
      }
      if (!this.group.active && this.group.failures) Text($r('app.string.activity_failed_count', this.group.failures))
        .fontSize(Tokens.toolMeta).fontColor(Colors.activity).maxLines(1).flexShrink(0)
      if (this.work.inspectionSteps.length) ToolGlyph({ name: 'chevron', glyphSize: 12 })
        .rotate({ angle: 90 * this.reveal })
    }.width('100%').justifyContent(FlexAlign.Start).padding({ top: 2, bottom: 2 })

  }
  @Builder step(row: BoundRow<WorkInspectionStep>) {
    if (row.value.kind === 'tool') {
      WorkToolRow({ binding: row, live: this.work.running, showLogPreview: this.work.latestExchange, animateRunning: this.expanded && this.active && !this.detailOpen,
        inspect: (step: ToolInspectionStep): void => this.inspectTool(step) })
    } else if (row.value.kind === 'narration' && row.value.item.compact) {
      AgentEventLine({ identity: row.value.id, event: agentEventDisplay(row.value.item, tr), clock: eventClock(row.value.item.ts) })
    } else if (row.value.kind === 'narration' && row.value.role !== 'thinking') {
      Column({ space: Tokens.fine }) {
        if (row.value.role === 'notice') Text(tr('work_notice'))
          .fontSize(Tokens.caption).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
        if (row.value.item.text) MarkdownText({ text:row.value.item.text, transcript:true, compact:true })
      }.width('100%').alignItems(HorizontalAlign.Start).padding({ top: Tokens.fine, bottom: Tokens.fine })
    } else if (row.value.kind === 'fold') {
      Button(tr('work_load_steps'), { buttonStyle: ButtonStyleMode.TEXTUAL }).constraintSize({ minHeight: Tokens.touch })
        .fontSize(Tokens.meta).fontColor(Colors.link).onClick(() => this.onLoad())
    }
  }

  @Builder detailRows() {
    Column() {
      ForEach(this.steps, (row: BoundRow<WorkInspectionStep>) => this.step(row), (row: BoundRow<WorkInspectionStep>) => row.key)
    }.width('100%').alignItems(HorizontalAlign.Start).padding({ left: 14 })
      .border({ width: { left: 1 }, color: Colors.divider })
  }
  build() {
    Column() {
      if (!this.continuation && this.work.inspectionSteps.length) {
        Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) { this.header() }
          .width('100%').height('auto').constraintSize({ minHeight: nativeEnvironment.fontScale <= largeToolFontScale ? 32 : 44 }).padding(0).backgroundColor(Color.Transparent)
          .enabled(this.ready).accessibilityText([this.liveSummary, this.group.title, this.liveSummary && !this.group.active ? tr('work_live_active') : accessibleToolState(this.group.status,tr), this.tally, this.elapsed,
            this.group.failures ? tr('work_failure_count').replace('{n}',String(this.group.failures)) : ''].filter(Boolean).join(' · '))
          .accessibilityStateDescription(tr(this.expanded ? 'work_expanded' : 'work_collapsed'))
          .onClick(() => this.hold(() => { this.userToggled = true; this.onToggle(); })).id('chat.work.' + this.work.workKey)
      } else if (!this.continuation) {
        this.header()
      }
      if (this.continuation) {
        Column() { this.detailRows() }.width('100%').padding({ left:7, bottom:this.joinsNext ? 0 : 5 })
      } else if (this.steps.length) {
        Scroll() {
          Column() { this.detailRows() }.width('100%').alignItems(HorizontalAlign.Start).padding({ left:7, bottom:this.joinsNext ? 0 : 5 })
            .onSizeChange((_old: SizeOptions, size: SizeOptions) => {
              const height = Number(size.height);
              if (height > 0 && height !== this.bodyHeight) this.bodyHeight = height;
            })
            .opacity(0.28 + 0.72 * this.reveal).translate({ y: -6 * (1 - this.reveal) })
        }.width('100%').height(this.bodyHeight > 0 ? this.bodyHeight * this.reveal : (this.reveal === 1 ? undefined : 1))
          .align(Alignment.TopStart).clip(true).scrollBar(BarState.Off).edgeEffect(EdgeEffect.None).enableScrollInteraction(false)
          .hitTestBehavior(this.expanded ? HitTestMode.Default : HitTestMode.None)
          .accessibilityLevel(this.expanded ? 'auto' : 'no-hide-descendants')
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
      .onVisibleAreaChange([0], (visible: boolean, _ratio: number) => { this.visible = visible; })
      .bindSheet($$this.detailOpen, this.inspectSheet(), { height: '70%', detents: ['70%', SheetSize.LARGE],
        preferType: SheetType.BOTTOM, dragBar: true, showClose: false, backgroundColor: Colors.canvas,
        scrollSizeMode: ScrollSizeMode.CONTINUOUS, onDisappear: () => { if (!this.detailOpen) this.dismissInspector(); } })
  }
}
