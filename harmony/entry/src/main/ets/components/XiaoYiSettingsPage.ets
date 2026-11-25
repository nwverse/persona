import { common, skillManager } from '@kit.AbilityKit';
import { XIAOYI_INTENT_CATALOG } from '../core/XiaoYiIntentCatalog';
import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { ClientModel } from '../models/ClientModel';
import { HeadlessDiagnosticReport, HeadlessHubService } from '../xiaoyi/HeadlessHubService';
import { IntentSuggestionsService } from '../xiaoyi/IntentSuggestionsService';
import { XiaoYiRuntimeContext } from '../xiaoyi/XiaoYiRuntimeContext';

/**
 * Native Settings and live self-diagnostics page for HarmonyOS 7 XiaoYi integration
 * (On-Device A2A Agent, Intents Kit, API 26 Skills, and opt-in XiaoYi Suggestions).
 */
@ComponentV2
export struct XiaoYiSettingsPage {
  @Require @Param model: ClientModel;
  @Param elevated: boolean = false;
  @Local suggestionsEnabled: boolean = false;
  @Local savingToggle: boolean = false;
  @Local checking: boolean = false;
  @Local skillCount: number = -1;
  @Local skillNames: string = '';
  @Local report: HeadlessDiagnosticReport | undefined = undefined;
  @Local statusNotice: string = '';
  private mounted: boolean = false;
  private checkRevision: number = 0;

  aboutToAppear(): void {
    this.mounted = true;
    this.loadInitialState();
  }

  aboutToDisappear(): void {
    this.mounted = false;
    this.checkRevision++;
  }

  private hostContext(): common.Context | undefined {
    const ctx = this.getUIContext().getHostContext() as common.Context | undefined;
    return ctx || XiaoYiRuntimeContext.get();
  }

  private async loadInitialState(): Promise<void> {
    const ctx = this.hostContext();
    if (!ctx) return;
    const rev = ++this.checkRevision;
    const enabled = await IntentSuggestionsService.isEnabled(ctx);
    const skills = await this.queryDeclaredSkills();
    if (!this.mounted || rev !== this.checkRevision) return;
    this.suggestionsEnabled = enabled;
    this.skillCount = skills.length;
    this.skillNames = skills.join(', ');
  }

  private async queryDeclaredSkills(): Promise<string[]> {
    try {
      const flags = skillManager.SkillInfoFlag.GET_SKILL_INFO_WITH_DESCRIPTION |
        skillManager.SkillInfoFlag.GET_SKILL_INFO_WITH_SRC_ENTRIES;
      const infos = await skillManager.getSkillInfosForSelf(flags);
      const names: string[] = [];
      for (let i = 0; i < infos.length; i++) {
        if (infos[i].skillName) names.push(infos[i].skillName);
      }
      return names;
    } catch (_) {
      return [];
    }
  }

  private async toggleSuggestions(next: boolean): Promise<void> {
    const ctx = this.hostContext();
    if (!ctx || this.savingToggle) return;
    this.savingToggle = true;
    this.statusNotice = '';
    try {
      const saved = await IntentSuggestionsService.setEnabled(ctx, next);
      if (!this.mounted) return;
      if (saved) {
        this.suggestionsEnabled = next;
        if (next) {
          const outcome = await IntentSuggestionsService.shareStatusSuggestions(ctx, 0, 0);
          if (this.mounted && outcome.status === 'unregistered') {
            this.statusNotice = tr('xiaoyi_suggestions_unregistered');
          }
        }
      } else {
        this.statusNotice = tr('cache_unavailable');
      }
    } finally {
      if (this.mounted) this.savingToggle = false;
    }
  }

  private async runSelfCheck(): Promise<void> {
    const ctx = this.hostContext();
    if (!ctx || this.checking) return;
    const rev = ++this.checkRevision;
    this.checking = true;
    this.statusNotice = '';
    const hub = new HeadlessHubService(ctx);
    try {
      const result = await hub.runDiagnostics();
      const skills = await this.queryDeclaredSkills();
      if (!this.mounted || rev !== this.checkRevision) return;
      this.report = result;
      this.skillCount = skills.length;
      this.skillNames = skills.join(', ');
    } finally {
      await hub.close();
      if (this.mounted && rev === this.checkRevision) this.checking = false;
    }
  }

