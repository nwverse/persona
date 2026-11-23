import { inputMethod } from '@kit.IMEKit';

/** A native input connection for a remote caret. The IME owns composition and sends us committed text. */
export class ScreenKeyboard {
  private static transitions: Promise<void> = Promise.resolve();
  private controller: inputMethod.InputMethodController | undefined;
  private active: boolean = true;
  private attached: boolean = false;
  private shown: boolean = false;
  private readonly text: (value: string) => void;
  private readonly key: (keysym: number, count: number) => void;
  private readonly hidden: () => void;

  constructor(text: (value: string) => void, key: (keysym: number, count: number) => void, hidden: () => void) {
    this.text = text;
    this.key = key;
    this.hidden = hidden;
  }

  private readonly insert = (value: string): void => { if (this.active && value) this.text(value); };
  private readonly backspace = (count: number): void => { if (this.active) this.key(0xff08, count); };
  private readonly forwardDelete = (count: number): void => { if (this.active) this.key(0xffff, count); };
  private readonly enter = (_key: inputMethod.FunctionKey): void => { if (this.active) this.key(0xff0d, 1); };
  private readonly move = (direction: inputMethod.Direction): void => {
    if (!this.active) return;
    switch (direction) {
      case inputMethod.Direction.CURSOR_LEFT: this.key(0xff51, 1); break;
      case inputMethod.Direction.CURSOR_UP: this.key(0xff52, 1); break;
      case inputMethod.Direction.CURSOR_RIGHT: this.key(0xff53, 1); break;
      case inputMethod.Direction.CURSOR_DOWN: this.key(0xff54, 1); break;
    }
  };
  private readonly status = (value: inputMethod.KeyboardStatus): void => {
    if (value === inputMethod.KeyboardStatus.SHOW) this.shown = true;
    if (this.active && this.shown && value === inputMethod.KeyboardStatus.HIDE) this.hidden();
  };

  open(): Promise<void> {
    const opening = ScreenKeyboard.transitions.then(async (): Promise<void> => {
      if (!this.active) return;
      const controller = inputMethod.getController();
      this.controller = controller;
      // API 26 requires an attached client to subscribe. Show the IME only after its callbacks are ready.
      await controller.attach(false, { inputAttribute: {
        textInputType: inputMethod.TextInputType.TEXT, enterKeyType: inputMethod.EnterKeyType.NEWLINE
      } });
      this.attached = true;
      if (!this.active) return;
      controller.on('insertText', this.insert);
      controller.on('deleteLeft', this.backspace);
      controller.on('deleteRight', this.forwardDelete);
      controller.on('sendFunctionKey', this.enter);
      controller.on('moveCursor', this.move);
      controller.on('sendKeyboardStatus', this.status);
      await controller.showTextInput();
      this.shown = true;
    });
    ScreenKeyboard.transitions = opening.catch((): void => {});
    return opening;
  }

  close(): void {
    if (!this.active) return;
    this.active = false;
    // Finish an in-flight attach/detach before another remote editor binds the process-wide controller.
    ScreenKeyboard.transitions = ScreenKeyboard.transitions.then(async (): Promise<void> => {
      const controller = this.controller;
      if (!controller || !this.attached) return;
      try {
        controller.off('insertText', this.insert);
        controller.off('deleteLeft', this.backspace);
        controller.off('deleteRight', this.forwardDelete);
        controller.off('sendFunctionKey', this.enter);
        controller.off('moveCursor', this.move);
        controller.off('sendKeyboardStatus', this.status);
      } finally {
        await controller.detach();
      }
    }).catch((): void => {});
  }
}
