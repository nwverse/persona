import { Tokens } from './Tokens';
import { Colors } from './Colors';

type PersonaButtonRole = 'primary' | 'secondary' | 'quiet' | 'chrome';

/** An ArkUI attribute modifier keeps native Button behavior and one set of style roles across modules. */
class PersonaButtonModifier implements AttributeModifier<ButtonAttribute> {
  private role: PersonaButtonRole;
  constructor(role: PersonaButtonRole) { this.role = role; }
  applyNormalAttribute(button: ButtonAttribute): void {
    if (this.role === 'chrome') {
      button.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).fontColor(Colors.ink)
        .width(Tokens.chrome).height(Tokens.chrome).padding(0).borderRadius(Tokens.card)
        .responseRegion({ x: -Tokens.fine, y: -Tokens.fine, width: Tokens.touch, height: Tokens.touch });
      return;
    }
    button.constraintSize({ minHeight: Tokens.touch }).fontSize(Tokens.body)
      .labelStyle({ maxLines: 1, overflow: TextOverflow.Ellipsis })
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.gap });
    if (this.role === 'primary') button.buttonStyle(ButtonStyleMode.EMPHASIZED).backgroundColor(Colors.action)
      .fontColor(Colors.onAction).fontWeight(FontWeight.Medium).borderRadius(Tokens.capsule);
    else if (this.role === 'secondary') button.buttonStyle(ButtonStyleMode.NORMAL).backgroundColor(Colors.control)
      .fontColor(Colors.link).borderRadius(Tokens.capsule);
    else button.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).fontColor(Colors.link)
      .padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.gap, bottom: Tokens.gap });
  }
}

export class ButtonStyles {
  static readonly primary: AttributeModifier<ButtonAttribute> = new PersonaButtonModifier('primary');
  static readonly secondary: AttributeModifier<ButtonAttribute> = new PersonaButtonModifier('secondary');
  static readonly quiet: AttributeModifier<ButtonAttribute> = new PersonaButtonModifier('quiet');
  static readonly chrome: AttributeModifier<ButtonAttribute> = new PersonaButtonModifier('chrome');
}
