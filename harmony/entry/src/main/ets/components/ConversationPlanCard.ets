import type { ConversationPlan, PlanStep, PlanStatus } from '../core/ConversationPlan';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { conversationMarkFade } from '../design/ConversationMotion';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

function stepState(status: PlanStatus): string {
  switch (status) {
    case 'in_progress': return tr('work_progress_running');
    case 'completed': return tr('done');
    case 'pending': return tr('plan_step_pending');
    case 'failed': return tr('work_tool_failed');
    case 'blocked': return tr('work_blocked');
    case 'cancelled': return tr('plan_step_cancelled');
    case 'skipped': return tr('plan_step_skipped');
    default: return tr('plan_step_unknown');
  }
}

@ComponentV2
struct PlanMark {
  @Require @Param status: PlanStatus;
  @Param running: boolean = false;
  @Local ringVisible: boolean = false;
  @Computed get spinning(): boolean {
    return this.status === 'in_progress' && this.running && this.ringVisible && !nativeEnvironment.reduceMotion;
  }
  build() {
    Stack() {
      if (this.status === 'completed') {
        Circle().width(14).height(14).fill(Colors.planSecondary)
        SymbolGlyph(Symbols.check).fontSize(10).fontWeight(FontWeight.Bold).fontColor([Colors.planSurface])
          .transition(conversationMarkFade())
      } else if (this.status === 'in_progress') {
        Stack() {
          Circle().width(9).height(9).fill(Colors.planSurface)
        }.width(12).height(12).borderRadius(6).clip(true)
          .sweepGradient({ center: ['50%', '50%'], start: 0, end: 360, rotation: 235, colors: [
            [$r('app.color.persona_plan_track'), 0], [$r('app.color.persona_plan_track'), 0.4],
            [$r('app.color.persona_plan_arc'), 1]
          ] })
          .rotate({ angle: this.spinning ? 360 : 0 })
          .animation({ duration: this.spinning ? 1000 : 0, iterations: this.spinning ? -1 : 1, curve: Curve.Linear })
          .onVisibleAreaChange([0], (visible: boolean, ratio: number) => { this.ringVisible = visible && ratio > 0; })
          .onDisAppear(() => { this.ringVisible = false; })
          .id(this.spinning ? 'chat.plan.running' : 'chat.plan.current')
      } else if (this.status === 'failed' || this.status === 'blocked') {
        SymbolGlyph($r('sys.symbol.exclamationmark_circle')).fontSize(15)
          .fontColor([this.status === 'failed' ? Colors.danger : Colors.needs])
      } else if (this.status === 'skipped' || this.status === 'cancelled') {
        Divider().width(11).strokeWidth(1.5).color(Colors.secondary)
      } else {
        Circle().width(12).height(12).fill(Color.Transparent).stroke(Colors.planSecondary).strokeWidth(1).strokeDashArray([2, 2])
      }
    }.width(14).height('20fp').accessibilityLevel('no')
  }
}

@ComponentV2
struct PlanStepLine {
  @Require @Param binding: BoundRow<PlanStep>;
  @Param running: boolean = false;
  @Computed get step(): PlanStep { return this.binding.value; }
  @Computed get title(): string {
    return this.step.status === 'in_progress' && this.step.activeForm ? this.step.activeForm : this.step.title;
  }
  build() {
    Row({ space: 6 }) {
      PlanMark({ status: this.step.status, running: this.running })
      Text() {
        Span(this.title)
        if (!['completed', 'in_progress', 'pending'].includes(this.step.status)) Span(' · ' + stepState(this.step.status))
      }.fontSize(Tokens.meta).lineHeight('20fp').fontWeight(FontWeight.Normal)
        .fontColor(this.step.status === 'in_progress' ? Colors.planCurrent : Colors.planSecondary)
        .layoutWeight(1).wordBreak(WordBreak.BREAK_WORD)
    }.width('100%').alignItems(VerticalAlign.Top).padding({ top: 4, bottom: 4 })
      .accessibilityGroup(true).accessibilityText(this.title + ' · ' + stepState(this.step.status)).id('chat.plan.step.' + this.step.id)
  }
}

