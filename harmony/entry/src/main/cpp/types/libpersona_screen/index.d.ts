export interface ScreenDecoder { readonly nativeDecoder: number }
interface VideoBridge {
  supports(codec: string): boolean;
  create(codec: string, surface: string, width: number, height: number): ScreenDecoder | undefined;
  submit(decoder: ScreenDecoder, bytes: ArrayBuffer, pts: number, flags: number): number;
  render(decoder: ScreenDecoder): number;
  close(decoder: ScreenDecoder): void;
}
declare const bridge: VideoBridge;
export default bridge;
