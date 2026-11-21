import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { ButtonStyles } from '../design/Controls';
import { NativeDictation, DictationListener } from '../platform/NativeDictation';

@ComponentV2
export struct PhoneDictationControl {
  @Require @Param owner: string;
  @Param active: boolean = true;
  @Param language: string = 'default';
  @Event allowed: (owner: string) => boolean = (_owner: string) => false;
  @Event insert: (text: string, owner: string) => boolean = (_text: string, _owner: string) => false;
  @Event keyboard: () => void = () => {};
  @Local shown: boolean = false;
  @Local phase: string = '';
  @Local transcript: string = '';
  @Local message: string = '';
  private run: NativeDictation | undefined;
  private keyboardOwner: string = '';

  private close(): void {
    const run = this.run;
    this.run = undefined;
    this.shown = false;
    this.phase = '';
    this.transcript = '';
    this.message = '';
    run?.cancel();
  }

  @Monitor('owner', 'active') ownerChanged(): void {
    this.keyboardOwner = '';
    this.close();
  }

  aboutToDisappear(): void {
    this.keyboardOwner = '';
    this.close();
  }

  private start(): void {
    if (this.shown || !this.active || !this.allowed(this.owner)) return;
    this.transcript = '';
    this.message = '';
    this.keyboardOwner = '';
    this.shown = true;
    const language = this.language === 'default' ? i18n.getSystemLanguage() : this.language;
    const context = this.getUIContext().getHostContext();
    if (!context || !language.toLowerCase().startsWith('zh')) {
      this.phase = 'error';
      this.message = 'phone_dictation_keyboard_language';
      return;
    }
    const owner = this.owner;
    this.phase = 'preparing';
    let run: NativeDictation;
    const valid = (): boolean => this.shown && this.active && this.owner === owner && this.run === run && this.allowed(owner);
    const listener: DictationListener = {
      listening: (): void => { if (valid()) this.phase = 'listening'; },
      preview: (text: string): void => { if (valid()) this.transcript = text; },
      completed: (text: string): void => {
        if (valid()) { this.transcript = text; this.phase = 'complete'; }
      },
      failed: (key: string): void => {
        if (valid()) { this.message = key; this.phase = 'error'; }
      },
      cancelled: (): void => { if (this.run === run) this.close(); }
    };
    run = new NativeDictation(listener, owner);
    this.run = run;
    run.start(context);
  }

  private accept(): void {
    if (!this.shown || this.phase !== 'complete' || !this.run) return;
    const owner = this.owner;
    if (!this.active || !this.allowed(owner)) { this.close(); return; }
    const text = this.run.takeConfirmed(owner);
    if (!text) { this.close(); return; }
    this.phase = 'inserting';
    if (this.insert(text, owner)) this.close();
    else { this.phase = 'error'; this.message = 'phone_dictation_too_long'; }
  }

  @Builder panel() {
    Scroll() {
      Column({space: Tokens.inset}) {
        if (this.phase === 'preparing' || this.phase === 'finishing') {
          LoadingProgress().width(Tokens.touch).height(Tokens.touch).color(Colors.link)
        }
        Text(tr(this.message || (this.phase === 'preparing' ? 'phone_dictation_preparing' :
          this.phase === 'finishing' ? 'phone_dictation_finishing' :
          this.phase === 'listening' ? 'phone_dictation_listening' : 'phone_dictation_review')))
          .fontSize(Tokens.body).fontColor(Colors.ink).width('100%').id('chat.dictation.status')
        if (this.transcript) Text(this.transcript).width('100%').fontSize(Tokens.body)
          .fontColor(Colors.ink).copyOption(CopyOptions.LocalDevice).id('chat.dictation.text')
        if (this.phase === 'listening') Button(tr('stop')).id('chat.dictation.stop').onClick(() => {
          this.phase = 'finishing'; this.run?.finish();
        })
        if (this.phase === 'complete') Button(tr('phone_dictation_insert')).id('chat.dictation.insert')
          .onClick(() => this.accept())
        if (this.phase === 'error') Button(tr('phone_dictation_keyboard')).id('chat.dictation.keyboard')
          .onClick(() => { this.keyboardOwner = this.owner; this.close(); })
        Button(tr('cancel')).attributeModifier(ButtonStyles.quiet).id('chat.dictation.cancel').onClick(() => this.close())
      }.width('100%').padding(Tokens.page)
    }.width('100%')
  }

  build() {
    Button({type: ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.mic')).fontSize(Tokens.symbolFont).fontColor([Colors.ink]) }
      .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0)
      .accessibilityText(tr('phone_chat_dictation')).bindTips(tr('phone_chat_dictation')).enabled(this.active)
      .id('chat.dictation').onClick(() => this.start())
      .bindSheet($$this.shown, this.panel(), {title: {title: tr('phone_chat_dictation')}, height: SheetSize.MEDIUM,
        detents: [SheetSize.MEDIUM, SheetSize.LARGE], showClose: true,
        onDisappear: () => {
          const focus = !!this.keyboardOwner && this.active && this.owner === this.keyboardOwner && this.allowed(this.keyboardOwner);
          this.keyboardOwner = ''; this.close();
          if (focus) this.keyboard();
        }})
  }
}