@ComponentV2
export struct ConversationPlanCard {
  @Require @Param plan: ConversationPlan;
  @Require @Param scope: string;
  @Param active: boolean = true;
  @Param running: boolean = false;
  /** Shown above the plan, as the desktop app says a plan is from before the last message of the owner. */
  @Param note: string = '';
  @Event onExpansion: (open: boolean) => void = (_open: boolean): void => {};
  @Event hold: (change: () => void) => void = (change: () => void): void => change();
  @Event read: (plan: ConversationPlan, valid: () => boolean) => Promise<ConversationPlan | undefined> =
    async (_plan: ConversationPlan, _valid: () => boolean): Promise<ConversationPlan | undefined> => undefined;
  @Local userExpanded: boolean | undefined = undefined;
  @Computed get expanded(): boolean { return this.userExpanded ?? this.plan.complete; }
  @Local recovered: ConversationPlan | undefined = undefined;
  @Local loading: boolean = false;
  @Local unavailable: boolean = false;
  @Local rows: BoundRow<PlanStep>[] = [];
  private rowScope: string = '';
  private owner: string = '';
  private revision: string = '';
  private request: number = 0;
  @Computed get shown(): ConversationPlan { return this.plan.complete ? this.plan : this.recovered || this.plan; }
  @Computed get headline(): string {
    const plan = this.shown;
    if (plan.current) return plan.current;
    if (plan.total > 0 && plan.done === plan.total) return tr('plan_all_done');
    if (!plan.complete || plan.steps.some((s: PlanStep) => s.status === 'unknown')) return tr('plan_view_steps');
    const issue = plan.steps.find((s: PlanStep) => s.status === 'failed' || s.status === 'blocked');
    if (issue) return stepState(issue.status) + ' · ' + issue.title;
    if (plan.steps.every((s: PlanStep) => s.status === 'pending')) return tr('plan_not_started');
    return tr('plan_no_active_step');
  }
  aboutToAppear(): void { this.receivePlan(); }
  aboutToDisappear(): void { this.request++; this.loading = false; if (this.expanded) this.onExpansion(false); }
  @Monitor('plan', 'scope', 'active')
  receivePlan(): void {
    const key = JSON.stringify([this.plan.sourceId, this.plan.at, this.plan.done, this.plan.total, this.plan.current, this.plan.complete]);
    if (this.scope !== this.owner) { this.userExpanded = undefined; this.owner = this.scope; this.revision = ''; }
    if (key !== this.revision || !this.active) {
      this.request++; this.loading = false; this.unavailable = false; this.recovered = undefined; this.revision = key;
    }
    const open = this.userExpanded ?? this.plan.complete;
    const shown = this.plan.complete ? this.plan : this.recovered || this.plan;
    this.syncRows(open, shown);
    if (this.active && open && !shown.complete && !this.loading && !this.unavailable) this.load();
  }
  private syncRows(open: boolean, plan: ConversationPlan): void {
    this.rows = reconcileBoundRows(this.rows, open && plan.complete ? plan.steps : [],
      (step: PlanStep) => step.id, this, this, this.rowScope, this.scope);
    this.rowScope = this.scope;
  }
  private async load(): Promise<void> {
    const id = ++this.request, scope = this.scope;
    const valid = (): boolean => this.active && this.expanded && this.scope === scope && this.request === id;
    this.loading = true; this.unavailable = false;
    try {
      const result = await this.read(this.plan, valid);
      if (valid()) { this.recovered = result; this.unavailable = !result?.complete; this.syncRows(true, result || this.plan); }
    } catch (_) { if (valid()) this.unavailable = true; }
    finally { if (valid()) this.loading = false; }
  }
  private toggle(): void {
    this.hold(() => {
      const open = !this.expanded;
      this.userExpanded = open;
      this.onExpansion(open);
      if (!open) { this.request++; this.loading = false; }
      this.syncRows(open, this.shown);
      if (open && !this.shown.complete) this.load();
    });
  }
  @Computed get countLabel(): string {
    if (!this.shown.complete && this.shown.sourceId) return '';
    return tr('plan_done_count').replace('{n}', String(this.shown.done)).replace('{total}', String(this.shown.total));
  }
  @Builder header() {
    Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
      Row({ space: 10 }) {
        if (this.running && this.shown.current) PlanMark({status:'in_progress',running:this.active})
        else SymbolGlyph(Symbols.spaces).fontSize(16).fontColor([Colors.secondary]).accessibilityLevel('no')
        Text(this.expanded ? tr('chat_execution_plan') : this.headline)
          .fontSize(Tokens.progressBody).lineHeight('21fp').fontColor(Colors.ink).fontWeight(FontWeight.Medium)
          .maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
        if (this.countLabel) {
          Text(this.shown.done + '/' + this.shown.total).fontSize(Tokens.caption).fontColor(Colors.secondary)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        }
        SymbolGlyph(this.expanded ? Symbols.up : Symbols.down).fontSize(10).fontColor([Colors.secondary])
      }.width('100%')
    }.width('100%').constraintSize({ minHeight: Tokens.touch }).padding({ left: 13, right: 13, top: 8, bottom: 8 })
      .backgroundColor(Color.Transparent)
      .accessibilityText([tr('chat_execution_plan'), this.note, this.headline, this.countLabel].filter(Boolean).join(' · '))
      .accessibilityStateDescription(tr(this.expanded ? 'work_expanded' : 'work_collapsed'))
      .onClick(() => this.toggle()).id('chat.plan.toggle')
  }
  build() {
    Column() {
      if (this.note) {
        Text(this.note).fontSize(Tokens.caption).fontColor(Colors.secondary).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
          .width('100%').padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset }).id('chat.plan.note')
      }
      if (this.expanded && this.shown.complete) {
        Column() {
          ForEach(this.rows, (row: BoundRow<PlanStep>) => { PlanStepLine({ binding: row, running:this.active && this.running }) }, (row: BoundRow<PlanStep>) => row.key)
        }.width('100%').alignItems(HorizontalAlign.Start).padding({ left: 16, right: 16, top: 11, bottom: 11 })
          .accessibilityRole(AccessibilityRoleType.BUTTON)
          .accessibilityText([tr('chat_execution_plan'), this.note, this.countLabel].filter(Boolean).join(' · '))
          .accessibilityStateDescription(tr('work_expanded')).onClick(() => this.toggle()).id('chat.plan.steps')
      } else {
        this.header()
        if (this.expanded) {
          Column() {
            if (this.loading) {
              Text(tr('plan_loading_steps')).fontSize(Tokens.meta).fontColor(Colors.secondary)
            } else if (this.unavailable) {
              Text(tr('plan_steps_unavailable')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              Button(tr('retry'), { buttonStyle: ButtonStyleMode.TEXTUAL }).fontColor(Colors.link).onClick(() => this.load())
            }
          }.width('100%').alignItems(HorizontalAlign.Start).padding({ left: 16, right: 16, bottom: 11 })
        }
      }
    }.width('100%').alignItems(HorizontalAlign.Start).backgroundColor(Colors.planSurface)
      .border({ width: Tokens.hairline, color: Colors.planBorder, radius: 12 }).borderRadius(12).clip(true).id('chat.execution.plan')
  }
}
