import type { AnimatorResult } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';

/** Animate only the glyphs of a visible running tool title. The row and its elapsed clock keep their own updates. */
@ComponentV2
export struct RunningToolTitle {
  @Require @Param title: string;
  @Require @Param animate: boolean;
  @Local visible: boolean = false;
  @Local highlightPosition: number = -1;
  private sweep: AnimatorResult | undefined;

  aboutToAppear(): void { this.syncSweep(); }
  aboutToDisappear(): void { this.stopSweep(); }
  @Monitor('animate', 'visible') syncSweep(): void {
    if (!this.animate || !this.visible) { this.stopSweep(); return; }
    if (this.sweep) return;
    const animator = this.getUIContext().createAnimator({ duration: 2400, easing: 'linear', delay: 0,
      fill: 'none', direction: 'normal', iterations: -1, begin: -0.24, end: 1.75 });
    this.sweep = animator;
    animator.setExpectedFrameRateRange({ min: 15, max: 30, expected: 24 });
    animator.onFrame = (position: number): void => {
      if (this.sweep === animator) this.highlightPosition = position < -0.22 || position > 1.22 ? -1 : position;
    };
    animator.play();
  }
  private stopSweep(): void {
    const animator = this.sweep;
    this.sweep = undefined;
    animator?.cancel();
    this.highlightPosition = -1;
  }
  @Computed get ink(): ShaderStyle {
    if (this.highlightPosition === -1) return new ColorShaderStyle(Colors.ink);
    const center = Math.max(0, Math.min(1, this.highlightPosition));
    return new LinearGradientStyle({ direction: GradientDirection.Right, colors: [
      [Colors.ink, 0], [Colors.ink, Math.max(0, this.highlightPosition - 0.22)],
      [Colors.working, center], [Colors.ink, Math.min(1, this.highlightPosition + 0.22)], [Colors.ink, 1]
    ] });
  }
  build() {
    Text(this.title).fontSize(Tokens.toolLabel).fontWeight(FontWeight.Medium).lineHeight(Tokens.toolLeading).fontColor(Colors.ink)
      .shaderStyle(this.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).wordBreak(WordBreak.BREAK_ALL)
      .onVisibleAreaChange([0], (visible: boolean, _ratio: number): void => { this.visible = visible; })
  }
}
