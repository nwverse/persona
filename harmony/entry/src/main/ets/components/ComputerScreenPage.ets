import { ButtonStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { image } from '@kit.ImageKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { systemDateTime } from '@kit.BasicServicesKit';
import { ScreenPresenter } from '../platform/ScreenPresenter';
import { ScreenKeyboard } from '../platform/ScreenKeyboard';
import { RFBFramebuffer } from '../core/RFBFramebuffer';
import { ClientModel } from '../models/ClientModel';
import { ComputerScreen } from '../models/ComputerScreen';
import { ScreenDisplay } from '../models/ScreenDisplays';
import { ScreenPoint, ScreenViewport } from '../models/ScreenViewport';
import { ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import type { AnimatorResult } from '@kit.ArkUI';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
struct ScreenLoadingStatus {
  @Require @Param messageKey: string;
  @Require @Param darkSurface: boolean;
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Local revealed: boolean = false;
  @Local phase: number = 0;
  private timer: number = -1;
  private wave: AnimatorResult | undefined;

  aboutToAppear(): void {
    this.timer = setTimeout((): void => {
      this.timer = -1;
      this.revealed = true;
      this.updateMotion();
    }, 180);
  }

  aboutToDisappear(): void {
    if (this.timer !== -1) { clearTimeout(this.timer); this.timer = -1; }
    this.revealed = false;
    this.stopMotion();
  }

  @Monitor('environment.reduceMotion') updateMotion(): void {
    if (!this.revealed || this.environment.reduceMotion) { this.stopMotion(); return; }
    if (this.wave) return;
    const wave = this.getUIContext().createAnimator({ duration: 1200, easing: 'linear', delay: 0,
      fill: 'none', direction: 'normal', iterations: -1, begin: 0, end: 1 });
    this.wave = wave;
    wave.setExpectedFrameRateRange({ min: 15, max: 30, expected: 24 });
    wave.onFrame = (value: number): void => { if (this.wave === wave) this.phase = value; };
    wave.play();
  }

  private stopMotion(): void {
    const wave = this.wave;
    this.wave = undefined;
    wave?.cancel();
  }

  @Builder dot(index: number) {
    Circle().width(5).height(5).fill(this.darkSurface ? Color.White : Colors.link)
      .opacity(this.environment.reduceMotion ? 0.72 : 0.65 + 0.35 * Math.cos(2 * Math.PI * (this.phase - index * 0.22)))
  }

  build() {
    Row({ space: Tokens.gap }) {
      if (this.revealed) {
        Row({ space: Tokens.fine }) {
          this.dot(0)
          this.dot(1)
          this.dot(2)
        }.id('screen.loading.indicator').flexShrink(0).accessibilityLevel('no-hide-descendants')
        Text(tr(this.messageKey)).id('screen.status').fontSize(Tokens.meta).fontWeight(FontWeight.Medium)
          .fontColor(this.darkSurface ? Color.White : Colors.secondary).textAlign(TextAlign.Center).flexShrink(1)
      }
    }.width('100%').height('100%').padding({ left: Tokens.page, right: Tokens.page })
      .justifyContent(FlexAlign.Center).alignItems(VerticalAlign.Center).hitTestBehavior(HitTestMode.None)
  }
}

@ComponentV2
export struct ComputerScreenPage {
  @Require @Param model: ClientModel;
  @Require @Param row: ResourceRow;
  @Param active: boolean = true;
  @Param fullscreen: boolean = false;
  @Event onClose: () => void = (): void => {};
  @Event onRotate: (landscape: boolean) => void = (_landscape: boolean): void => {};
  @Event onFullscreen: (shown: boolean) => void = (_shown: boolean): void => {};

  @Local screen: ComputerScreen = new ComputerScreen();
  @Local framePixels: image.PixelMap | undefined = undefined;
  @Local frameRatio: number = 1;
  @Local previewPixels: image.PixelMap | undefined = undefined;
  private previews = new Map<string, image.PixelMap>();
  @Local accountDraft: string = '';
  @Local passwordDraft: string = '';
  @Local frameAreaWidth: number = 0;
  @Local frameAreaHeight: number = 0;
  @Local viewport: ScreenViewport = new ScreenViewport();
  @Local keyboardShown: boolean = false;
  @Local pressed: string = '';
  @Local rememberLogin: boolean = false;
  @Local viewingWidth: number = 0;
  @Local viewingHeight: number = 0;
  @Local toolbarHeight: number = Tokens.touch + Tokens.gap * 2;

  private mounted: boolean = false;
  private openedAt: number = 0;
  private frameEpoch: number = 0;
  private rendering: boolean = false;
  private needsFrame: boolean = false;
  private presenter: ScreenPresenter = new ScreenPresenter();
  private sampleStep: number = 0;
  @Local rasterWidth: number = 1;
  @Local rasterHeight: number = 1;
  private viewingGesture: boolean = false;
  private displayedRowKey: string = '';
  private videoController: XComponentController = new XComponentController();
  private pointerDown: boolean = false;
  @Local pointerPosition: ScreenPoint | undefined = undefined;
  private keyboard: ScreenKeyboard | undefined;

  aboutToAppear(): void { this.mounted = true; this.updateVisibility(); }

  aboutToDisappear(): void {
    this.mounted = false;
    this.deactivate();
  }

  @Monitor('active') activeChanged(): void { this.updateVisibility(); }

  private updateVisibility(): void {
    if (!this.mounted) return;
    if (this.active) this.open();
    else this.deactivate();
  }

  private isVisible(): boolean { return this.mounted && this.active; }

  private deactivate(): void {
    this.displayedRowKey = '';
    this.viewingGesture = false;
    this.pointerDown = false;
    this.pointerPosition = undefined;
    this.viewport.reset();
    this.clearFields();
    this.clearPreviews();
    this.screen.hide();
    this.clearFrame();
  }

  @Monitor('row') rowChanged(): void { if (this.row.key() !== this.displayedRowKey) this.open(); }

  @Monitor('model.profile') profileChanged(): void { this.dismissForOwnerChange(); }

  @Monitor('model.connected') connectionChanged(): void { if (!this.model.connected) this.dismissForOwnerChange(); }

  @Monitor('model.machines') machinesChanged(): void {
    const machine: ResourceRow | undefined = this.model.machines.find((candidate: ResourceRow) => candidate.key() === this.row.key());
    if (!machine || !Wire.bool(machine.data(), 'online') || !this.screen.validateOwner()) this.dismissForOwnerChange();
  }

  @Monitor('screen.frameRevision') frameChanged(): void { this.decodeFrame(); }
  @Monitor('screen.hasFrame') frameAvailabilityChanged(): void { if (!this.screen.hasFrame) this.clearFrame(); }

  @Monitor('screen.phase') phaseChanged(): void {
    if (!this.screen.isLive()) { this.clearPreviews(); this.clearFrame(); }
    if (['idle', 'closed', 'failed', 'auth_failed', 'sharing_off'].includes(this.screen.phase)) {
      this.clearFields();
      if (this.fullscreen) this.onFullscreen(false);
    }
  }

  @Monitor('screen.controlling') controlChanged(): void {
    this.viewport.cancel();
    this.setViewingGesture(false);
    if (!this.screen.controlAllowed()) { this.closeKeyboard(); this.pointerDown = false; this.pointerPosition = undefined; }
  }

  @Monitor('screen.videoCodec') videoPictureChanged(): void {
    if (this.screen.videoCodec && this.isVisible()) {
      this.frameRatio = this.screen.displayAspect();
      this.viewport.layout(this.frameAreaWidth, this.frameAreaHeight, this.frameRatio);
      this.firstPictureReady(this.screen.videoCodec);
    } else {
      this.viewport.cancel();
      this.setViewingGesture(false);
    }
  }

  @Monitor('screen.videoFailed') videoFailureChanged(): void {
    if (this.screen.videoFailed && this.isVisible()) this.getUIContext().getPromptAction()
      .showToast({ message: tr('computer_screen_video_fallback') });
  }

  private open(): void {
    if (!this.isVisible()) return;
    this.openedAt = systemDateTime.getUptime(systemDateTime.TimeType.STARTUP);
    this.displayedRowKey = this.row.key();
    this.viewingGesture = false;
    this.viewport.reset();
    this.clearFields();
    this.clearFrame();
    this.clearPreviews();
    this.frameRatio = 1;
    this.pointerDown = false;
    this.pointerPosition = undefined;
    this.screen.open(this.model, this.row).catch((): void => {});
  }

  private firstPictureReady(mode: string): void {
    if (!this.openedAt) return;
    const elapsed = systemDateTime.getUptime(systemDateTime.TimeType.STARTUP) - this.openedAt;
    this.openedAt = 0;
    hilog.info(0x0000, 'PersonaScreen', 'viewer first picture %{public}s %{public}d ms', mode, elapsed);
  }

  private clearFields(): void {
    this.closeKeyboard();
    this.accountDraft = '';
    this.passwordDraft = '';
    this.rememberLogin = false;
  }

  private clearFrame(): void {
    this.frameEpoch++;
    this.needsFrame = false;
    this.framePixels = undefined;
    this.sampleStep = 0;
    this.presenter.clear();
  }

  private dismissForOwnerChange(): void {
    if (!this.isVisible()) return;
    this.deactivate();
    this.onClose();
  }

  private decodeFrame(): void {
    if (!this.isVisible() || !this.screen.isLive() || !this.screen.hasFrame) { this.clearFrame(); return; }
    this.needsFrame = true;
    if (!this.viewingGesture && !this.rendering) this.renderFrames();
  }

  private async renderFrames(): Promise<void> {
    this.rendering = true;
    try {
      while (this.needsFrame && !this.viewingGesture && this.isVisible() && this.screen.hasFrame) {
        this.needsFrame = false;
        const source = this.screen.presentationSource();
        if (!source) return;
        const epoch = this.frameEpoch, attempt = this.screen.connectionRevision();
        try {
          const step = this.previewStep(source);
          const next = await this.presenter.update(source, step);
          if (!next || epoch !== this.frameEpoch || !this.isVisible() || !this.screen.hasFrame || !this.screen.isLive()) continue;
          this.sampleStep = step;
          this.rasterWidth = Math.ceil(source.storedWidth / step); this.rasterHeight = Math.ceil(source.storedHeight / step);
          this.frameRatio = this.screen.displayAspect();
          this.viewport.layout(this.frameAreaWidth, this.frameAreaHeight, this.frameRatio);
          this.framePixels = next;
          this.firstPictureReady('rfb');
          this.previewPixels = undefined;
        } catch (_) {
          if (epoch === this.frameEpoch && !this.framePixels) this.screen.presentationFailed(attempt);
        }
      }
    } finally { this.rendering = false; }
  }

  private previewStep(source: RFBFramebuffer): number {
    const fit = Math.min(this.frameAreaWidth, this.frameAreaHeight * source.view.width / source.view.height);
    return fit > 0 && source.storedWidth >= 2 * this.getUIContext().vp2px(fit) * this.viewport.zoom ? 2 : 1;
  }

  private updateDetail(): void {
    if (this.viewingGesture || !this.framePixels) return;
    const source = this.screen.presentationSource();
    if (source && this.previewStep(source) !== this.sampleStep) this.decodeFrame();
  }

  private fitPicture(): void {
    this.viewport.reset();
    this.updateDetail();
  }

  private pixelAligned(value: number): number {
    const ctx = this.getUIContext();
    return ctx.px2vp(Math.round(ctx.vp2px(value)));
  }

  private confirmControl(): void {
    if (!this.isVisible() || !this.screen.isLive() || this.screen.controlAllowed()) return;
    const attempt = this.screen.connectionRevision(), rowKey = this.row.key();
    const target: string = this.row.title || this.row.id;
    this.getUIContext().showAlertDialog({
      title: tr('computer_screen_control_confirm_title'),
      message: tr('computer_screen_control_confirm_message') + '\n' + target,
      primaryButton: { value: tr('cancel'), action: (): void => {} },
      secondaryButton: { value: tr('computer_screen_allow_control'), action: (): void => { if (this.isVisible() && this.row.key() === rowKey && this.screen.connectionRevision() === attempt) this.screen.grantControl(); } }
    });
  }

  private submitCredentials(): void {
    if (this.screen.submitCredentials(this.accountDraft, this.passwordDraft, this.rememberLogin)) this.clearFields();
  }

  private toggleKeyboard(): void {
    if (this.keyboardShown) { this.closeKeyboard(); return; }
    if (!this.isVisible() || !this.screen.controlAllowed()) return;
    this.screen.releasePointer(); this.pointerDown = false;
    this.screen.keyboardInputFailed = false;
    this.keyboardShown = true;
    const keyboard = new ScreenKeyboard(
      (text: string): void => { this.screen.sendText(text); },
      (key: number, count: number): void => { this.screen.pressKey(key, count); },
      (): void => this.closeKeyboard());
    this.keyboard = keyboard;
    keyboard.open().catch((): void => {
      if (this.keyboard !== keyboard) return;
      this.closeKeyboard();
      this.getUIContext().getPromptAction().showToast({ message: tr('computer_screen_keyboard_unavailable') });
    });
  }

  private closeKeyboard(): void {
    this.keyboard?.close();
    this.keyboard = undefined;
    this.keyboardShown = false;
  }

  private focusCredential(field: string): void {
    if (!this.isVisible() || !this.screen.credentialsRequested) return;
    try { this.getUIContext().getFocusController().requestFocus('screen.login.' + field); }
    catch (_) { /* The destination may already be leaving the focus tree. */ }
  }

  private displayLabel(display: ScreenDisplay, index: number): string {
    return display.main ? tr('computer_screen_main_display') : tr('computer_screen_display') + ' ' + String(index + 1);
  }

  private displayOptions(): SelectOption[] {
    return this.screen.displays.map((display: ScreenDisplay, index: number): SelectOption => ({ value: this.displayLabel(display, index) }));
  }

  private clearPreviews(): void {
    this.previewPixels = undefined;
    for (const map of this.previews.values()) map.release();
    this.previews.clear();
  }

  private previewKey(display: ScreenDisplay): string {
    return [display.key, display.x, display.y, display.width, display.height,
      this.screen.remoteWidth, this.screen.remoteHeight].join('/');
  }

  private keepPreview(): void {
    const map = this.framePixels;
    const display = this.screen.displays[this.selectedDisplayIndex()];
    if (!map || !display || !this.screen.hasFrame || map.getPixelBytesNumber() > 12 * 1024 * 1024 ||
      !this.presenter.takeFront(map)) return;
    const key = this.previewKey(display);
    this.previews.get(key)?.release();
    this.previews.delete(key);
    this.previews.set(key, map);
    if (this.previews.size > 2) {
      const oldest = Array.from(this.previews.keys())[0];
      this.previews.get(oldest)?.release();
      this.previews.delete(oldest);
    }
  }

  private selectedDisplayIndex(): number {
    return Math.max(0, this.screen.displays.findIndex((d: ScreenDisplay) => d.key === this.screen.selectedDisplay));
  }

  private switchDisplay(index: number): void {
    const next = this.screen.displays[index];
    if (!next || next.key === this.screen.selectedDisplay) return;
    this.keepPreview();
    this.previewPixels = this.previews.get(this.previewKey(next));
    this.pointerDown = false;
    this.pointerPosition = undefined;
    this.viewport.reset();
    this.clearFrame();
    this.screen.selectDisplay(index);
    this.setViewingGesture(false);
  }

  private setViewingGesture(active: boolean): void {
    if (this.viewingGesture === active) return;
    this.viewingGesture = active;
    this.screen.setViewingGesture(active);
    if (!active) {
      if (this.needsFrame && !this.rendering) this.renderFrames();
      else this.updateDetail();
    }
  }

  private touchFrame(event: TouchEvent): void {
    if (event.type === TouchType.Cancel) {
      if (this.pointerDown) this.screen.releasePointer();
      this.pointerDown = false;
      this.pointerPosition = undefined;
      this.viewport.cancel();
      this.setViewingGesture(false);
      return;
    }
    if ((!this.screen.videoCodec && (!this.screen.hasFrame || !this.framePixels)) || !event.changedTouches.length) return;
    const touch = event.changedTouches[0];
    const kind = event.type === TouchType.Down ? 'down' : event.type === TouchType.Up ? 'up' : event.type === TouchType.Move ? 'move' : 'cancel';
    const fingers: ScreenPoint[] = [];
    for (const finger of event.touches) {
      if (kind === 'up' && (finger.type === TouchType.Up || event.changedTouches.some((ended: TouchObject) => ended.id === finger.id))) continue;
      fingers.push(new ScreenPoint(finger.x, finger.y));
      if (fingers.length === 2) break;
    }
    const control = this.screen.controlAllowed();
    if (control && kind === 'down' && fingers.length === 1) {
      const r = this.viewport.displayed;
      const x = (touch.x - r.x) / r.width, y = (touch.y - r.y) / r.height;
      if (x >= 0 && x < 1 && y >= 0 && y < 1) this.pointerPosition = new ScreenPoint(x, y);
    }
    if (fingers.length > 1) this.pointerPosition = undefined;
    this.viewport.touch(kind, new ScreenPoint(touch.x, touch.y), fingers, control,
      (x: number, y: number, buttons: number): void => {
        const accepted = this.screen.pointerAt(x, y, buttons);
        this.pointerDown = accepted && buttons !== 0;
        if (accepted) this.pointerPosition = new ScreenPoint(x, y);
      },
      (): void => { if (this.pointerDown) this.screen.releasePointer(); this.pointerDown = false; });
    this.viewport.publish();
    this.setViewingGesture(this.viewport.movingPicture());
  }

  @Builder machineHeading(signIn: boolean) {
    Row({ space: Tokens.inset }) {
      Stack() {
        SymbolGlyph(Symbols.machine).fontSize(Tokens.symbolFont).fontColor([Colors.ink])
      }.width(Tokens.avatar).height(Tokens.avatar).borderRadius(Tokens.inset).backgroundColor(Colors.control)
        .accessibilityLevel('no')
      Column({ space: Tokens.fine }) {
        Text(this.row.title || this.row.id).id('screen.machine.title').fontSize(Tokens.title).fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        Text(tr(signIn ? 'computer_screen_mac_sign_in' : this.screen.controlAllowed() ? 'computer_screen_controlling' : 'computer_screen_read_only'))
          .fontSize(Tokens.meta).fontColor(Colors.secondary)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (!signIn && this.screen.loginRemembered) {
        Button(tr('computer_screen_forget_login'), { type: ButtonType.Capsule }).id('screen.login.forget')
          .height(Tokens.touch).padding({ left: Tokens.gap, right: Tokens.gap }).fontSize(Tokens.meta)
          .backgroundColor(Color.Transparent).fontColor(Colors.link).onClick((): void => { this.screen.forgetLogin(); })
      }
    }.width('100%').id('screen.machine.heading')
  }

  @Builder credentialForm() {
    Scroll() {
      Column({ space: Tokens.section }) {
        this.machineHeading(true)
        Column({ space: Tokens.inset }) {
          Column() {
            Column({ space: Tokens.fine }) {
              Text(tr('computer_screen_account')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              TextInput({ text: this.accountDraft, placeholder: tr('computer_screen_account_hint') })
                .id('screen.login.account').accessibilityText(tr('computer_screen_account')).maxLength(256)
                .enterKeyType(EnterKeyType.Next).enableKeyboardOnFocus(true)
                .onAppear((): void => this.focusCredential('account'))
                .onSubmit((_key: EnterKeyType, event: SubmitEvent): void => {
                  event.keepEditableState();
                  this.focusCredential('password');
                })
                .onChange((value: string) => this.accountDraft = value).height(Tokens.touch)
                .fontSize(Tokens.body).fontColor(Colors.ink).backgroundColor(Color.Transparent).padding(0)
            }.width('100%').alignItems(HorizontalAlign.Start)
            Divider().color(Colors.divider).margin({ top: Tokens.fine, bottom: Tokens.inset })
            Column({ space: Tokens.fine }) {
              Text(tr('computer_screen_password')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              TextInput({ text: this.passwordDraft, placeholder: tr('computer_screen_password_hint') })
                .id('screen.login.password').accessibilityText(tr('computer_screen_password')).type(InputType.Password)
                .showPasswordIcon(true).maxLength(256).enterKeyType(EnterKeyType.Done).onSubmit((): void => this.submitCredentials())
                .onChange((value: string) => this.passwordDraft = value).height(Tokens.touch)
                .fontSize(Tokens.body).fontColor(Colors.ink).backgroundColor(Color.Transparent).padding(0)
            }.width('100%').alignItems(HorizontalAlign.Start)
          }.padding({ top: Tokens.inset, bottom: Tokens.fine, left: Tokens.page, right: Tokens.page })
            .width('100%').backgroundColor(Colors.control).borderRadius(Tokens.card)
          Row({ space: Tokens.inset }) {
            Column({ space: Tokens.fine }) {
              Text(tr('computer_screen_remember')).fontSize(Tokens.body).fontColor(Colors.ink)
              Text(tr('computer_screen_remember_hint')).fontSize(Tokens.caption).fontColor(Colors.secondary)
            }.alignItems(HorizontalAlign.Start).layoutWeight(1)
            Toggle({ type: ToggleType.Switch, isOn: this.rememberLogin }).id('screen.login.remember')
              .accessibilityText(tr('computer_screen_remember')).onChange((checked: boolean): void => { this.rememberLogin = checked; })
          }.width('100%').constraintSize({ minHeight: Tokens.touch })
          if (this.screen.loginNotice) this.loginNotice()
          Button(tr('computer_screen_connect'), { type: ButtonType.Capsule }).attributeModifier(ButtonStyles.primary)
            .width('100%').id('screen.login.submit').enabled(!!this.accountDraft.trim() && !!this.passwordDraft)
            .onClick(() => this.submitCredentials())
        }.width('100%')
      }.constraintSize({ maxWidth: Tokens.readingMax }).width('100%').alignItems(HorizontalAlign.Start)
        .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.page, bottom: Tokens.page })
    }.width('100%').layoutWeight(1).align(Alignment.Top).scrollBar(BarState.Auto)
  }

  @Builder loginNotice() {
    Text(tr(this.screen.loginNotice)).id('screen.login.notice').fontSize(Tokens.meta).fontColor(Colors.secondary).width('100%')
  }

  private inlineFrameHeight(): number {
    // ScreenDisplays orders the primary display first. Size the slot once for that layout;
    // ScreenViewport fits the selected picture inside it, including while awaiting its first frame.
    const reference = this.screen.displays[0];
    const width = reference?.width || this.screen.remoteWidth;
    const height = reference?.height || this.screen.remoteHeight;
    const aspect = width > 0 && height > 0 ? width / height : 1.6;
    return Math.max(1, Math.min((this.viewingWidth - 2 * Tokens.page) / aspect, this.viewingHeight - this.toolbarHeight));
  }

  @Builder shortcut(label: string, name: string, keysym: number) {
    Button(label, { type: ButtonType.Normal, stateEffect: true })
      .width(Tokens.touch).height(Tokens.touch).padding(0).borderRadius(Tokens.inset)
      .fontSize(Tokens.body).fontColor(Colors.ink).backgroundColor(Colors.control).focusOnTouch(false)
      .id('screen.key.' + name).accessibilityText(tr('computer_screen_key_' + name)).bindTips(tr('computer_screen_key_' + name))
      .onClick((): void => { this.screen.pressKey(keysym); })
  }

  @Builder keyboardTools() {
    Column({ space: Tokens.fine }) {
      Text(tr('computer_screen_shortcut_keys')).fontSize(Tokens.caption).fontColor(Colors.secondary)
      Scroll() {
        Row({ space: Tokens.fine }) {
          this.shortcut('Esc', 'escape', 0xff1b)
          this.shortcut('Tab', 'tab', 0xff09)
          this.shortcut('←', 'left', 0xff51)
          this.shortcut('↑', 'up', 0xff52)
          this.shortcut('↓', 'down', 0xff54)
          this.shortcut('→', 'right', 0xff53)
          this.shortcut('↵', 'return', 0xff0d)
        }
      }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off).align(Alignment.Start).width('100%')
      if (this.screen.keyboardInputFailed) {
        Text(tr('computer_screen_input_failed')).id('screen.keyboard.error').fontSize(Tokens.caption).fontColor(Colors.danger)
      }
    }.alignItems(HorizontalAlign.Start).padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.fine, bottom: Tokens.fine })
      .width('100%').id('screen.keyboard.editor')
  }

  private feedback(key: string, event: TouchEvent): void {
    if (event.type === TouchType.Down) this.pressed = key;
    if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressed = '';
  }

  private setFullscreen(shown: boolean): void {
    this.viewport.cancel();
    this.setViewingGesture(false);
    if (this.pointerDown) this.screen.releasePointer();
    this.pointerDown = false;
    this.pointerPosition = undefined;
    this.closeKeyboard();
    this.onFullscreen(shown);
  }

  @Builder utilityButton(key: string, label: string, symbol: Resource, action: () => void) {
    Button({ type: ButtonType.Circle, stateEffect: true }) {
      SymbolGlyph(symbol).fontSize(Tokens.symbolFont).fontColor([this.fullscreen ? Color.White : Colors.ink])
    }.width(Tokens.touch).height(Tokens.touch).padding(0)
      .backgroundColor(this.fullscreen ? '#CC24262A' : Colors.control)
      .scale({ x: this.pressed === key ? 0.93 : 1, y: this.pressed === key ? 0.93 : 1 }).animation({ duration: 100, curve: Curve.EaseOut })
      .id(key).accessibilityText(label).bindTips(label).onNeedSoftkeyboard((): boolean => key === 'screen.keyboard')
      .onTouch((e: TouchEvent) => this.feedback(key, e)).onClick(action)
  }

  @Builder controlButton() {
    Button({ type: ButtonType.Capsule, stateEffect: true }) {
      Row({ space: Tokens.fine }) {
        SymbolGlyph(this.screen.controlAllowed() ? $r('sys.symbol.square_fill') : $r('sys.symbol.hand_point_up_tap'))
          .fontSize(16).fontColor([this.screen.controlAllowed() ? Colors.danger : Colors.onAction])
        Stack() {
          Text(tr('computer_screen_control_short')).fontSize(Tokens.meta).fontWeight(FontWeight.Medium).fontColor(Colors.onAction)
            .visibility(this.screen.controlAllowed() ? Visibility.Hidden : Visibility.Visible)
          Text(tr('computer_screen_stop_short')).fontSize(Tokens.meta).fontWeight(FontWeight.Medium).fontColor(Colors.danger)
            .visibility(this.screen.controlAllowed() ? Visibility.Visible : Visibility.Hidden)
        }
      }
    }.height(Tokens.touch).padding({ left: Tokens.inset, right: Tokens.inset })
      .backgroundColor(this.screen.controlAllowed() ? Colors.control : Colors.action)
      .scale({ x: this.pressed === 'control' ? 0.95 : 1, y: this.pressed === 'control' ? 0.95 : 1 }).animation({ duration: 100, curve: Curve.EaseOut })
      .id(this.screen.controlAllowed() ? 'screen.control.stop' : 'screen.control.start')
      .accessibilityText(tr(this.screen.controlAllowed() ? 'computer_screen_release_control' : 'computer_screen_control'))
      .onTouch((e: TouchEvent) => this.feedback('control', e))
      .onClick((): void => { if (this.screen.controlAllowed()) this.screen.releaseControl(); else this.confirmControl(); })
  }

  private choosePicture(): void {
    const attempt = this.screen.connectionRevision();
    const choose = (video: boolean): void => {
      if (!this.isVisible() || attempt !== this.screen.connectionRevision()) return;
      this.screen.setVideoPreferred(video).then((saved: boolean): void => {
        if (!saved && this.isVisible() && attempt === this.screen.connectionRevision()) this.getUIContext().getPromptAction()
          .showToast({ message: tr('computer_screen_picture_save_failed') });
      });
    };
    if (this.screen.videoFailed && this.screen.videoPreferred && this.screen.canUseVideo()) {
      this.getUIContext().showAlertDialog({ title: tr('computer_screen_picture_options'), message: tr('computer_screen_video_fallback'),
        primaryButton: { value: tr('computer_screen_video_retry'), action: () => choose(true) },
        secondaryButton: { value: tr('computer_screen_video_stop'), action: () => choose(false) } });
    } else choose(!this.screen.videoPreferred);
  }

  @Builder screenToolbar() {
    Row({ space: Tokens.gap }) {
      if (this.screen.displays.length > 1) {
        Select(this.displayOptions()).layoutWeight(1).controlSize(ControlSize.NORMAL)
          .height(Tokens.touch).constraintSize({ minHeight: Tokens.touch, maxHeight: Tokens.touch })
          .font({ size: Tokens.body }).fontColor(Colors.ink).backgroundColor(Colors.control).borderRadius(Tokens.capsule)
          .optionFont({ size: Tokens.body }).selectedOptionFont({ size: Tokens.body, weight: FontWeight.Medium })
          .selected(this.selectedDisplayIndex()).value(this.displayLabel(this.screen.displays[this.selectedDisplayIndex()], this.selectedDisplayIndex()))
          .id('screen.displays').onSelect((index: number) => this.switchDisplay(index))
      } else {
        Text(tr('computer_screen_main_display')).fontSize(Tokens.meta).fontColor(Colors.secondary).layoutWeight(1)
      }
      if (this.screen.videoFailed && this.screen.videoPreferred && this.screen.canUseVideo()) {
        this.utilityButton('screen.video.options', tr('computer_screen_picture_options'), $r('sys.symbol.play'), (): void => this.choosePicture())
      } else if (this.screen.videoPreferred) {
        this.utilityButton('screen.video.stop', tr('computer_screen_video_stop'), $r('sys.symbol.pause'), (): void => this.choosePicture())
      } else if (this.screen.canUseVideo()) {
        this.utilityButton('screen.video.start', tr('computer_screen_video_start'), $r('sys.symbol.play'), (): void => this.choosePicture())
      }
      this.utilityButton('screen.fullscreen.enter', tr('computer_screen_fullscreen'), $r('sys.symbol.full_screen'), (): void => this.setFullscreen(true))
      this.controlButton()
    }.width('100%').padding({ top: Tokens.gap, bottom: Tokens.gap }).id('screen.tools')
      .onAreaChange((_old: Area, area: Area): void => { this.toolbarHeight = Number(area.height); })
  }

  @Builder desktop() {
      Stack({ alignContent: Alignment.Center }) {
        Stack({ alignContent: Alignment.TopStart }) {
          if (this.previewPixels && this.screen.isLive() && !this.framePixels) {
            Image(this.previewPixels).width('100%').height('100%').objectFit(ImageFit.Contain).opacity(0.6)
              .id('screen.preview').draggable(false).hitTestBehavior(HitTestMode.None)
          }
          if (this.screen.isLive() && this.screen.hasFrame && this.framePixels) {
            Image(this.framePixels).id('screen.frame')
              .width(this.getUIContext().px2vp(this.rasterWidth)).height(this.getUIContext().px2vp(this.rasterHeight))
              .objectFit(ImageFit.Fill).autoResize(false).interpolation(ImageInterpolation.Low)
              .scale({ x: this.getUIContext().vp2px(this.viewport.displayed.width) / this.rasterWidth,
                y: this.getUIContext().vp2px(this.viewport.displayed.height) / this.rasterHeight, centerX: 0, centerY: 0 })
              .translate({ x: this.pixelAligned(this.viewport.displayed.x), y: this.pixelAligned(this.viewport.displayed.y) })
              .contentTransition(ContentTransitionEffect.IDENTITY).draggable(false).hitTestBehavior(HitTestMode.None)
          }
          if (this.screen.videoRequested && this.screen.isLive()) {
            XComponent({ id: 'computer-screen-video', type: XComponentType.TEXTURE, controller: this.videoController })
              .width(this.viewport.fitWidth).height(this.viewport.fitHeight)
              .scale({ x: this.viewport.displayedZoom, y: this.viewport.displayedZoom, centerX: 0, centerY: 0 })
              .translate({ x: this.pixelAligned(this.viewport.displayed.x), y: this.pixelAligned(this.viewport.displayed.y) })
              .opacity(this.screen.videoCodec ? 1 : 0).hitTestBehavior(HitTestMode.None).accessibilityLevel('no')
              .id(this.screen.videoCodec ? 'screen.video.' + this.screen.videoCodec : 'screen.video.waiting')
              .onLoad((): void => this.screen.setVideoSurface(this.videoController.getXComponentSurfaceId()))
              .onDestroy((): void => this.screen.setVideoSurface(''))
          }
          if (this.pointerPosition && this.screen.controlling && (this.screen.videoCodec || this.screen.hasFrame)) {
            Path().commands('M 1 1 L 2 19 L 7 15 L 11 23 L 15 21 L 11 13 L 18 13 Z')
              .width(20).height(25).fill(Color.White).stroke(Color.Black).strokeWidth(1.5)
              .position({ x: this.viewport.displayed.x + this.pointerPosition.x * this.viewport.displayed.width,
                y: this.viewport.displayed.y + this.pointerPosition.y * this.viewport.displayed.height })
              .hitTestBehavior(HitTestMode.None).accessibilityLevel('no').id('screen.pointer')
          }
        }.width('100%').height('100%').clip(true).backgroundColor(Color.Transparent).id('screen.viewport').onTouch((event: TouchEvent) => this.touchFrame(event))
        if (!this.screen.videoCodec && (!this.screen.hasFrame || !this.framePixels)) {
          if (['live', 'authenticating', 'connecting', 'checking'].includes(this.screen.phase)) {
            ScreenLoadingStatus({ messageKey: this.screen.messageKey, darkSurface: this.screen.isLive() || this.fullscreen })
          }
          else if (!this.screen.credentialsRequested) Column({ space: Tokens.gap }) {
            Text(tr(this.screen.messageKey)).id('screen.error.' + (this.screen.failureDetail || 'status')).fontSize(Tokens.body).fontColor($r('sys.color.font_secondary')).textAlign(TextAlign.Center)
            Button(tr('computer_screen_connect')).id('screen.retry').attributeModifier(ButtonStyles.secondary).onClick(() => this.open())
          }.align(Alignment.Center).width('100%').padding(Tokens.page)
        }
        if (this.fullscreen) {
          Row({ space: Tokens.gap }) {
            if (this.screen.controlAllowed()) this.controlButton()
            this.utilityButton('screen.rotate', tr(this.frameAreaWidth < this.frameAreaHeight ? 'computer_screen_landscape' : 'computer_screen_portrait'),
              $r('sys.symbol.rectangle_portrait_rotate'), (): void => this.onRotate(this.frameAreaWidth < this.frameAreaHeight))
            this.utilityButton('screen.fullscreen.exit', tr('computer_screen_exit_fullscreen'), $r('sys.symbol.exit_full_screen'), (): void => this.setFullscreen(false))
          }.position({ top: Tokens.page, right: Tokens.page })
        }
        if (this.screen.isLive() && this.screen.controlAllowed()) {
          Row() {
            this.utilityButton('screen.keyboard', tr(this.keyboardShown ? 'computer_screen_hide_keys' : 'computer_screen_show_keys'), $r('sys.symbol.keyboard'),
              (): void => this.toggleKeyboard())
          }.position({ left: Tokens.page, bottom: Tokens.page })
        }
        if ((this.screen.videoCodec || this.screen.hasFrame) && this.viewport.zoomPercent > 100) {
          Button(String(this.viewport.zoomPercent) + '%').id('screen.zoom.reset')
            .height(Tokens.touch).width(76).fontSize(Tokens.meta).backgroundColor('#CC24262A').fontColor(Color.White)
            .accessibilityText(tr('computer_screen_fit')).bindTips(tr('computer_screen_fit')).position({ right: Tokens.page, bottom: Tokens.page })
            .onClick((): void => this.fitPicture())
        }
      }.height(this.fullscreen || !this.screen.isLive() ? '100%' : this.inlineFrameHeight()).width('100%')
        .borderRadius(this.fullscreen || !this.screen.isLive() ? 0 : Tokens.inset).clip(true)
        .backgroundColor(this.fullscreen || this.screen.isLive() ? Color.Black : Colors.canvas)
        .onAreaChange((_oldArea: Area, area: Area) => {
          this.frameAreaWidth = Number(area.width); this.frameAreaHeight = Number(area.height);
          this.viewport.layout(this.frameAreaWidth, this.frameAreaHeight, this.frameRatio);
          this.updateDetail();
        })

  }

  build() {
    Column() {
      if (this.screen.credentialsRequested) {
        this.credentialForm()
      } else {
        if (this.screen.isLive() && !this.fullscreen) {
          Column({ space: Tokens.fine }) {
            this.machineHeading(false)
            if (this.screen.loginNotice) this.loginNotice()
          }.width('100%').padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.inset })
        }
        Column() {
          this.desktop()
          if (this.screen.isLive() && !this.fullscreen) this.screenToolbar()
        }.layoutWeight(1).width('100%').alignItems(HorizontalAlign.Center)
          .padding({ left: this.fullscreen ? 0 : Tokens.page, right: this.fullscreen ? 0 : Tokens.page })
          .onAreaChange((_old: Area, area: Area): void => {
            this.viewingWidth = Number(area.width);
            this.viewingHeight = Number(area.height);
          })
        if (this.keyboardShown && this.screen.isLive() && this.screen.controlAllowed()) this.keyboardTools()
      }
    }.width('100%').height('100%').backgroundColor(this.fullscreen ? Color.Black : Colors.canvas)
  }
}
