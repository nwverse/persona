import type { NextStepChoice, NextStepProposal } from '../core/Domain';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct NextStepCard {
  @Require @Param proposal: NextStepProposal;
  @Param selected: string = '';
  @Param choicesEnabled: boolean = false;
  @Local showRecap: boolean = false;
  @Event choose: (label: string) => void = (_label: string): void => {};
  @Event dismiss: () => void = (): void => {};
  private label(choice: NextStepChoice): string {
    if (!choice.action) return choice.label;
    const key = 'next_step_' + choice.action, localized = tr(key);
    return localized && localized !== key ? localized : choice.label;
  }
  @Builder choices() {
    Column({space:Tokens.gap}) {
      Row({space:Tokens.gap}) {
        Text(this.proposal.ask).fontSize(Tokens.progressBody).lineHeight('22fp').fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          .layoutWeight(1).wordBreak(WordBreak.BREAK_WORD)
        Button({type:ButtonType.Circle,buttonStyle:ButtonStyleMode.TEXTUAL}) {
          SymbolGlyph($r('sys.symbol.xmark')).fontSize(14).fontColor([Colors.secondary])
        }.width(40).height(40).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr('dismiss')).onClick(() => this.dismiss()).id('chat.next.dismiss')
      }.alignItems(VerticalAlign.Top).width('100%')
      if (this.proposal.recap) {
        if (this.showRecap) Text(this.proposal.recap).fontSize(Tokens.meta).lineHeight('20fp').fontColor(Colors.secondary).width('100%')
        Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
          Row({space:4}) {
            Text(tr('next_step_context')).fontSize(Tokens.caption).fontColor(Colors.secondary)
            SymbolGlyph(this.showRecap ? $r('sys.symbol.chevron_up') : $r('sys.symbol.chevron_down')).fontSize(10).fontColor([Colors.secondary])
          }
        }.padding(0).height(32).backgroundColor(Color.Transparent)
          .accessibilityStateDescription(tr(this.showRecap ? 'work_expanded' : 'work_collapsed')).onClick(() => this.showRecap = !this.showRecap)
      }
      Flex({wrap:FlexWrap.Wrap}) {
        ForEach(this.proposal.choices,(choice: NextStepChoice, index: number) => {
          Button(this.label(choice),{type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL})
            .fontSize(Tokens.meta).fontColor(index === 0 ? Colors.onAction : Colors.ink)
            .backgroundColor(index === 0 ? Colors.action : Colors.control).borderRadius(Tokens.gap)
            .constraintSize({minHeight:40,maxWidth:'100%'}).padding({left:Tokens.inset,right:Tokens.inset})
            .margin({right:Tokens.gap,top:Tokens.fine}).enabled(this.choicesEnabled)
            .accessibilityDescription(tr('next_step_send')).onClick(() => this.choose(this.label(choice)))
        },(choice: NextStepChoice, index: number) => String(index) + ':' + (choice.action || choice.label))
      }.width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
  build() {
    Column({space:Tokens.gap}) {
      if (this.selected) {
        Text(this.proposal.recap).width('100%').fontSize(Tokens.meta).lineHeight('21fp').fontColor(Colors.secondary)
          .wordBreak(WordBreak.BREAK_WORD).lineBreakStrategy(LineBreakStrategy.HIGH_QUALITY)
        Column({space:Tokens.fine}) {
          Text(this.proposal.ask).width('100%').fontSize(Tokens.progressBody).lineHeight('22fp').fontColor(Colors.ink)
            .wordBreak(WordBreak.BREAK_WORD).lineBreakStrategy(LineBreakStrategy.HIGH_QUALITY)
          Text(this.selected).width('100%').fontSize(Tokens.meta).fontColor(Colors.secondary).id('chat.next.answer')
        }.width('100%').alignItems(HorizontalAlign.Start)
      } else {
        this.choices()
      }
    }.padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
      .border({width:Tokens.hairline,color:Colors.divider,radius:Tokens.inset}).id('chat.next.step')
  }
}
