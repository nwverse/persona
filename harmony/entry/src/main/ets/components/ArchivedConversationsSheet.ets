import { SymbolGlyphModifier } from '@kit.ArkUI';
import { sidebarConversationTitle } from '../models/AssistantRecentChats';
import { Colors } from '../design/Colors';
import { IconActions } from '../design/IconActions';
import { PersonaSheetHeader } from '../design/NormalizedPrimitives';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { ClientModel } from '../models/ClientModel';
import { segment } from '../models/HubRepository';
import { toastCenter } from '../models/ToastCenter';
import { ResourceRow, Wire } from '../models/Wire';
import { Haptics } from '../platform/Haptics';

/**
 * Archived assistant conversations list matching iOS `ArchivedConversationsView.swift`.
 */
@ComponentV2
export struct ArchivedConversationsSheet {
  @Require @Param model: ClientModel;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onClose: () => void = () => {};

  @Local private query: string = '';
  @Local private busyKeys: string[] = [];

  @Computed get archivedRows(): ResourceRow[] {
    const cleanQuery = this.query.trim().toLowerCase();
    const list = this.model.conversations.filter((row: ResourceRow) => Wire.bool(row.data(), 'archived'));
    const sorted = list.slice().sort((a: ResourceRow, b: ResourceRow) => {
      const ta = Date.parse(Wire.text(a.data(), 'updated_at', Wire.text(a.data(), 'created_at'))) || 0;
      const tb = Date.parse(Wire.text(b.data(), 'updated_at', Wire.text(b.data(), 'created_at'))) || 0;
      return tb - ta;
    });
    if (!cleanQuery) return sorted;
    return sorted.filter((row: ResourceRow) => {
      const title = sidebarConversationTitle(row, tr).toLowerCase();
      return title.includes(cleanQuery) || row.id.toLowerCase().includes(cleanQuery);
    });
  }

  private rowSessionId(row: ResourceRow): string {
    return row.session || row.id;
  }

  private rowBusyKey(row: ResourceRow): string {
    return JSON.stringify([this.model.scope(), this.model.connectionGeneration(), row.machine, this.rowSessionId(row)]);
  }

  private isBusy(row: ResourceRow): boolean {
    return this.busyKeys.includes(this.rowBusyKey(row));
  }

  private formattedUpdatedAt(row: ResourceRow): string {
    const raw = Wire.text(row.data(), 'updated_at', Wire.text(row.data(), 'created_at'));
    if (!raw) return '';
    const ms = Date.parse(raw);
    if (!Number.isFinite(ms)) return '';
    try {
      return new Date(ms).toLocaleDateString();
    } catch (_) {
      return '';
    }
  }

  private async unarchive(row: ResourceRow): Promise<void> {
    const owner = this.model;
    const id = this.rowSessionId(row);
    if (!owner.connected || !id) return;
    const path = '/api/v1/assistant/conversations/' + segment(id) + '/unarchive';
    if (owner.operationBlocked(path)) {
      toastCenter.error(tr('pending_action'));
      return;
    }
    const busyKey = this.rowBusyKey(row);
    if (this.busyKeys.includes(busyKey)) return;
    const generation = owner.connectionGeneration();
    const scope = owner.scope();
    this.busyKeys = this.busyKeys.concat([busyKey]);
    let ok = false;
    try {
      await owner.repo().call(path, 'POST', {}, '', () => owner === this.model && owner.owns(generation, scope));
      ok = true;
      if (owner === this.model && owner.owns(generation, scope)) {
        const updated = owner.conversations.map((item: ResourceRow) => {
          if (item !== row && this.rowSessionId(item) !== id) return item;
          const data = item.data();
          data['archived'] = false;
          const next = ResourceRow.from(data, item.kind);
          next.group = item.group;
          next.manager = item.manager;
          next.machine = item.machine;
          next.session = item.session || item.id;
          return next;
        });
        owner.conversations = updated;
        Haptics.success();
        toastCenter.success(tr('unarchived_toast'));
      }
    } catch (error) {
      if (owner === this.model && owner.owns(generation, scope)) {
        owner.reportError(error as Error);
        Haptics.error();
        toastCenter.error(trError(owner.error || 'failed'));
      }
    } finally {
      this.busyKeys = this.busyKeys.filter((entry: string) => entry !== busyKey);
    }
    if (ok && owner === this.model && owner.owns(generation, scope)) {
      try { await owner.refresh(); } catch (_) {}
    }
  }

  @Builder rowContextMenu(row: ResourceRow) {
    Menu() {
      MenuItem({
        content: tr('unarchive'),
        symbolStartIcon: new SymbolGlyphModifier($r('sys.symbol.archivebox'))
      })
        .enabled(!this.isBusy(row))
        .onClick(() => { void this.unarchive(row); })
        .id('archived.menu.unarchive.' + this.rowSessionId(row))
    }
  }

  @Builder searchBar() {
    Row({ space: Tokens.spaceS }) {
      SymbolGlyph(Symbols.search)
        .fontSize('15vp')
        .fontColor([Colors.secondary])
      TextInput({ text: this.query, placeholder: tr('archived_search_placeholder') })
        .layoutWeight(1)
        .height(Tokens.controlCompact)
        .fontSize(Tokens.typeSecondary)
        .fontColor(Colors.ink)
        .placeholderColor(Colors.secondary)
        .backgroundColor(Color.Transparent)
        .padding({ left: 0, right: 0 })
        .onChange((value: string) => {
          this.query = value;
        })
        .id('archived.search')
      if (this.query.length > 0) {
        Button({ type: ButtonType.Circle }) {
          SymbolGlyph(IconActions.symbol('close'))
            .fontSize('13vp')
            .fontColor([Colors.secondary])
        }
        .width(28)
        .height(28)
        .padding(0)
        .backgroundColor(Color.Transparent)
        .responseRegion({ x: -8, y: -8, width: Tokens.controlBar, height: Tokens.controlBar })
        .accessibilityText(tr('clear'))
        .onClick(() => {
          this.query = '';
        })
      }
    }
    .width('100%')
    .padding({ left: Tokens.spaceM, right: Tokens.spaceS, top: Tokens.spaceXs, bottom: Tokens.spaceXs })
    .backgroundColor(Colors.control)
    .borderRadius(Tokens.radiusCard)
    .alignItems(VerticalAlign.Center)
  }

