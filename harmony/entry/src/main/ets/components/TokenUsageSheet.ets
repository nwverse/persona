import { LengthMetrics } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { ButtonStyles } from '../design/Controls';
import { Motion } from '../design/Motion';
import { PersonaSectionLabel, PersonaSheetHeader, PersonaStatusPill } from '../design/NormalizedPrimitives';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import {
  buildTokenHeatmap,
  cacheHitRatePercent,
  emptyUsageTokens,
  formatSharePercent,
  HeatmapCell,
  HeatmapMonthLabel,
  parseUsageDaysResponse,
  parseUsageHoursResponse,
  parseUsageProjectDetailResponse,
  parseUsageProjectsResponse,
  RankedUsagePart,
  RankedUsageProject,
  rankProjectsForRange,
  rankUsageParts,
  selectProjectBreakdown,
  sumLastDaysTokens,
  TokenFormat,
  TokenHeatmapGrid,
  UsageDaysResponse,
  UsageHoursResponse,
  UsageProject,
  UsageProjectBreakdown,
  UsageProjectDetailResponse,
  UsageProjectRangeKey,
  UsageProjectsResponse,
  UsageTokens
} from '../core/TokenUsage';
import { ClientModel } from '../models/ClientModel';
import { HttpFailure } from '../platform/HubTransport';
import { Haptics } from '../platform/Haptics';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct TokenUsageSheet {
  @Require @Param model: ClientModel;
  @Event onClose: () => void = () => {};
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private loading: boolean = true;
  @Local private unsupported: boolean = false;
  @Local private loadError: string = '';
  @Local private daysData: UsageDaysResponse | undefined = undefined;
  @Local private hoursData: UsageHoursResponse | undefined = undefined;
  @Local private projectsData: UsageProjectsResponse | undefined = undefined;
  @Local private heatmap: TokenHeatmapGrid = { weeks: 0, columns: [], months: [], todayCell: null };
  @Local private pickedDate: string = '';
  @Local private projectRange: UsageProjectRangeKey = '7d';
  @Local private selectedProject: UsageProject | undefined = undefined;
  @Local private projectDetail: UsageProjectDetailResponse | undefined = undefined;
  @Local private detailLoading: boolean = false;
  @Local private detailError: string = '';
  private loadSerial: number = 0;
  private detailSerial: number = 0;

  aboutToAppear(): void {
    this.loadAll().catch(() => undefined);
  }

  private is404(err: Object): boolean {
    if (err instanceof HttpFailure) {
      return err.status === 404;
    }
    const rec = err as Record<string, Object>;
    return typeof rec['status'] === 'number' && rec['status'] === 404;
  }

  private async loadAll(): Promise<void> {
    const serial = ++this.loadSerial;
    const owner = this.model;
    const scope = owner.scope();
    const generation = owner.connectionGeneration();
    if (!owner.profile.id || (!owner.connected && !owner.demo)) {
      this.loading = false;
      this.loadError = 'offline';
      return;
    }
    this.loading = true;
    this.loadError = '';
    this.unsupported = false;
    try {
      const repo = owner.repo();
      const daysPromise = repo.call('/api/v1/usage/days?days=84');
      const hoursPromise = repo.call('/api/v1/usage/hours?days=7').catch((err: Object): Record<string, Object> => {
        if (this.is404(err)) return {};
        return {};
      });
      const projectsPromise = repo.call('/api/v1/usage/projects').catch((): Record<string, Object> => ({}));

      const rawDays = await daysPromise;
      if (serial !== this.loadSerial || !owner.owns(generation, scope)) return;
      const parsedDays = parseUsageDaysResponse(rawDays);
      this.daysData = parsedDays;
      this.heatmap = buildTokenHeatmap(parsedDays.days, 12);

      const rawHours = await hoursPromise;
      if (serial !== this.loadSerial || !owner.owns(generation, scope)) return;
      if (Object.keys(rawHours).length > 0) {
        this.hoursData = parseUsageHoursResponse(rawHours);
      }

      const rawProjects = await projectsPromise;
      if (serial !== this.loadSerial || !owner.owns(generation, scope)) return;
      if (Object.keys(rawProjects).length > 0) {
        const parsedProjects = parseUsageProjectsResponse(rawProjects);
        this.projectsData = parsedProjects.ranges.length > 0 ? parsedProjects : undefined;
      } else {
        this.projectsData = undefined;
      }
      this.loading = false;
    } catch (err) {
      if (serial !== this.loadSerial || !owner.owns(generation, scope)) return;
      this.loading = false;
      if (this.is404(err as Object)) {
        this.unsupported = true;
        this.loadError = '';
      } else {
        this.loadError = 'failed';
      }
    }
  }

  private async openProject(project: UsageProject): Promise<void> {
    Haptics.tap();
    this.selectedProject = project;
    this.projectDetail = undefined;
    this.detailError = '';
    this.detailLoading = true;
    const serial = ++this.detailSerial;
    const owner = this.model;
    const scope = owner.scope();
    const generation = owner.connectionGeneration();
    try {
      const raw = await owner.repo().call('/api/v1/usage/projects/detail?key=' + encodeURIComponent(project.key));
      if (serial !== this.detailSerial || !owner.owns(generation, scope)) return;
      const parsed = parseUsageProjectDetailResponse(raw);
      if (parsed) {
        this.projectDetail = parsed;
      } else {
        this.detailError = 'failed';
      }
    } catch (_) {
      if (serial !== this.detailSerial || !owner.owns(generation, scope)) return;
      this.detailError = 'failed';
    } finally {
      if (serial === this.detailSerial) {
        this.detailLoading = false;
      }
    }
  }

  private closeProjectDetail(): void {
    this.detailSerial++;
    this.selectedProject = undefined;
    this.projectDetail = undefined;
    this.detailLoading = false;
    this.detailError = '';
  }

  private projectDisplayName(project: UsageProject): string {
    if (project.kind === 'home') return tr('token_usage_project_home');
    if (project.kind === 'temp') return tr('token_usage_project_temp');
    if (project.kind === 'assistant') return tr('token_usage_project_assistant');
    if (project.kind === 'unknown' || !project.name.trim()) return tr('token_usage_project_unknown');
    return project.name;
  }

  private summaryTokens(): UsageTokens {
    if (this.pickedDate && this.daysData) {
      const hit = this.daysData.days.find((day) => day.date === this.pickedDate);
      if (hit) return hit.tokens;
    }
    if (this.daysData && this.daysData.days.length > 0) {
      return sumLastDaysTokens(this.daysData.days, 84);
    }
    return this.hoursData?.total ?? emptyUsageTokens();
  }

  private heatCellColor(level: number): ResourceColor {
    if (level <= 0) return Colors.control;
    return Colors.link;
  }

  private heatCellOpacity(level: number): number {
    if (level <= 0) return 1;
    if (level === 1) return 0.22;
    if (level === 2) return 0.45;
    if (level === 3) return 0.7;
    return 1.0;
  }

  private rangeLabel(range: UsageProjectRangeKey): string {
    if (range === 'today') return tr('token_usage_range_today');
    if (range === '3d') return tr('token_usage_range_3d');
    return tr('token_usage_range_7d');
  }

  @Builder summaryStat(label: string, value: string, highlight: boolean = false) {
    Column({ space: Tokens.spaceXxs }) {
      Text(label)
        .fontSize(Tokens.typeLabel)
        .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
        .fontColor(Colors.secondary)
        .maxLines(1)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
      Text(value)
        .fontSize(Tokens.typeHeading)
        .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeHeading))
        .fontWeight(FontWeight.Bold)
        .fontColor(highlight ? Colors.positive : Colors.ink)
        .fontFeature('tnum 1')
        .maxLines(1)
    }
    .layoutWeight(1)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder totalsSummaryCard() {
    Column({ space: Tokens.spaceM }) {
      Row({ space: Tokens.spaceS }) {
        Column({ space: Tokens.spaceXxs }) {
          Text(this.pickedDate ? this.pickedDate : tr('token_usage_total_12w'))
            .fontSize(Tokens.typeCaption)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeCaption))
            .fontColor(Colors.secondary)
            .maxLines(1)
          Text(TokenFormat.count(this.summaryTokens().total))
            .fontSize(Tokens.typeDisplay)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeDisplay))
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.ink)
            .fontFeature('tnum 1')
            .maxLines(1)
        }
        .layoutWeight(1)
        .alignItems(HorizontalAlign.Start)

        PersonaStatusPill({
          tone: cacheHitRatePercent(this.summaryTokens()) >= 75 ? 'unread' : 'neutral',
          label: tr('token_usage_hit_rate') + ' ' + String(cacheHitRatePercent(this.summaryTokens())) + '%',
          identity: 'token.usage.hit_rate'
        })
      }
      .width('100%')
      .alignItems(VerticalAlign.Center)

      Divider().strokeWidth(Tokens.divider).color(Colors.divider)

      Row({ space: Tokens.spaceS }) {
        this.summaryStat(tr('token_usage_input'), TokenFormat.count(this.summaryTokens().input))
        this.summaryStat(tr('token_usage_output'), TokenFormat.count(this.summaryTokens().output))
        this.summaryStat(tr('token_usage_cache_read'), TokenFormat.count(this.summaryTokens().cacheRead), true)
      }
      .width('100%')

      if (!this.pickedDate && this.daysData && this.daysData.days.length > 0) {
        Row({ space: Tokens.spaceS }) {
          Text(tr('token_usage_range_today') + ' ' + TokenFormat.count(sumLastDaysTokens(this.daysData.days, 1).total))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontColor(Colors.secondary)
            .fontFeature('tnum 1')
          Text('·').fontSize(Tokens.typeLabel).fontColor(Colors.tertiary)
          Text(tr('token_usage_range_7d') + ' ' + TokenFormat.count(sumLastDaysTokens(this.daysData.days, 7).total))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontColor(Colors.secondary)
            .fontFeature('tnum 1')
        }
        .width('100%')
      } else if (this.pickedDate) {
        Row({ space: Tokens.spaceS }) {
          Text(tr('token_usage_cache_write') + ' ' + TokenFormat.count(this.summaryTokens().cacheWrite))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontColor(Colors.secondary)
            .fontFeature('tnum 1')
            .layoutWeight(1)
          Button(tr('clear'), { type: ButtonType.Normal })
            .buttonStyle(ButtonStyleMode.TEXTUAL)
            .fontSize(Tokens.typeLabel)
            .fontColor(Colors.link)
            .constraintSize({ minWidth: Tokens.controlBar, minHeight: Tokens.controlBar })
            .padding({ left: Tokens.spaceS, right: Tokens.spaceS })
            .onClick(() => {
              Haptics.tap();
              this.pickedDate = '';
            })
            .id('token.usage.clear_day')
        }
        .width('100%')
        .alignItems(VerticalAlign.Center)
      }
    }
    .width('100%')
    .padding(Tokens.spaceL)
    .backgroundColor(Colors.surface)
    .borderRadius(Tokens.radiusCard)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.radiusCard })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
    .id('token.usage.summary')
  }

  @Builder heatmapCard() {
    Column({ space: Tokens.spaceS }) {
      PersonaSectionLabel({
        text: tr('token_usage_heatmap_title'),
        trailing: this.pickedDate ? this.pickedDate : tr('token_usage_heatmap_tap_hint')
      })

      Column({ space: Tokens.spaceS }) {
        if (this.heatmap.months.length > 0) {
          Row({ space: Tokens.spaceXs }) {
            ForEach(this.heatmap.months, (m: HeatmapMonthLabel) => {
              Text(m.label)
                .fontSize(Tokens.typeLabel)
                .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
                .fontColor(Colors.secondary)
                .layoutWeight(1)
                .maxLines(1)
            }, (m: HeatmapMonthLabel) => String(m.column) + ':' + m.label)
          }
          .width('100%')
        }

        Row({ space: Tokens.spaceXs }) {
          ForEach(this.heatmap.columns, (col: HeatmapCell[], colIdx: number) => {
            Column({ space: Tokens.spaceXs }) {
              ForEach(col, (cell: HeatmapCell, rowIdx: number) => {
                if (cell.level < 0 || !cell.date) {
                  Blank().width('100%').aspectRatio(1)
                } else {
                  Button({ type: ButtonType.Normal }) {
                    Row().width('100%').height('100%')
                  }
                  .width('100%')
                  .aspectRatio(1)
                  .padding(0)
                  .backgroundColor(this.heatCellColor(cell.level))
                  .opacity(this.heatCellOpacity(cell.level))
                  .borderRadius(Tokens.radiusBadge / 2)
                  .border({
                    width: this.pickedDate === cell.date ? 2 : cell.isToday ? 1 : 0,
                    color: this.pickedDate === cell.date || cell.isToday ? Colors.ink : Color.Transparent,
                    radius: Tokens.radiusBadge / 2
                  })
                  .accessibilityText(cell.date + ' · ' + TokenFormat.count(cell.tokens.total))
                  .onClick(() => {
                    Haptics.tap();
                    this.pickedDate = this.pickedDate === cell.date ? '' : (cell.date || '');
                  })
                  .id('token.usage.cell.' + cell.date)
                }
              }, (cell: HeatmapCell, rowIdx: number) => String(colIdx) + ':' + String(rowIdx) + ':' + (cell.date || 'future'))
            }
            .layoutWeight(1)
          }, (col: HeatmapCell[], colIdx: number) => String(colIdx))
        }
        .width('100%')
        .id('token.usage.heatmap.grid')

        Row({ space: Tokens.spaceXs }) {
          Blank().layoutWeight(1)
          Text(tr('token_usage_less'))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontColor(Colors.secondary)
          ForEach([0, 1, 2, 3, 4], (level: number) => {
            Rect()
              .width(10)
              .height(10)
              .radius(2)
              .fill(this.heatCellColor(level))
              .opacity(this.heatCellOpacity(level))
          }, (level: number) => String(level))
          Text(tr('token_usage_more'))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontColor(Colors.secondary)
        }
        .width('100%')
        .alignItems(VerticalAlign.Center)
      }
      .width('100%')
      .padding(Tokens.spaceL)
      .backgroundColor(Colors.surface)
      .borderRadius(Tokens.radiusCard)
      .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.radiusCard })
      .shadow(PhoneStyle.cardElevation(this.env.dark))
    }
    .width('100%')
    .alignItems(HorizontalAlign.Start)
  }

  @Builder rangeSegmentPicker() {
    Row({ space: Tokens.spaceXxs }) {
      ForEach(['today', '3d', '7d'], (key: UsageProjectRangeKey) => {
        Button({ type: ButtonType.Normal }) {
          Text(this.rangeLabel(key))
            .fontSize(Tokens.typeCaption)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeCaption))
            .fontWeight(this.projectRange === key ? FontWeight.Bold : FontWeight.Medium)
            .fontColor(this.projectRange === key ? Colors.ink : Colors.secondary)
            .maxLines(1)
        }
        .layoutWeight(1)
        .height(32)
        .responseRegion({ x: 0, y: -6, width: '100%', height: Tokens.controlBar })
        .padding({ left: Tokens.spaceS, right: Tokens.spaceS })
        .backgroundColor(this.projectRange === key ? Colors.surface : Color.Transparent)
        .borderRadius(Tokens.radiusControl - 1)
        .shadow(this.projectRange === key ? PhoneStyle.cardElevation(this.env.dark) : { radius: 0, color: Color.Transparent })
        .accessibilitySelected(this.projectRange === key)
        .onClick(() => {
          Haptics.selection();
          Motion.run(this.getUIContext(), Motion.state(this.env.reduceMotion), () => {
            this.projectRange = key;
          });
        })
        .id('token.usage.range.' + key)
      }, (key: UsageProjectRangeKey) => key)
    }
    .width('100%')
    .padding(3)
    .backgroundColor(Colors.control)
    .borderRadius(Tokens.radiusControl)
  }

  @Builder progressMeter(fraction: number) {
    Stack({ alignContent: Alignment.Start }) {
      Row().width('100%').height(6).borderRadius(3).backgroundColor(Colors.control)
      Row()
        .width(String(Math.round(Math.max(0, Math.min(1, fraction)) * 100)) + '%')
        .height(6)
        .borderRadius(3)
        .backgroundColor(Colors.link)
    }
    .width('100%')
    .height(6)
    .clip(true)
  }

  @Builder projectsSection() {
    if (this.projectsData) {
      Column({ space: Tokens.spaceS }) {
        PersonaSectionLabel({ text: tr('token_usage_by_project') })

        Column({ space: Tokens.spaceM }) {
          this.rangeSegmentPicker()

          if (rankProjectsForRange(this.projectsData, this.projectRange).length === 0) {
            Text(tr('token_usage_no_projects'))
              .fontSize(Tokens.typeSecondary)
              .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeSecondary))
              .fontColor(Colors.secondary)
              .padding({ top: Tokens.spaceS, bottom: Tokens.spaceS })
              .width('100%')
              .textAlign(TextAlign.Center)
          } else {
            Column({ space: Tokens.spaceM }) {
              ForEach(rankProjectsForRange(this.projectsData, this.projectRange), (item: RankedUsageProject) => {
                Button({ type: ButtonType.Normal }) {
                  Column({ space: Tokens.spaceXs }) {
                    Row({ space: Tokens.spaceS }) {
                      Text(this.projectDisplayName(item.project))
                        .fontSize(Tokens.typeSecondary)
                        .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeSecondary))
                        .fontWeight(FontWeight.Medium)
                        .fontColor(Colors.ink)
                        .maxLines(1)
                        .textOverflow({ overflow: TextOverflow.Ellipsis })
                        .layoutWeight(1)
                      Text(TokenFormat.count(item.project.tokens.total) + ' · ' + formatSharePercent(item.sharePercent))
                        .fontSize(Tokens.typeCaption)
                        .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeCaption))
                        .fontColor(Colors.secondary)
                        .fontFeature('tnum 1')
                        .maxLines(1)
                      SymbolGlyph(Symbols.expand)
                        .fontSize('12vp')
                        .fontColor([Colors.tertiary])
                    }
                    .width('100%')
                    .alignItems(VerticalAlign.Center)

                    this.progressMeter(item.progressFraction)
                  }
                  .width('100%')
                }
                .width('100%')
                .constraintSize({ minHeight: Tokens.controlBar })
                .padding({ top: Tokens.spaceXs, bottom: Tokens.spaceXs })
                .backgroundColor(Color.Transparent)
                .onClick(() => {
                  this.openProject(item.project).catch(() => undefined);
                })
                .id('token.usage.project.' + item.project.key)
              }, (item: RankedUsageProject) => item.project.key + ':' + String(item.project.tokens.total))
            }
            .width('100%')
          }
        }
        .width('100%')
        .padding(Tokens.spaceL)
        .backgroundColor(Colors.surface)
        .borderRadius(Tokens.radiusCard)
        .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.radiusCard })
        .shadow(PhoneStyle.cardElevation(this.env.dark))
      }
      .width('100%')
      .alignItems(HorizontalAlign.Start)
    }
  }

  @Builder breakdownPartsCard(title: string, parts: RankedUsagePart[], fallbackName: string) {
    if (parts.length > 0) {
      Column({ space: Tokens.spaceS }) {
        PersonaSectionLabel({ text: title })
        Column({ space: Tokens.spaceM }) {
          ForEach(parts, (entry: RankedUsagePart) => {
            Column({ space: Tokens.spaceXs }) {
              Row({ space: Tokens.spaceS }) {
                Text(entry.part.name || entry.part.key || fallbackName)
                  .fontSize(Tokens.typeSecondary)
                  .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeSecondary))
                  .fontColor(Colors.ink)
                  .maxLines(1)
                  .textOverflow({ overflow: TextOverflow.Ellipsis })
                  .layoutWeight(1)
                Text(TokenFormat.count(entry.part.tokens.total) + ' · ' + formatSharePercent(entry.sharePercent))
                  .fontSize(Tokens.typeCaption)
                  .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeCaption))
                  .fontColor(Colors.secondary)
                  .fontFeature('tnum 1')
                  .maxLines(1)
              }
              .width('100%')
              this.progressMeter(entry.progressFraction)
            }
            .width('100%')
          }, (entry: RankedUsagePart) => entry.part.key + ':' + String(entry.part.tokens.total))
        }
        .width('100%')
        .padding(Tokens.spaceL)
        .backgroundColor(Colors.surface)
        .borderRadius(Tokens.radiusCard)
        .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.radiusCard })
        .shadow(PhoneStyle.cardElevation(this.env.dark))
      }
      .width('100%')
      .alignItems(HorizontalAlign.Start)
    }
  }

  @Builder projectDetailBody(project: UsageProject) {
    Scroll() {
      Column({ space: Tokens.spaceL }) {
        this.rangeSegmentPicker()

        if (this.detailLoading && !this.projectDetail) {
          Row() { LoadingProgress().width(Tokens.controlCompact).height(Tokens.controlCompact) }
            .width('100%')
            .justifyContent(FlexAlign.Center)
            .padding(Tokens.spaceXl)
        } else if (this.detailError && !this.projectDetail) {
          Text(tr(this.detailError))
            .fontSize(Tokens.typeSecondary)
            .fontColor(Colors.secondary)
            .padding(Tokens.spaceL)
            .width('100%')
            .textAlign(TextAlign.Center)
        } else if (this.projectDetail) {
          this.projectDetailBreakdown(this.projectDetail)
        }
      }
      .width('100%')
      .padding({ left: Tokens.spaceL, right: Tokens.spaceL, top: Tokens.spaceM, bottom: Tokens.spaceXl })
      .alignItems(HorizontalAlign.Start)
    }
    .width('100%')
    .layoutWeight(1)
    .align(Alignment.TopStart)
    .id('token.usage.project.detail')
  }

  @Builder projectDetailBreakdown(detail: UsageProjectDetailResponse) {
    if (selectProjectBreakdown(detail, this.projectRange)) {
      Column({ space: Tokens.spaceL }) {
        this.projectDetailSummaryCard(selectProjectBreakdown(detail, this.projectRange)!)
        this.breakdownPartsCard(
          tr('token_usage_by_machine'),
          rankUsageParts(selectProjectBreakdown(detail, this.projectRange)!.machines),
          tr('machines')
        )
        this.breakdownPartsCard(
          tr('token_usage_by_model'),
          rankUsageParts(selectProjectBreakdown(detail, this.projectRange)!.models),
          tr('model')
        )
      }
      .width('100%')
    }
  }

  @Builder projectDetailSummaryCard(b: UsageProjectBreakdown) {
    Column({ space: Tokens.spaceM }) {
      Row({ space: Tokens.spaceS }) {
        Text(TokenFormat.count(b.total.total))
          .fontSize(Tokens.typeDisplay)
          .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeDisplay))
          .fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink)
          .fontFeature('tnum 1')
          .layoutWeight(1)
        PersonaStatusPill({
          tone: b.cacheHit >= 0.75 ? 'unread' : 'neutral',
          label: tr('token_usage_hit_rate') + ' ' + String(Math.round(b.cacheHit * 100)) + '%'
        })
      }
      .width('100%')

      Row({ space: Tokens.spaceS }) {
        this.summaryStat(tr('token_usage_share'), formatSharePercent(b.share * 100))
        this.summaryStat(tr('token_usage_input'), TokenFormat.count(b.total.input))
        this.summaryStat(tr('token_usage_output'), TokenFormat.count(b.total.output))
      }
      .width('100%')
    }
    .width('100%')
    .padding(Tokens.spaceL)
    .backgroundColor(Colors.surface)
    .borderRadius(Tokens.radiusCard)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.radiusCard })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
  }

  build() {
    Column({ space: 0 }) {
      if (this.selectedProject) {
        PersonaSheetHeader({
          title: this.projectDisplayName(this.selectedProject),
          subtitle: this.selectedProject.host || '',
          leadingMark: 'navigationBack',
          closeLabel: tr('back'),
          closeIdentity: 'token.usage.detail.back',
          showDivider: true,
          onClose: () => this.closeProjectDetail()
        })
        this.projectDetailBody(this.selectedProject)
      } else {
        PersonaSheetHeader({
          title: tr('token_usage_title'),
          closeIdentity: 'token.usage.close',
          showDivider: true,
          onClose: () => this.onClose()
        })

        if (this.loading && !this.daysData) {
          Column() {
            LoadingProgress().width(Tokens.controlCompact).height(Tokens.controlCompact)
          }
          .width('100%')
          .layoutWeight(1)
          .justifyContent(FlexAlign.Center)
          .alignItems(HorizontalAlign.Center)
          .id('token.usage.loading')
        } else if (this.unsupported) {
          Column({ space: Tokens.spaceM }) {
            SymbolGlyph(Symbols.health)
              .fontSize('32vp')
              .fontColor([Colors.secondary])
            Text(tr('token_usage_unsupported'))
              .fontSize(Tokens.typeSecondary)
              .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeSecondary))
              .fontColor(Colors.secondary)
              .textAlign(TextAlign.Center)
          }
          .width('100%')
          .layoutWeight(1)
          .padding(Tokens.spaceXl)
          .justifyContent(FlexAlign.Center)
          .alignItems(HorizontalAlign.Center)
          .id('token.usage.unsupported')
        } else if (this.loadError && !this.daysData) {
          Column({ space: Tokens.spaceM }) {
            Text(tr(this.loadError))
              .fontSize(Tokens.typeSecondary)
              .fontColor(Colors.secondary)
              .textAlign(TextAlign.Center)
            Button(tr('retry'), { type: ButtonType.Capsule })
              .height(Tokens.controlCompact)
              .padding({ left: Tokens.spaceL, right: Tokens.spaceL })
              .backgroundColor(Colors.action)
              .fontColor(Colors.onAction)
              .onClick(() => {
                this.loadAll().catch(() => undefined);
              })
          }
          .width('100%')
          .layoutWeight(1)
          .padding(Tokens.spaceXl)
          .justifyContent(FlexAlign.Center)
          .alignItems(HorizontalAlign.Center)
        } else {
          Scroll() {
            Column({ space: Tokens.spaceL }) {
              this.totalsSummaryCard()
              if (this.heatmap.columns.length > 0) {
                this.heatmapCard()
              }
              this.projectsSection()
            }
            .width('100%')
            .padding({ left: Tokens.spaceL, right: Tokens.spaceL, top: Tokens.spaceM, bottom: Tokens.spaceXl })
            .alignItems(HorizontalAlign.Start)
          }
          .width('100%')
          .layoutWeight(1)
          .align(Alignment.TopStart)
        }
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(Colors.sheetBackground)
    .id('token.usage.sheet')
  }
}
