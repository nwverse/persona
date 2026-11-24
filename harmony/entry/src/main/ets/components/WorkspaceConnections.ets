import { ClientModel } from '../models/ClientModel';
import { HubProfile, ResourceRow } from '../models/Wire';
import { ButtonStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct WorkspaceConnections {
  @Require @Param model: ClientModel;
  @Event onClose: () => void = () => {};
  @Event onAdd: () => void = () => {};
  @Local editing: string = '';
  @Local draft: string = '';
  @Local busy: boolean = false;
  @Local error: string = '';

  private async select(profile: HubProfile): Promise<void> {
    if (this.busy || !this.model.profiles.includes(profile)) return;
    this.onClose();
    if (profile.id !== this.model.profile.id || !this.model.connected) {
      await this.model.selectProfile(profile).catch(() => { this.model.error = 'failed'; });
    }
  }

  private async save(profile: HubProfile): Promise<void> {
    if (this.busy || this.editing !== profile.id) return;
    this.busy = true; this.error = '';
    try { await this.model.renameWorkspace(profile, this.draft); this.editing = ''; }
    catch (_) { this.error = 'cache_unavailable'; }
    finally { this.busy = false; }
  }

  private confirmRemoval(profile: HubProfile): void {
    this.getUIContext().showAlertDialog({ title: tr('forget_hub'),
      message: this.model.workspaceName(profile) + '\n' + profile.baseUrl + '\n\n' + tr('forget_hub_notice'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('forget_hub'), action: () => this.remove(profile) } });
  }

  private async remove(profile: HubProfile): Promise<void> {
    if (this.busy || !this.model.profiles.includes(profile)) return;
    this.busy = true; this.error = '';
    try {
      await this.model.forgetProfile(profile.id);
      if (!this.model.profile.id && this.model.profiles.length) await this.model.selectProfile(this.model.profiles[0]);
      if (!this.model.profile.id) this.onClose();
    } catch (_) { this.error = 'cleanup_incomplete'; }
    finally { this.busy = false; }
  }

  @Builder card(profile: HubProfile) {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.inset }) {
        Button({ type: ButtonType.Normal }) {
          Row({ space: Tokens.inset }) {
            SymbolGlyph(Symbols.spaces).fontSize(Tokens.symbolFont).fontColor([Colors.link])
            Column({ space: Tokens.fine }) {
              Text(this.model.workspaceName(profile)).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
                .fontColor(Colors.ink).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
              Text(profile.baseUrl).fontSize(Tokens.caption).fontColor(Colors.secondary).maxLines(1)
                .textOverflow({ overflow: TextOverflow.Ellipsis })
              Text((profile.id === this.model.profile.id ? tr('workspace_current') + ' · ' : '') + tr(this.model.workspaceStatus(profile)))
                .fontSize(Tokens.caption).fontColor(this.model.workspaceStatus(profile) === 'online' ? Colors.positive : Colors.secondary)
            }.alignItems(HorizontalAlign.Start).layoutWeight(1)
          }.width('100%')
        }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).layoutWeight(1)
          .constraintSize({ minHeight: Tokens.touch }).enabled(!this.busy).onClick(() => this.select(profile)).id('workspaces.select.' + profile.id)
        Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.secondary) }
          .attributeModifier(ButtonStyles.icon).accessibilityText(tr('more') + ' · ' + this.model.workspaceName(profile)).enabled(!this.busy)
          .bindMenu([{ value: tr('rename'), action: () => { this.draft = this.model.workspaceName(profile); this.editing = profile.id; this.error = ''; } },
            { value: tr('forget_hub'), action: () => this.confirmRemoval(profile) }]).id('workspaces.more.' + profile.id)
      }.width('100%')
      if (this.model.workspaceStatus(profile) === 'workspace_pair_again') {
        Button(tr('workspace_pair_action')).attributeModifier(ButtonStyles.secondary).enabled(!this.busy)
          .onClick(() => this.onAdd()).id('workspaces.repair.' + profile.id)
      }
      if (this.editing === profile.id) {
        TextInput({ text: this.draft, placeholder: profile.name }).onChange((name: string) => this.draft = name)
          .accessibilityText(tr('workspace_local_name')).onSubmit(() => this.save(profile)).enabled(!this.busy)
        Text(tr('workspace_local_name')).fontSize(Tokens.caption).fontColor(Colors.secondary)
        Row({ space: Tokens.gap }) {
          Button(tr('cancel')).attributeModifier(ButtonStyles.secondary).onClick(() => this.editing = '').enabled(!this.busy)
          Button(tr('save')).attributeModifier(ButtonStyles.primary).onClick(() => this.save(profile)).enabled(!this.busy)
        }
      }
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page).backgroundColor(Colors.canvas).borderRadius(Tokens.card)
  }

  build() {
    List({ space: Tokens.inset }) {
      ForEach(this.model.profiles, (p: HubProfile) => { ListItem() { this.card(p) } }, (p: HubProfile) => JSON.stringify([p.id, p.name, p.baseUrl, p.manager]))
      ListItem() { Button(tr('workspace_add')).attributeModifier(ButtonStyles.secondary).width('100%').enabled(!this.busy)
        .onClick(() => this.onAdd()).id('workspaces.add') }
      if (this.model.connected && this.model.managers.length) {
        ListItem() { Text(tr('manager')).fontSize(Tokens.meta).fontColor(Colors.secondary).padding({ top: Tokens.inset }) }
        ForEach(this.model.managers, (m: ResourceRow) => {
          ListItem() {
            Row({ space: Tokens.inset }) {
              Text(m.title || m.id).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
              if (m.id === this.model.profile.manager) { SymbolGlyph(Symbols.check).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }
            }.constraintSize({ minHeight: Tokens.touch }).width('100%').padding(Tokens.inset).backgroundColor(Colors.canvas).borderRadius(Tokens.card)
          }.enabled(!this.busy).onClick(() => {
            this.onClose();
            if (m.id !== this.model.profile.manager) this.model.selectManager(m.id).catch(() => { this.model.error = 'failed'; });
          }).accessibilityGroup(true).accessibilitySelected(m.id === this.model.profile.manager)
        }, (m: ResourceRow) => m.id)
      }
      if (this.error) { ListItem() { Text(tr(this.error)).fontColor(Colors.danger).fontSize(Tokens.meta) } }
    }.width('100%').height('100%').padding(Tokens.page).backgroundColor(Colors.grouped)
  }
}
