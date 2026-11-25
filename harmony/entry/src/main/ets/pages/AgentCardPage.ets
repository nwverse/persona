import { common } from '@kit.AbilityKit';
import {
  AgentCardModel,
  AgentCardSessionRow,
  InlineConfirmStep,
  buildInlineCardResolution,
  nextInlineConfirmStep,
  parseAgentCardPayload
} from '../core/AgentCardPayload';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { Wire } from '../models/Wire';
import { HeadlessHubService } from '../xiaoyi/HeadlessHubService';
import { XiaoYiRuntimeContext } from '../xiaoyi/XiaoYiRuntimeContext';

/**
 * Embedded ArkUI card displayed directly inside XiaoYi's conversation stream via AgentUIExtensionAbility.
 * Strictly avoids sub-windows and dialogs, using inline two-step confirmation for eligible approvals.
 */
@Entry
@ComponentV2
struct AgentCardPage {
  @Local card: AgentCardModel = parseAgentCardPayload();
  @Local confirmStep: InlineConfirmStep = 'idle';
  @Local feedbackMessage: string = '';

  aboutToAppear(): void {
    const storage = this.getUIContext().getSharedLocalStorage();
    const raw = storage ? storage.get<string>('agentCardParamsJson') : '';
    if (raw) {
      try {
        this.card = parseAgentCardPayload(Wire.parse(raw));
      } catch (_) {
        this.card = parseAgentCardPayload();
      }
    }
  }

  private openInPersona(): void {
    const host = this.getUIContext().getHostContext() as common.UIExtensionContext | undefined;
    if (!host) return;
    host.startAbility({
      abilityName: 'EntryAbility',
      action: 'ohos.want.action.viewData',
      uri: this.card.deepLinkUri
    }).catch(() => {
      this.feedbackMessage = tr('xiaoyi_card_open_failed');
    });
  }

  private async submitInlineApproval(): Promise<void> {
    if (this.confirmStep !== 'confirming' || !this.card.canApproveInline) return;
    const payload = buildInlineCardResolution(this.card.decisionKind);
    if (!payload) {
      this.openInPersona();
      return;
    }
    const host = this.getUIContext().getHostContext() as common.Context | undefined;
    if (!host) {
      this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'error');
      this.feedbackMessage = tr('xiaoyi_failed');
      return;
    }
    this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'confirm');
    this.feedbackMessage = '';
    const hub = new HeadlessHubService(host);
    try {
      await hub.resolveDecision(this.card.decisionId, payload);
      this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'ok');
      this.feedbackMessage = tr('xiaoyi_card_approved_done');
    } catch (error) {
      this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'error');
      this.feedbackMessage = XiaoYiRuntimeContext.formatError(error as Error);
    } finally {
      await hub.close();
    }
  }

  @Builder headerBadge(label: string, tint: ResourceColor) {
    Text(label)
      .fontSize(Tokens.caption)
      .fontWeight(FontWeight.Medium)
      .fontColor(tint)
      .padding({ left: Tokens.gap, right: Tokens.gap, top: Tokens.fine, bottom: Tokens.fine })
      .backgroundColor(Colors.control)
      .borderRadius(Tokens.inset)
  }

  @Builder statusBody() {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        this.headerBadge(tr('xiaoyi_card_working').replace('{count}', String(this.card.workingCount)), Colors.working)
        this.headerBadge(tr('xiaoyi_card_needs_you').replace('{count}', String(this.card.pendingCount)), Colors.needs)
      }.width('100%')

      if (this.card.body) {
        Text(this.card.body)
          .fontSize(Tokens.meta)
          .fontColor(Colors.secondary)
          .width('100%')
          .maxLines(3)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }

      ForEach(this.card.sessions, (row: AgentCardSessionRow) => {
        Row({ space: Tokens.gap }) {
          Text(row.title)
            .fontSize(Tokens.body)
            .fontColor(Colors.ink)
            .layoutWeight(1)
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
          Text(row.state)
            .fontSize(Tokens.caption)
            .fontColor(Colors.secondary)
        }
        .width('100%')
        .padding({ top: Tokens.fine, bottom: Tokens.fine })
      }, (row: AgentCardSessionRow) => row.title + ':' + row.machine)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder approvalBody() {
    Column({ space: Tokens.inset }) {
      if (this.card.body) {
        Text(this.card.body)
          .fontSize(Tokens.body)
          .fontColor(Colors.ink)
          .lineHeight(Tokens.lineHeight)
          .width('100%')
      }

      if (this.feedbackMessage) {
        Text(this.feedbackMessage)
          .fontSize(Tokens.meta)
          .fontColor(this.confirmStep === 'approved' ? Colors.positive : Colors.secondary)
          .width('100%')
      }

      if (this.card.canApproveInline && this.confirmStep !== 'approved') {
        if (this.confirmStep === 'confirming') {
          Text(tr('xiaoyi_card_confirm_prompt'))
            .fontSize(Tokens.meta)
            .fontColor(Colors.secondary)
            .width('100%')
          Row({ space: Tokens.gap }) {
            Button(tr('cancel'), { type: ButtonType.Capsule })
              .layoutWeight(1)
              .constraintSize({ minHeight: Tokens.touch })
              .backgroundColor(Colors.control)
              .fontColor(Colors.ink)
              .onClick(() => {
                this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'cancel');
              })
              .id('xiaoyi.card.cancel')
            Button(tr('confirm'), { type: ButtonType.Capsule })
              .layoutWeight(1)
              .constraintSize({ minHeight: Tokens.touch })
              .backgroundColor(Colors.action)
              .fontColor(Colors.onAction)
              .onClick(() => {
                this.submitInlineApproval();
              })
              .id('xiaoyi.card.confirm')
          }.width('100%')
        } else {
          Button(this.confirmStep === 'submitting' ? tr('xiaoyi_card_submitting') : tr('xiaoyi_card_approve'), { type: ButtonType.Capsule })
            .width('100%')
            .constraintSize({ minHeight: Tokens.touch })
            .backgroundColor(Colors.action)
            .fontColor(Colors.onAction)
            .enabled(this.confirmStep !== 'submitting')
            .onClick(() => {
              this.confirmStep = nextInlineConfirmStep(this.confirmStep, 'request');
            })
            .id('xiaoyi.card.approve')
        }
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder textBody() {
    Column({ space: Tokens.gap }) {
      if (this.card.body) {
        Text(this.card.body)
          .fontSize(Tokens.body)
          .fontColor(Colors.ink)
          .lineHeight(Tokens.lineHeight)
          .width('100%')
          .maxLines(8)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  build() {
    Column({ space: Tokens.inset }) {
      Row({ space: Tokens.gap }) {
        Text(this.card.title || tr('persona'))
          .fontSize(Tokens.title)
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink)
          .layoutWeight(1)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }.width('100%')

      if (this.card.variant === 'status') {
        this.statusBody()
      } else if (this.card.variant === 'approval') {
        this.approvalBody()
      } else {
        this.textBody()
      }

      Button(this.card.variant === 'answer' ? tr('xiaoyi_card_continue') : tr('xiaoyi_card_open_persona'), {
        type: ButtonType.Capsule
      })
        .width('100%')
        .constraintSize({ minHeight: Tokens.touch })
        .backgroundColor(Colors.control)
        .fontColor(Colors.link)
        .onClick(() => {
          this.openInPersona();
        })
        .id('xiaoyi.card.open')
    }
    .width('100%')
    .padding(Tokens.page)
    .backgroundColor(Colors.surface)
    .borderRadius(Tokens.card)
    .alignItems(HorizontalAlign.Start)
  }
}
