import { util } from '@kit.ArkTS';
import { common } from '@kit.AbilityKit';
import { image } from '@kit.ImageKit';
import { sameMessageImageSource } from '../core/MessageImageGallery';
import type { ConversationPhotoRequest, MessageImageGalleryEntry } from '../core/MessageImageGallery';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { loadMessageImage } from '../models/MessageImage';
import { readThumbnail, thumbnailCachePath } from '../platform/MessageThumbnailCache';

/** A modal over the conversation. Only the selected original is decoded. */
@ComponentV2
export struct ConversationPhotoViewer {
  @Require @Param request: ConversationPhotoRequest;
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Event onClose: () => void = () => {};
  @Local index: number = 0;
  @Local pixels: image.PixelMap | undefined;
  @Local ratio: number = 1;
  @Local loading: boolean = false;
  @Local failed: boolean = false;
  @Local full: boolean = false;
  @Local animationSource: string = '';
  @Local zoom: number = 1;
  @Local shiftX: number = 0;
  @Local shiftY: number = 0;
  @Local viewportWidth: number = 0;
  @Local viewportHeight: number = 0;
  private startZoom: number = 1;
  private startX: number = 0;
  private startY: number = 0;
  private ticket: number = 0;
  private mounted: boolean = false;

  aboutToAppear(): void {
    this.mounted = true; this.index = this.request.gallery.selectedIndex; void this.load();
  }
  aboutToDisappear(): void {
    this.mounted = false; this.ticket++; this.animationSource = ''; this.pixels?.release(); this.pixels = undefined;
  }
  private owns(): boolean {
    return this.mounted && this.active && this.model.scope() === this.request.scope &&
      (this.model.profile.hubId || this.model.profile.id) === this.request.hub &&
      this.request.gallery.entries[this.index]?.target === this.model.target();
  }
  @Monitor('active','model.profile','model.selected') ownerChanged(): void {
    if (this.mounted && !this.owns()) this.onClose();
  }
  @Monitor('model.connected') connectionChanged(): void {
    if (this.owns() && this.model.connected && !this.full) void this.load();
  }
  private cached(entry: MessageImageGalleryEntry): ArrayBuffer | undefined {
    if (this.index === this.request.gallery.selectedIndex && this.request.thumbnail) return this.request.thumbnail;
    const host = this.getUIContext().getHostContext();
    if (!host) return undefined;
    try {
      return readThumbnail(thumbnailCachePath(host as common.UIAbilityContext, this.request.hub,
        JSON.stringify([this.request.scope, entry.target, entry.key, entry.itemId, entry.remotePath])));
    } catch (_) { return undefined; }
  }
  private async renderBytes(data: ArrayBuffer, maxEdge: number, valid: () => boolean, mime: string = ''): Promise<void> {
    const source = image.createImageSource(data);
    try {
      const info = await source.getImageInfo(), w = info.size.width, h = info.size.height;
      if (!valid()) return;
      if (!(w > 0 && h > 0) || w * h > 40000000) throw new Error('preview_unavailable');
      const factor = Math.min(1, maxEdge / Math.max(w,h));
      const pixels = await source.createPixelMap({desiredSize:{width:Math.max(1,Math.round(w*factor)),height:Math.max(1,Math.round(h*factor))}});
      if (!valid()) { await pixels.release(); return; }
      const encoded = mime === 'image/gif' ? await new util.Base64Helper().encodeToString(new Uint8Array(data)) : '';
      if (!valid()) { await pixels.release(); return; }
      this.animationSource = encoded ? 'data:image/gif;base64,' + encoded : '';
      const old = this.pixels; this.ratio = w/h; this.pixels = pixels; old?.release();
    } finally { await source.release(); }
  }
  private async load(): Promise<void> {
    if (!this.owns()) return;
    const ticket = ++this.ticket, entry = this.request.gallery.entries[this.index];
    const valid = (): boolean => ticket === this.ticket && this.owns();
    this.failed = false; this.loading = true;
    try {
      const original = this.request.original;
      if (original && sameMessageImageSource(original.source, entry) && original.scope === this.request.scope &&
        original.hub === this.request.hub && original.generation === this.model.connectionGeneration()) {
        await this.renderBytes(original.data, 3072, valid, original.mime);
      } else {
        const saved = this.cached(entry);
        if (saved) { try { await this.renderBytes(saved, 480, valid); } catch (_) {} }
        if (!valid()) return;
        if (!this.model.connected) { this.failed = !this.pixels; return; }
        const data = await loadMessageImage(this.model, entry.itemId, entry.remotePath);
        if (!valid()) return;
        await this.renderBytes(data.data, 3072, valid, data.mime);
      }
      if (valid()) this.full = true;
    } catch (_) { if (valid()) this.failed = true; }
    finally { if (valid()) this.loading = false; }
  }
  private resetZoom(): void { this.zoom = 1; this.shiftX = 0; this.shiftY = 0; }
  private select(index: number): void {
    if (index < 0 || index >= this.request.gallery.entries.length || index === this.index) return;
    this.ticket++; this.animationSource = ''; this.index = index; this.resetZoom(); this.pixels?.release(); this.pixels = undefined; this.full = false;
    void this.load();
  }
  @Computed get fitWidth(): number { return Math.max(1, Math.min(this.viewportWidth, this.viewportHeight * this.ratio)); }
  private constrain(): void {
    const x = Math.max(0,(this.fitWidth * this.zoom - this.viewportWidth)/2);
    const y = Math.max(0,(this.fitWidth / this.ratio * this.zoom - this.viewportHeight)/2);
    this.shiftX = Math.min(x,Math.max(-x,this.shiftX)); this.shiftY = Math.min(y,Math.max(-y,this.shiftY));
  }
  @Builder stage() {
    Stack() {
      if (this.pixels) {
        Image(this.animationSource || this.pixels).width(this.fitWidth).height(this.fitWidth / this.ratio).objectFit(ImageFit.Contain)
          .scale({x:this.zoom,y:this.zoom}).translate({x:this.shiftX,y:this.shiftY})
          .onError(() => { if (this.animationSource) this.animationSource = ''; })
          .accessibilityText(tr('image')).id(this.animationSource ? 'chat.photo.animation' : this.full ? 'chat.photo.original' : 'chat.photo.preview')
      } else if (this.loading) { LoadingProgress().width(26).height(26).color(Color.White) }
    }.width('100%').layoutWeight(1).clip(true).id('chat.photo.stage')
      .onSizeChange((_old: SizeOptions, size: SizeOptions) => { this.viewportWidth = Number(size.width); this.viewportHeight = Number(size.height); this.constrain(); })
      .gesture(GestureGroup(GestureMode.Parallel,
        PinchGesture({fingers:2}).onActionStart(() => { this.startZoom = this.zoom; })
          .onActionUpdate((e: GestureEvent) => { this.zoom = Math.min(5,Math.max(1,this.startZoom * e.scale)); this.constrain(); }),
        PanGesture({fingers:1}).onActionStart(() => { this.startX = this.shiftX; this.startY = this.shiftY; })
          .onActionUpdate((e: GestureEvent) => {
            if (this.zoom > 1) { this.shiftX = this.startX + e.offsetX; this.shiftY = this.startY + e.offsetY; this.constrain(); }
          }).onActionEnd((e: GestureEvent) => {
            if (this.zoom === 1 && Math.abs(e.offsetX) > 48 && Math.abs(e.offsetX) > 1.4 * Math.abs(e.offsetY)) this.select(this.index + (e.offsetX < 0 ? 1 : -1));
          }),
        GestureGroup(GestureMode.Exclusive,
          TapGesture({count:2}).onAction(() => { if (this.zoom > 1) this.resetZoom(); else if (this.pixels) this.zoom = 2.5; }),
          TapGesture().onAction(() => this.onClose()))
      ))
  }
  @Builder icon(glyph: Resource, name: string, enabled: boolean, action: () => void) {
    Button({type:ButtonType.Circle}) { SymbolGlyph(glyph).fontSize(19).fontColor([Color.White]) }
      .width(44).height(44).padding(0).backgroundColor('#442A2D33').enabled(enabled).opacity(enabled ? 1 : 0.36)
      .accessibilityText(tr(name)).onClick(action).id('chat.photo.' + name)
  }
  build() {
    Column() {
      Row() {
        Blank()
        this.icon($r('sys.symbol.xmark'), 'close', true, () => this.onClose())
      }.width('100%').padding({left:Tokens.inset,right:Tokens.inset,top:Tokens.gap})
      this.stage()
      if (this.failed || (!this.model.connected && !this.full)) {
        if (this.model.connected) Button(tr('retry'), {buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Color.White).onClick(() => { void this.load(); })
        else Text(tr('chat_photo_offline')).fontSize(Tokens.caption).fontColor(Color.White)
      }
      if (this.request.gallery.entries.length > 1) {
        Row({space:Tokens.page}) {
          this.icon($r('sys.symbol.chevron_left'), 'chat_photo_previous', this.index > 0, () => this.select(this.index - 1))
          Text(String(this.index + 1) + ' / ' + this.request.gallery.entries.length).fontSize(Tokens.meta).fontColor(Color.White)
          this.icon($r('sys.symbol.chevron_right'), 'chat_photo_next', this.index + 1 < this.request.gallery.entries.length, () => this.select(this.index + 1))
        }.justifyContent(FlexAlign.Center).width('100%')
      }
    }.width('100%').height('100%').padding({bottom:Tokens.inset}).backgroundColor('#EC111317').id('chat.photo.viewer')
  }
}
