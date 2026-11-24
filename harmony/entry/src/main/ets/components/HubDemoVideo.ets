import { Colors } from '../design/Colors';
import { ButtonStyles, ScrollStyles } from '../design/Controls';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct HubDemoVideo {
  @Event onDemo: () => void = () => {};
  @Local playing: boolean = false;
  @Local failed: boolean = false;
  private finished: boolean = false;
  private controller: VideoController = new VideoController();

  aboutToDisappear(): void { if (this.playing) this.controller.stop(); }

  private togglePlayback(): void {
    if (this.playing) { this.controller.pause(); return; }
    if (this.finished) { this.controller.setCurrentTime(0); this.finished = false; }
    this.controller.start();
  }

  build() {
    Scroll() {
      Column({ space: Tokens.page }) {
        Video({ src: $rawfile('hub_tour.mp4'), previewUri: $r('app.media.hub_tour_cover'), controller: this.controller })
          .width('100%').aspectRatio(2 / 3).objectFit(ImageFit.Contain).controls(true).autoPlay(false).muted(true)
          .borderRadius(Tokens.card).clip(true).onStart(() => this.playing = true).onPause(() => this.playing = false)
          .onFinish(() => { this.playing = false; this.finished = true; }).onError(() => { this.failed = true; this.playing = false; }).id('tour.player')
        Text(tr(this.failed ? 'hub_tour_error' : 'hub_tour_note')).fontSize(Tokens.meta).fontColor(Colors.secondary).textAlign(TextAlign.Center)
        if (!this.failed) Button(tr(this.playing ? 'hub_tour_pause' : 'hub_tour_play')).attributeModifier(ButtonStyles.primary)
          .width('100%').onClick(() => this.togglePlayback()).id('tour.play')
        Button(tr('hub_tour_try')).attributeModifier(ButtonStyles.secondary).width('100%').onClick(() => this.onDemo()).id('tour.demo')
      }.width('100%').constraintSize({ maxWidth: Tokens.master }).padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.section })
    }.width('100%').height('100%').attributeModifier(ScrollStyles.form)
  }
}
