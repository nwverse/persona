import type { AnimatorResult } from '@kit.ArkUI';
import type { ToolMotion } from '../core/WorkPresentation';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from './Colors';
import { toolOutlines, ToolGlyphName } from './ToolGlyphPaths';

// A running tool breathes its outline; command tools draw the shell outline.
@ComponentV2
export struct ToolGlyph {
  @Require @Param name: ToolGlyphName;
  @Param glyphSize: number = 16;
  @Param ink: ResourceColor = Colors.activity;
  @Param running: boolean = false;
  @Param motion: ToolMotion | undefined = undefined;
  @Local visible: boolean = false;
  @Local strength: number = 1;
  private animator: AnimatorResult | undefined;
  private playing: ToolMotion | undefined;

  @Computed get motionEnabled(): boolean { return this.running && !nativeEnvironment.reduceMotion; }
  @Monitor('motion', 'motionEnabled', 'visible') syncMotion(): void {
    if (!this.motionEnabled || !this.visible) { this.stopMotion(); return; }
    if (this.animator && this.playing === this.motion) return;
    this.stopMotion();
    const animator = this.getUIContext().createAnimator({ duration: 1200, easing: 'linear', delay: 0,
      fill: 'none', direction: 'normal', iterations: -1, begin: 0, end: 1 });
    this.animator = animator;
    this.playing = this.motion;
    animator.setExpectedFrameRateRange({ min: 15, max: 60, expected: 24 });
    animator.onFrame = (fraction: number): void => {
      if (this.animator !== animator) return;
      this.strength = 0.79 + 0.21 * Math.cos(2 * Math.PI * fraction);
    };
    animator.play();
  }
  private stopMotion(): void {
    const animator = this.animator;
    this.animator = undefined;
    this.playing = undefined;
    animator?.cancel();
    this.strength = 1;
  }
  aboutToDisappear(): void { this.stopMotion(); }

  @Builder glyphShape() {
    Shape() {
      Path().commands(toolOutlines[this.motion === 'command' ? 'shell' : this.name]).fill(Color.Transparent).stroke(this.ink).strokeWidth('1.8px')
        .strokeLineCap(LineCapStyle.Round).strokeLineJoin(LineJoinStyle.Round)
    }.viewPort({ width: '24px', height: '24px' }).width(this.glyphSize).height(this.glyphSize)
      .flexShrink(0).accessibilityLevel('no').opacity(this.strength)
  }
  build() {
    if (this.motionEnabled) {
      Stack({ alignContent: Alignment.TopStart }) {
        this.glyphShape()
      }.width(this.glyphSize).height(this.glyphSize).clip(true).flexShrink(0).accessibilityLevel('no')
        .onVisibleAreaChange([0, 0.01, 1], (_increasing: boolean, ratio: number) => { this.visible = ratio > 0; })
        .onDisAppear(() => { this.visible = false; this.stopMotion(); })
    } else { this.glyphShape() }
  }
}
