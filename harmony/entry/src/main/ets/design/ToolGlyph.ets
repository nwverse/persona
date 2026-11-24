import { ColorMetrics } from '@kit.ArkUI';
import type { AnimatorResult } from '@kit.ArkUI';
import type { ToolMotion } from '../core/WorkPresentation';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from './Colors';
import { toolOutlines, ToolGlyphName } from './ToolGlyphPaths';

interface MotionSheet { image: Resource; frames: number; duration: number; }

// Ten 64 px alpha cells per row, played on a 60 Hz timeline and tinted with the ink. Drawn by harmony/scripts/make-tool-motion.py.
const sheets: Record<string, MotionSheet> = {
  'command': { image: $r('app.media.tool_motion_command'), frames: 100, duration: 1667 },
  'search': { image: $r('app.media.tool_motion_search'), frames: 72, duration: 1200 },
  'read': { image: $r('app.media.tool_motion_read'), frames: 36, duration: 600 },
  'web': { image: $r('app.media.tool_motion_web'), frames: 100, duration: 1667 },
  'scan': { image: $r('app.media.tool_motion_scan'), frames: 100, duration: 1667 },
  'computer': { image: $r('app.media.tool_motion_computer'), frames: 180, duration: 3000 },
  'knowledge': { image: $r('app.media.tool_motion_knowledge'), frames: 72, duration: 1200 },
  'memory': { image: $r('app.media.tool_motion_memory'), frames: 90, duration: 1500 },
  'generate': { image: $r('app.media.tool_motion_generate'), frames: 240, duration: 4000 },
};

@ComponentV2
export struct ToolGlyph {
  @Require @Param name: ToolGlyphName;
  @Param glyphSize: number = 16;
  @Param ink: ResourceColor = Colors.activity;
  @Param running: boolean = false;
  @Param motion: ToolMotion | undefined = undefined;
  @Local visible: boolean = false;
  @Local frame: number = 0;
  @Local strength: number = 1;
  private animator: AnimatorResult | undefined;
  private playing: ToolMotion | undefined;

  @Computed get motionEnabled(): boolean { return this.running && !nativeEnvironment.reduceMotion; }
  @Computed get sheet(): MotionSheet | undefined { return this.motion ? sheets[this.motion] : undefined; }
  @Computed get tint(): ColorFilter {
    const color = ColorMetrics.resourceColor(this.ink);
    return new ColorFilter([0, 0, 0, 0, color.red / 255, 0, 0, 0, 0, color.green / 255,
      0, 0, 0, 0, color.blue / 255, 0, 0, 0, 1, 0]);
  }
  @Monitor('motion', 'motionEnabled', 'visible') syncMotion(): void {
    const sheet = this.sheet;
    if (!this.motionEnabled || !this.visible) { this.stopMotion(); return; }
    if (this.animator && this.playing === this.motion) return;
    this.stopMotion();
    const animator = this.getUIContext().createAnimator({ duration: sheet?.duration ?? 1200, easing: 'linear', delay: 0,
      fill: 'none', direction: 'normal', iterations: -1, begin: 0, end: 1 });
    this.animator = animator;
    this.playing = this.motion;
    animator.setExpectedFrameRateRange({ min: 15, max: 60, expected: sheet ? 60 : 24 });
    animator.onFrame = (fraction: number): void => {
      if (this.animator !== animator) return;
      if (sheet) this.frame = Math.min(sheet.frames - 1, Math.floor(sheet.frames * fraction));
      else this.strength = 0.79 + 0.21 * Math.cos(2 * Math.PI * fraction);
    };
    animator.play();
  }
  private stopMotion(): void {
    const animator = this.animator;
    this.animator = undefined;
    this.playing = undefined;
    animator?.cancel();
    this.frame = 0; this.strength = 1;
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
        if (this.sheet) {
          Image(this.sheet.image).width(10 * this.glyphSize).height(Math.ceil(this.sheet!.frames / 10) * this.glyphSize)
          .objectFit(ImageFit.Fill).interpolation(ImageInterpolation.High).draggable(false).colorFilter(this.tint)
          .position({ x: -(this.frame % 10) * this.glyphSize, y: -Math.floor(this.frame / 10) * this.glyphSize })
        } else { this.glyphShape() }
      }.width(this.glyphSize).height(this.glyphSize).clip(true).flexShrink(0).accessibilityLevel('no')
        .onVisibleAreaChange([0, 0.01, 1], (_increasing: boolean, ratio: number) => { this.visible = ratio > 0; })
        .onDisAppear(() => { this.visible = false; this.stopMotion(); })
    } else { this.glyphShape() }
  }
}
