import { pasteboard } from '@kit.BasicServicesKit';
import { ComposerQueueState, QueuedComposerItem } from '../core/ComposerQueue';
import { Colors } from '../design/Colors';
import { IconActions } from '../design/IconActions';
import { Motion } from '../design/Motion';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { toastCenter } from '../models/ToastCenter';
import { Haptics } from '../platform/Haptics';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

/**
 * Unified composer-top queue dock strip matching iOS `ComposerTopSlot.swift` (#16247).
 * Docks inside the top edge of the composer surface above a hairline divider.
 */
@ComponentV2
export struct ComposerTopStrip {
  @Require @Param queue: ComposerQueueState;
  @Param busy: boolean = false;
  @Event onSendNow: () => void = () => {};
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private expanded: boolean = false;
  @Local private copiedItemId: string = '';
  private copyResetTimer: number = -1;

  aboutToDisappear(): void {
    if (this.copyResetTimer !== -1) {
      clearTimeout(this.copyResetTimer);
      this.copyResetTimer = -1;
    }
  }

  private toggleExpanded(): void {
    Haptics.tap();
    Motion.run(this.getUIContext(), Motion.state(this.env.reduceMotion), () => {
      this.expanded = !this.expanded;
    });
  }

  private copyItem(item: QueuedComposerItem): void {
    try {
      pasteboard.getSystemPasteboard().setDataSync(
        pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, item.text)
      );
      Haptics.selection();
      this.copiedItemId = item.id;
      if (this.copyResetTimer !== -1) clearTimeout(this.copyResetTimer);
      this.copyResetTimer = setTimeout(() => {
        if (this.copiedItemId === item.id) this.copiedItemId = '';
        this.copyResetTimer = -1;
      }, 1500);
      toastCenter.show(tr('copied'));
    } catch (_) {
      Haptics.error();
      toastCenter.error(tr('failed'));
    }
  }

  private countBadgeLabel(): string {
    return tr('composer_queued_count').replace('{count}', String(this.queue.items.length));
  }

  build() {
    Column({ space: 0 }) {
      Row({ space: 8 }) {
        Button({ type: ButtonType.Normal }) {
          Row({ space: 6 }) {
            SymbolGlyph($r('sys.symbol.clock'))
              .fontSize('12.5vp')
              .fontColor([Colors.secondary])
            Text(this.countBadgeLabel())
              .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
              .fontWeight(FontWeight.Medium)
              .fontColor(Colors.link)
              .maxLines(1)
              .padding({ left: 7, right: 7, top: 2, bottom: 2 })
              .backgroundColor(Colors.accentWell)
              .borderRadius(10)
            if (!this.expanded && this.queue.preview) {
              Text(this.queue.preview)
                .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                .fontColor(Colors.secondary)
                .maxLines(1)
                .textOverflow({ overflow: TextOverflow.Ellipsis })
                .layoutWeight(1)
            } else {
              Blank().layoutWeight(1)
            }
            SymbolGlyph(Symbols.down)
              .fontSize('11vp')
              .fontColor([Colors.tertiary])
              .rotate({ angle: this.expanded ? 180 : 0 })
              .animation(Motion.state(this.env.reduceMotion))
          }
          .width('100%')
          .alignItems(VerticalAlign.Center)
        }
        .layoutWeight(1)
        .padding({ left: 4, right: 4, top: 4, bottom: 4 })
        .backgroundColor(Color.Transparent)
        .accessibilityText(this.countBadgeLabel())
        .onClick(() => this.toggleExpanded())
        .id('composer.queue.toggle')

        if (this.queue.steersQueue && this.queue.ownCount > 0) {
          Button({ type: ButtonType.Capsule }) {
            Row({ space: 4 }) {
              if (this.busy) {
                LoadingProgress()
                  .width(12)
                  .height(12)
                  .color(Colors.link)
              } else {
                SymbolGlyph($r('sys.symbol.bolt'))
                  .fontSize('11.5vp')
                  .fontColor([Colors.link])
              }
              Text(tr('composer_queue_send_now'))
                .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
                .fontWeight(FontWeight.Medium)
                .fontColor(Colors.link)
                .maxLines(1)
            }
          }
          .height(26)
          .padding({ left: 10, right: 10 })
          .backgroundColor(Colors.selection)
          .border({
            width: PhoneStyle.surfaceEdgeWidth(0.5),
            color: PhoneStyle.cardBorder(this.env.dark),
            radius: 13
          })
          .enabled(!this.busy)
          .onClick(() => {
            Haptics.tap();
            this.onSendNow();
          })
          .id('composer.queue.send_now')
        }
      }
      .width('100%')
      .padding({ left: 4, right: 4, top: 2, bottom: 4 })
      .alignItems(VerticalAlign.Center)
      .id('composer.queue.strip')

      if (this.expanded) {
        Scroll() {
          Column({ space: 6 }) {
            ForEach(this.queue.items, (item: QueuedComposerItem, index: number) => {
              Row({ space: 8 }) {
                Text(String(index + 1))
                  .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                  .fontWeight(FontWeight.Bold)
                  .fontColor(Colors.link)
                  .textAlign(TextAlign.Center)
                  .width(20)
                  .height(20)
                  .borderRadius(10)
                  .backgroundColor(Colors.accentWell)

                Column({ space: 2 }) {
                  Text(item.text)
                    .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                    .fontColor(Colors.ink)
                    .lineHeight(18)
                    .maxLines(3)
                    .textOverflow({ overflow: TextOverflow.Ellipsis })
                    .width('100%')
                  if (item.relayed || item.sender) {
                    Row({ space: 6 }) {
                      if (item.relayed) {
                        Text(tr('composer_queue_relayed'))
                          .fontSize(Tokens.interfaceFontVp(10.5, this.env.fontScale))
                          .fontWeight(FontWeight.Medium)
                          .fontColor(Colors.secondary)
                          .padding({ left: 6, right: 6, top: 1, bottom: 1 })
                          .backgroundColor(Colors.control)
                          .borderRadius(6)
                      }
                      if (item.sender) {
                        Text(item.sender)
                          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                          .fontColor(Colors.secondary)
                          .maxLines(1)
                          .textOverflow({ overflow: TextOverflow.Ellipsis })
                      }
                    }
                  }
                }
                .layoutWeight(1)
                .alignItems(HorizontalAlign.Start)

                Button({ type: ButtonType.Circle }) {
                  SymbolGlyph(this.copiedItemId === item.id ? Symbols.check : IconActions.symbol('copy'))
                    .fontSize('13vp')
                    .fontColor([this.copiedItemId === item.id ? Colors.positive : Colors.secondary])
                }
                .width(28)
                .height(28)
                .padding(0)
                .backgroundColor(Color.Transparent)
                .responseRegion({ x: -8, y: -8, width: 44, height: 44 })
                .accessibilityText(tr('copy'))
                .bindTips(tr('copy'))
                .onClick(() => this.copyItem(item))
                .id('composer.queue.copy.' + item.id)
              }
              .width('100%')
              .padding({ left: 10, right: 8, top: 7, bottom: 7 })
              .alignItems(VerticalAlign.Top)
              .backgroundColor(this.env.dark ? '#18FFFFFF' : '#08000000')
              .borderRadius(12)
              .border({
                width: PhoneStyle.surfaceEdgeWidth(0.5),
                color: PhoneStyle.cardBorder(this.env.dark),
                radius: 12
              })
            }, (item: QueuedComposerItem, index: number) => item.id + ':' + String(index) + ':' + item.text)
          }
          .width('100%')
          .padding({ left: 4, right: 4, bottom: 6 })
        }
        .scrollable(ScrollDirection.Vertical)
        .scrollBar(BarState.Auto)
        .constraintSize({ maxHeight: 168 })
        .width('100%')
        .transition(Motion.slideFade(this.env.reduceMotion, -4))
        .animation(Motion.state(this.env.reduceMotion))
        .id('composer.queue.list')
      }

      Divider()
        .strokeWidth(0.5)
        .color(Colors.divider)
        .width('100%')
        .margin({ top: 2, bottom: 2 })
    }
    .width('100%')
    .alignItems(HorizontalAlign.Start)
    .animation(Motion.state(this.env.reduceMotion))
  }
}