  @Builder emptyState(title: string, subtitle: string) {
    Column({ space: Tokens.spaceS }) {
      Stack({ alignContent: Alignment.Center }) {
        SymbolGlyph($r('sys.symbol.archivebox'))
          .fontSize('26vp')
          .fontColor([Colors.secondary])
      }
      .width(56)
      .height(56)
      .borderRadius(Tokens.radiusPanel)
      .backgroundColor(Colors.control)

      Text(title)
        .fontSize(Tokens.typeHeading)
        .maxFontScale(Tokens.interfaceFontMaxScale)
        .fontWeight(600)
        .fontColor(Colors.ink)
        .textAlign(TextAlign.Center)

      if (subtitle) {
        Text(subtitle)
          .fontSize(Tokens.typeSecondary)
          .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeSecondary))
          .fontColor(Colors.secondary)
          .textAlign(TextAlign.Center)
          .constraintSize({ maxWidth: 320 })
      }
    }
    .width('100%')
    .layoutWeight(1)
    .padding(Tokens.spaceXl)
    .justifyContent(FlexAlign.Center)
    .alignItems(HorizontalAlign.Center)
  }

  build() {
    Column({ space: 0 }) {
      PersonaSheetHeader({
        title: tr('archived_chats'),
        subtitle: this.archivedRows.length > 0 ? String(this.archivedRows.length) : '',
        closeIdentity: 'archived.close',
        showDivider: true,
        onClose: () => this.onClose()
      })

      Column({ space: Tokens.spaceM }) {
        if (this.model.conversations.some((row: ResourceRow) => Wire.bool(row.data(), 'archived'))) {
          this.searchBar()
        }

        if (this.archivedRows.length === 0) {
          this.emptyState(
            this.query.trim() ? tr('archived_search_empty') : tr('archived_empty_title'),
            this.query.trim() ? '' : tr('archived_empty_message')
          )
        } else {
          List({ space: Tokens.spaceS }) {
            ForEach(this.archivedRows, (row: ResourceRow) => {
              ListItem() {
                Row({ space: Tokens.spaceM }) {
                  Column({ space: Tokens.spaceXxs }) {
                    Text(sidebarConversationTitle(row, tr))
                      .fontSize(Tokens.typeBody)
                      .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeBody))
                      .fontWeight(FontWeight.Medium)
                      .fontColor(Colors.ink)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                    if (this.formattedUpdatedAt(row)) {
                      Text(this.formattedUpdatedAt(row))
                        .fontSize(Tokens.typeCaption)
                        .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeCaption))
                        .fontColor(Colors.secondary)
                        .maxLines(1)
                    }
                  }
                  .layoutWeight(1)
                  .alignItems(HorizontalAlign.Start)
                  .onClick(() => {
                    Haptics.tap();
                    this.onOpen(row);
                  })

                  Button({ type: ButtonType.Capsule }) {
                    Row({ space: Tokens.spaceXs }) {
                      if (this.isBusy(row)) {
                        LoadingProgress().width(13).height(13).color(Colors.link)
                      } else {
                        SymbolGlyph($r('sys.symbol.archivebox'))
                          .fontSize('12vp')
                          .fontColor([Colors.link])
                      }
                      Text(tr('unarchive'))
                        .fontSize(Tokens.typeCaption)
                        .fontWeight(FontWeight.Medium)
                        .fontColor(Colors.link)
                        .maxLines(1)
                    }
                  }
                  .height(Tokens.controlCompact)
                  .padding({ left: Tokens.spaceM, right: Tokens.spaceM })
                  .responseRegion({ x: 0, y: -4, width: '100%', height: Tokens.controlBar })
                  .backgroundColor(Colors.needsWash)
                  .enabled(!this.isBusy(row))
                  .onClick(() => { void this.unarchive(row); })
                  .id('archived.unarchive.' + this.rowSessionId(row))
                }
                .width('100%')
                .constraintSize({ minHeight: Tokens.controlBar })
                .padding({ left: Tokens.spaceL, right: Tokens.spaceM, top: Tokens.spaceM, bottom: Tokens.spaceM })
                .backgroundColor(Colors.sheetCard)
                .borderRadius(Tokens.radiusCard)
                .border({
                  width: PhoneStyle.surfaceEdgeWidth(0.5),
                  color: Colors.hairlineGlass,
                  radius: Tokens.radiusCard
                })
                .alignItems(VerticalAlign.Center)
                .bindContextMenu(this.rowContextMenu(row), ResponseType.LongPress)
                .id('archived.row.' + this.rowSessionId(row))
              }
            }, (row: ResourceRow) => row.key() + ':' + Wire.text(row.data(), 'updated_at'))
          }
          .width('100%')
          .layoutWeight(1)
          .scrollBar(BarState.Auto)
        }
      }
      .width('100%')
      .layoutWeight(1)
      .padding({ left: Tokens.spaceL, right: Tokens.spaceL, top: Tokens.spaceM, bottom: Tokens.spaceL })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(Colors.sheetBackground)
    .id('archived.sheet')
  }
}
