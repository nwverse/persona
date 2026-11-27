import { ButtonStyles, SelectStyles } from '../design/Controls';
import { ScreenWheel, ScreenTwoFingers, screenScrollFactor } from '../core/ScreenWheel';
import { screenScrollPreference } from '../platform/ScreenScrollPreference';
import { Colors } from '../design/Colors';
import { image } from '@kit.ImageKit';
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
  private frameEpoch: number = 0;
  private rendering: boolean = false;
  private needsFrame: boolean = false;
  private presenter: ScreenPresenter = new ScreenPresenter();
  private sampleStep: number = 0;
  @Local rasterWidth: number = 1;
  @Local rasterHeight: number = 1;
  private viewingGesture: boolean = false;
  private displayedRowKey: string = '';
  private pointerDown: boolean = false;
  private keyboard: ScreenKeyboard | undefined;
  private wheel: ScreenWheel = new ScreenWheel();
  private fingerMotion: ScreenTwoFingers = new ScreenTwoFingers();
  private wheelTimer: number = -1;
  private frameTime: number = 0;
  private multiTouch: boolean = false;
  private scrolling: boolean = false;
  private wheelX: number = 0;
  private wheelY: number = 0;
  private previousY: number = 0;
  private previousTime: number = 0;
  private velocity: number = 0;
  private perPoint: number = 1;

  aboutToAppear(): void { this.mounted = true; screenScrollPreference.load(this.getUIContext().getHostContext()); this.updateVisibility(); }

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
    this.stopWheel();
    this.pointerDown = false;
    this.viewport.reset();
    this.clearFields();
    this.screen.hide();
    this.clearFrame();
  }

  @Monitor('row') rowChanged(): void { if (this.row.key() !== this.displayedRowKey) this.open(); }

  @Monitor('model.profile') profileChanged(): void { this.dismissForOwnerChange(); }

  @Monitor('model.connected') connectionChanged(): void { if (!this.model.connected) this.dismissForOwnerChange(); }

  @Monitor('model.machines') machinesChanged(): void {
    const machine: ResourceRow | undefined = this.model.machines.find((candidate: ResourceRow) => this.sameMachine(candidate));
    if (!machine || !Wire.bool(machine.data(), 'online') || !this.screen.validateOwner()) this.dismissForOwnerChange();
  }

  /** Rows opened from the Computers directory carry their Hub; the owner's own roster may not. */
  private sameMachine(candidate: ResourceRow): boolean {
    if (candidate.key() === this.row.key()) return true;
    if (candidate.group || candidate.kind !== this.row.kind || candidate.id !== this.row.id) return false;
    return (candidate.manager || this.model.profile.manager) === this.row.manager &&
      (!this.row.group || this.row.group === this.model.profile.id);
  }

  @Monitor('screen.frameRevision') frameChanged(): void { this.decodeFrame(); }
  @Monitor('screen.hasFrame') frameAvailabilityChanged(): void { if (!this.screen.hasFrame) this.clearFrame(); }

  @Monitor('screen.phase') phaseChanged(): void {
    if (!this.screen.isLive()) this.clearFrame();
    if (['idle', 'closed', 'failed', 'auth_failed', 'sharing_off'].includes(this.screen.phase)) {
      this.clearFields();
      if (this.fullscreen) this.onFullscreen(false);
    }
  }

  @Monitor('screen.controlling') controlChanged(): void {
    this.viewport.cancel();
    this.setViewingGesture(false);
    this.stopWheel();
    if (!this.screen.controlAllowed()) { this.closeKeyboard(); this.pointerDown = false; }
  }

  private open(): void {
    if (!this.isVisible()) return;
    this.displayedRowKey = this.row.key();
    this.viewingGesture = false;
    this.viewport.reset();
    this.clearFields();
    this.clearFrame();
    this.frameRatio = 1;
    this.stopWheel();
    this.pointerDown = false;
    this.screen.open(this.model, this.row).catch((): void => {});
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

  private selectedDisplayIndex(): number {
    return Math.max(0, this.screen.displays.findIndex((d: ScreenDisplay) => d.key === this.screen.selectedDisplay));
  }

  private switchDisplay(index: number): void {
    if (this.screen.displays[index]?.key === this.screen.selectedDisplay) return;
    this.pointerDown = false;
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

  private stopWheel(): void {
    if (this.wheelTimer !== -1) clearTimeout(this.wheelTimer);
    this.wheelTimer = -1; this.wheel.stop(); this.multiTouch = false; this.scrolling = false;
  }

  private paceWheel(): void {
    if (this.wheelTimer !== -1 || !this.wheel.busy) return;
    const attempt = this.screen.connectionRevision();
    this.frameTime = Date.now();
    const frame = (): void => {
      this.wheelTimer = -1;
      if (!this.isVisible() || !this.screen.controlAllowed() || attempt !== this.screen.connectionRevision()) { this.stopWheel(); return; }
      const now = Date.now(), dt = Math.min(.1, Math.max(0, (now - this.frameTime) / 1000));
      this.frameTime = now;
      const amount = this.wheel.frame(dt);
      if (amount && !this.screen.scrollFromView(this.wheelX, this.wheelY, this.frameAreaWidth, this.frameAreaHeight, amount)) { this.stopWheel(); return; }
      if (this.wheel.busy) this.wheelTimer = setTimeout(frame, 16);
    };
    this.wheelTimer = setTimeout(frame, 16);
  }

  /** Two fingers moving together scroll the computer with the wheel; a pinch zooms and pans the picture, and so does
   * every two-finger gesture without control. The viewport still sees two fingers arrive (it releases a held pointer and
   * drops a pending tap) and the lift (so the last finger never taps). True when this event belongs to the scroll. */
  private scrollWithTwoFingers(kind: string, fingers: ScreenPoint[]): boolean {
    if (fingers.length < 2) {
      if (kind === 'down') this.stopWheel(); // a new touch stops the momentum, as a finger on a trackpad does
      if (this.multiTouch) {
        if (this.scrolling && Date.now() - this.previousTime < 100) this.wheel.release(this.velocity, this.perPoint);
        this.multiTouch = false; this.scrolling = false; this.paceWheel();
      }
      return false;
    }
    const a = fingers[0], b = fingers[1], x = (a.x + b.x) / 2, y = (a.y + b.y) / 2, spread = Math.hypot(a.x - b.x, a.y - b.y), now = Date.now();
    if (!this.multiTouch) {
      this.stopWheel(); this.multiTouch = true;
      this.fingerMotion.begin(x, y, spread); this.wheel.begin(); this.previousY = y; this.previousTime = now; this.velocity = 0;
      this.perPoint = this.screen.scrollPointsInView(this.frameAreaWidth, this.frameAreaHeight) * screenScrollFactor(screenScrollPreference.choice);
      return false;
    }
    if (!this.screen.controlAllowed()) return false;
    const meaning = this.fingerMotion.moved(x, y, spread);
    if (meaning === 'pinch') return false;
    if (meaning !== 'scroll') return true; // undecided: hold the picture still
    this.scrolling = true;
    const dy = y - this.previousY, dt = Math.max(.001, (now - this.previousTime) / 1000);
    this.velocity = dy / dt; this.wheelX = x; this.wheelY = y;
    this.wheel.move(dy, this.velocity, this.perPoint); this.previousY = y; this.previousTime = now; this.paceWheel();
    return true;
  }

  private touchFrame(event: TouchEvent): void {
    if (event.type === TouchType.Cancel) {
      if (this.pointerDown) this.screen.releasePointer();
      this.pointerDown = false;
      this.stopWheel();
      this.viewport.cancel();
      this.setViewingGesture(false);
      return;
    }
    if (!this.screen.hasFrame || !this.framePixels || !event.changedTouches.length) return;
    const touch = event.changedTouches[0];
    const kind = event.type === TouchType.Down ? 'down' : event.type === TouchType.Up ? 'up' : event.type === TouchType.Move ? 'move' : 'cancel';
    const fingers: ScreenPoint[] = [];
    for (const finger of event.touches) {
      if (kind === 'up' && (finger.type === TouchType.Up || event.changedTouches.some((ended: TouchObject) => ended.id === finger.id))) continue;
      fingers.push(new ScreenPoint(finger.x, finger.y));
      if (fingers.length === 2) break;
    }
    if (this.scrollWithTwoFingers(kind, fingers)) { this.setViewingGesture(false); return; }
    this.viewport.touch(kind, new ScreenPoint(touch.x, touch.y), fingers, this.screen.controlAllowed(),
      (x: number, y: number, buttons: number): void => { this.pointerDown = this.screen.pointerAt(x, y, buttons) && buttons !== 0; },
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
    const aspect = this.framePixels ? this.frameRatio : this.screen.displayAspect() || 1.6;
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

  @Builder screenToolbar() {
    Row({ space: Tokens.gap }) {
      if (this.screen.displays.length > 1) {
        Select(this.displayOptions()).attributeModifier(SelectStyles.inline).layoutWeight(1)
          .selected(this.selectedDisplayIndex()).value(this.displayLabel(this.screen.displays[this.selectedDisplayIndex()], this.selectedDisplayIndex()))
          .id('screen.displays').onSelect((index: number) => this.switchDisplay(index))
      } else {
        Text(tr('computer_screen_main_display')).fontSize(Tokens.meta).fontColor(Colors.secondary).layoutWeight(1)
      }
      this.utilityButton('screen.fullscreen.enter', tr('computer_screen_fullscreen'), $r('sys.symbol.full_screen'), (): void => this.setFullscreen(true))
      this.controlButton()
    }.width('100%').padding({ top: Tokens.gap, bottom: Tokens.gap }).id('screen.tools')
      .onAreaChange((_old: Area, area: Area): void => { this.toolbarHeight = Number(area.height); })
  }

  @Builder desktop() {
      Stack({ alignContent: Alignment.Center }) {
        Stack({ alignContent: Alignment.TopStart }) {
          if (this.screen.isLive() && this.screen.hasFrame && this.framePixels) {
            Image(this.framePixels).id('screen.frame')
              .width(this.getUIContext().px2vp(this.rasterWidth)).height(this.getUIContext().px2vp(this.rasterHeight))
              .objectFit(ImageFit.Fill).autoResize(false).interpolation(ImageInterpolation.Low)
              .scale({ x: this.getUIContext().vp2px(this.viewport.displayed.width) / this.rasterWidth,
                y: this.getUIContext().vp2px(this.viewport.displayed.height) / this.rasterHeight, centerX: 0, centerY: 0 })
              .translate({ x: this.pixelAligned(this.viewport.displayed.x), y: this.pixelAligned(this.viewport.displayed.y) })
              .contentTransition(ContentTransitionEffect.IDENTITY).draggable(false).hitTestBehavior(HitTestMode.None)
          }
        }.width('100%').height('100%').clip(true).backgroundColor(this.fullscreen ? Color.Black : Colors.canvas).id('screen.viewport').onTouch((event: TouchEvent) => this.touchFrame(event))
        if (!this.screen.hasFrame || !this.framePixels) {
          if (['live', 'authenticating', 'connecting', 'checking'].includes(this.screen.phase)) Column({ space: Tokens.gap }) {
            LoadingProgress().width(Tokens.touch).height(Tokens.touch)
            Text(tr(this.screen.messageKey)).id('screen.status').fontSize(Tokens.body).fontColor($r('sys.color.font_secondary')).textAlign(TextAlign.Center)
          }.align(Alignment.Center).padding(Tokens.page)
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
        if (this.screen.hasFrame && this.viewport.zoomPercent > 100) {
          Button(String(this.viewport.zoomPercent) + '%').id('screen.zoom.reset')
            .height(Tokens.touch).width(76).fontSize(Tokens.meta).backgroundColor('#CC24262A').fontColor(Color.White)
            .accessibilityText(tr('computer_screen_fit')).bindTips(tr('computer_screen_fit')).position({ right: Tokens.page, bottom: Tokens.page })
            .onClick((): void => this.fitPicture())
        }
      }.height(this.fullscreen || !this.screen.isLive() ? '100%' : this.inlineFrameHeight()).width('100%')
        .borderRadius(this.fullscreen || !this.screen.isLive() ? 0 : Tokens.inset).clip(true)
        .backgroundColor(this.fullscreen ? Color.Black : Colors.canvas)
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