  @Builder infoRow(label: string, value: string, accent?: ResourceColor) {
    Row({ space: Tokens.gap }) {
      Text(label)
        .fontSize(Tokens.body)
        .maxFontScale(Tokens.interfaceFontMaxScale)
        .fontColor(Colors.ink)
        .layoutWeight(1)
      Text(value)
        .fontSize(Tokens.meta)
        .maxFontScale(Tokens.interfaceScaleFor(Tokens.meta))
        .fontColor(accent || Colors.secondary)
        .textAlign(TextAlign.End)
    }
    .width('100%')
    .constraintSize({ minHeight: Tokens.touch })
    .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.gap })
    .accessibilityGroup(true)
  }

  @Builder cardGroup(title: string) {
    Text(title)
      .fontSize(Tokens.caption)
      .maxFontScale(Tokens.interfaceScaleFor(Tokens.caption))
      .fontWeight(FontWeight.Medium)
      .fontColor(Colors.secondary)
      .width('100%')
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.fine })
  }

  build() {
    List({ space: Tokens.inset }) {
      ListItem() {
        Column() {
          this.cardGroup(tr('xiaoyi_section_capabilities'))
          Column() {
            this.infoRow(tr('xiaoyi_agent_card_label'), 'persona_harmony_agent · 6')
            this.infoRow(tr('xiaoyi_intents_count_label'), String(XIAOYI_INTENT_CATALOG.length))
            this.infoRow(
              tr('xiaoyi_skills_runtime_label'),
              this.skillCount < 0 ? '…' : this.skillCount > 0 ? String(this.skillCount) + (this.skillNames ? ' · ' + this.skillNames : '') : '0'
            )
          }
          .width('100%')
          .backgroundColor(Colors.card(this.elevated))
          .borderRadius(PhoneStyle.cardRadius)
          .clip(true)
        }.width('100%')
      }

      ListItem() {
        Column() {
          this.cardGroup(tr('xiaoyi_section_preferences'))
          Column() {
            Row({ space: Tokens.gap }) {
              Column({ space: Tokens.fine }) {
                Text(tr('xiaoyi_suggestions_toggle'))
                  .fontSize(Tokens.body)
                  .maxFontScale(Tokens.interfaceFontMaxScale)
                  .fontColor(Colors.ink)
                  .width('100%')
                Text(tr('xiaoyi_suggestions_hint'))
                  .fontSize(Tokens.caption)
                  .maxFontScale(Tokens.interfaceScaleFor(Tokens.caption))
                  .fontColor(Colors.secondary)
                  .width('100%')
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              Toggle({ type: ToggleType.Switch, isOn: this.suggestionsEnabled })
                .enabled(!this.savingToggle)
                .onChange((isOn: boolean) => {
                  if (isOn !== this.suggestionsEnabled) this.toggleSuggestions(isOn);
                })
                .id('settings.xiaoyi.suggestions')
            }
            .width('100%')
            .constraintSize({ minHeight: Tokens.touch })
            .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.inset })

            Text(tr('xiaoyi_lock_screen_notice'))
              .fontSize(Tokens.caption)
              .maxFontScale(Tokens.interfaceScaleFor(Tokens.caption))
              .fontColor(Colors.secondary)
              .width('100%')
              .padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })

            if (this.statusNotice) {
              Text(this.statusNotice)
                .fontSize(Tokens.caption)
                .maxFontScale(Tokens.interfaceScaleFor(Tokens.caption))
                .fontColor(Colors.secondary)
                .width('100%')
                .padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })
            }
          }
          .width('100%')
          .backgroundColor(Colors.card(this.elevated))
          .borderRadius(PhoneStyle.cardRadius)
          .clip(true)
        }.width('100%')
      }

      ListItem() {
        Column() {
          this.cardGroup(tr('xiaoyi_section_diagnostics'))
          Column() {
            Button(this.checking ? tr('xiaoyi_diag_running') : tr('xiaoyi_diag_run'), {
              type: ButtonType.Normal,
              buttonStyle: ButtonStyleMode.TEXTUAL
            })
              .width('100%')
              .constraintSize({ minHeight: Tokens.touch })
              .fontColor(Colors.link)
              .enabled(!this.checking)
              .onClick(() => { this.runSelfCheck(); })
              .id('settings.xiaoyi.selfcheck')

            if (this.report) {
              this.infoRow(
                tr('xiaoyi_diag_hub_paired'),
                this.report.paired ? (this.report.profileName || tr('xiaoyi_diag_pass')) : tr('xiaoyi_diag_attention'),
                this.report.paired ? Colors.positive : Colors.working
              )
              this.infoRow(
                tr('xiaoyi_diag_credential'),
                this.report.credentialReadable ? tr('xiaoyi_diag_pass') : tr('xiaoyi_diag_attention'),
                this.report.credentialReadable ? Colors.positive : Colors.working
              )
              this.infoRow(
                tr('xiaoyi_diag_transport'),
                this.report.sshTunnelOnly
                  ? tr('xiaoyi_diag_ssh_warning')
                  : this.report.hubReachable
                    ? tr('xiaoyi_diag_reachable').replace('{online}', String(this.report.onlineMachines)).replace('{total}', String(this.report.totalMachines))
                    : tr('xiaoyi_diag_attention'),
                this.report.hubReachable && !this.report.sshTunnelOnly ? Colors.positive : Colors.working
              )
              if (this.report.errorCode) {
                Text(tr(this.report.errorCode))
                  .fontSize(Tokens.caption)
                  .maxFontScale(Tokens.interfaceScaleFor(Tokens.caption))
                  .fontColor(Colors.secondary)
                  .width('100%')
                  .padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })
              }
            }
          }
          .width('100%')
          .backgroundColor(Colors.card(this.elevated))
          .borderRadius(PhoneStyle.cardRadius)
          .clip(true)
        }.width('100%')
      }
    }
    .width('100%')
    .height('100%')
    .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.section })
    .backgroundColor(Colors.listGroup(this.elevated))
    .id('settings.xiaoyi.page')
  }
}
