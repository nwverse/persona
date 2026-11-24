// Semantic native colors; appearance resources own the light and dark variants.
@ObservedV2
class NativeColors {
  readonly canvas: ResourceColor = $r('app.color.persona_canvas');
  readonly planSurface: ResourceColor = $r('app.color.persona_plan_surface');
  readonly planBorder: ResourceColor = $r('app.color.persona_plan_border');
  readonly planSecondary: ResourceColor = $r('app.color.persona_plan_secondary');
  readonly planCurrent: ResourceColor = $r('app.color.persona_plan_current');
  readonly grouped: ResourceColor = $r('app.color.persona_grouped');
  readonly ink: ResourceColor = $r('app.color.persona_ink');
  readonly progress: ResourceColor = $r('app.color.persona_progress_text');
  /** Inline code in replies: the body ink and the secondary ink blended 55:45 (DESIGN_SYSTEM 3.11). */
  readonly inlineCode: ResourceColor = $r('app.color.persona_inline_code_ink');
  readonly reference: ResourceColor = $r('app.color.persona_reference');
  readonly activity: ResourceColor = $r('app.color.persona_activity');
  readonly secondary: ResourceColor = $r('app.color.persona_secondary');
  @Trace action: ResourceColor = $r('sys.color.comp_background_emphasize');
  @Trace onAction: ResourceColor = $r('sys.color.font_on_primary');
  @Trace link: ResourceColor = $r('sys.color.font_emphasize');
  readonly control: ResourceColor = $r('app.color.persona_control');
  readonly selection: ResourceColor = $r('app.color.persona_selection');
  readonly question: ResourceColor = $r('app.color.persona_question');
  readonly needs: ResourceColor = $r('app.color.persona_needs');
  readonly working: ResourceColor = $r('app.color.persona_working');
  readonly unread: ResourceColor = $r('app.color.persona_unread');
  readonly diffAddedBackground: ResourceColor = $r('app.color.persona_diff_added_background');
  readonly diffRemovedBackground: ResourceColor = $r('app.color.persona_diff_removed_background');
  readonly positive: ResourceColor = $r('app.color.persona_positive');
  readonly codeKeyword: ResourceColor = $r('app.color.persona_code_keyword');
  readonly codeString: ResourceColor = $r('app.color.persona_code_string');
  readonly codeLiteral: ResourceColor = $r('app.color.persona_code_literal');
  readonly divider: ResourceColor = $r('app.color.persona_divider');
  readonly danger: ResourceColor = $r('app.color.persona_danger');
  readonly ai: ResourceColor = $r('app.color.persona_ai');
  readonly teal: ResourceColor = $r('app.color.persona_teal');
  readonly subtle: ResourceColor = $r('app.color.persona_subtle');
}

export const Colors: NativeColors = new NativeColors();
