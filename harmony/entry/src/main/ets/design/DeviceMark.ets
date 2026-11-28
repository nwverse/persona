import { Colors } from './Colors';
import { Symbols } from './Symbols';
import { Tokens } from './Tokens';

// Single-color marks tinted to the current appearance. Edge has no openly licensed mark and uses the fallback glyph.
const systemMarks: Map<string, Resource> = new Map<string, Resource>([
  ['chrome', $r('app.media.device_mark_chrome')],
  ['firefox', $r('app.media.device_mark_firefox')],
  ['safari', $r('app.media.device_mark_safari')],
  ['alma', $r('app.media.device_mark_alma')],
  ['alpine', $r('app.media.device_mark_alpine')],
  ['android', $r('app.media.device_mark_android')],
  ['macos', $r('app.media.device_mark_apple')],
  ['ios', $r('app.media.device_mark_apple')],
  ['ipados', $r('app.media.device_mark_apple')],
  ['arch', $r('app.media.device_mark_arch')],
  ['centos', $r('app.media.device_mark_centos')],
  ['debian', $r('app.media.device_mark_debian')],
  ['fedora', $r('app.media.device_mark_fedora')],
  ['linux', $r('app.media.device_mark_linux')],
  ['nixos', $r('app.media.device_mark_nixos')],
  ['opensuse', $r('app.media.device_mark_opensuse')],
  ['redhat', $r('app.media.device_mark_redhat')],
  ['rocky', $r('app.media.device_mark_rocky')],
  ['ubuntu', $r('app.media.device_mark_ubuntu')],
  ['windows', $r('app.media.device_mark_windows')]
]);

@ComponentV2
export struct DeviceMark {
  @Param family: string = '';
  @Param fallback: Resource = Symbols.device;
  @Param tint: ResourceColor = Colors.ink;
  @Param markSize: number = Tokens.symbol;

  build() {
    if (systemMarks.has(this.family)) {
      Image(systemMarks.get(this.family)).width(this.markSize).height(this.markSize)
        .objectFit(ImageFit.Contain).fillColor(this.tint).accessibilityLevel('no')
    } else {
      SymbolGlyph(this.family === 'harmonyos' ? $r('sys.symbol.HarmonyOS') : this.fallback)
        .fontSize(String(this.markSize) + 'vp').width(this.markSize).fontColor([this.tint]).accessibilityLevel('no')
    }
  }
}
