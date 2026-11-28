import { Colors } from '../design/Colors';
import { common } from '@kit.AbilityKit';
import { image } from '@kit.ImageKit';
import type { ConversationPhotoRequest, LoadedMessageImage, MessageImageGalleryEntry, MessageImageGalleryWindow } from '../core/MessageImageGallery';
import { fittedThumbnail, messageImageGalleryWindow, sameMessageImageSource } from '../core/MessageImageGallery';
import { loadMessageImage } from '../models/MessageImage';
import { readDisplayThumbnail, readThumbnail, thumbnailByteLimit, thumbnailCachePath, writeThumbnail } from '../platform/MessageThumbnailCache';
import { ToolGlyph } from '../design/ToolGlyph';
import type { ClientModel } from '../models/ClientModel';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct TranscriptImage {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Param itemId: string = '';
  @Param remotePath: string = '';
  @Param sourceKey: string = '';
  @Param galleryEntries: MessageImageGalleryEntry[] = [];
  @Param galleryIndex: number = -1;
  @Param label: string = '';
  @Param thumbnailEdge: number = 384;
  @Param maximumThumbnailWidth: number = 0;
  @Param maximumThumbnailHeight: number = 0;
  private thumbnailVisible: boolean = false;
  @Event onPreview: (request: ConversationPhotoRequest) => void = () => {};
  @Event onEdit: (image: LoadedMessageImage) => void = (_image: LoadedMessageImage): void => {};
  private loaded: LoadedMessageImage | undefined;
  @Local pixels: image.PixelMap | undefined;
  @Local busy: boolean = false;
  @Local failed: boolean = false;
  @Local ratio: number = 1;
  private mounted: boolean = false;
  private revision: number = 0;
  private activityVisit: number = 0;
  private sourceVisit: number = 0;
  private galleryVisit: number = 0;
  private observedGallery: string = '';

  aboutToAppear(): void {
    this.mounted = true;
    this.activityVisit++;
    this.sourceVisit++;
    this.observedGallery = this.gallerySignature();
    this.load();
  }

  @Monitor('active') activityChanged(): void {
    this.activityVisit++;
    this.busy = false;
    if (this.loaded && !this.loadedOwnerCurrent()) this.resetThumbnail();
    if (this.active && this.mounted) this.load();
  }

  @Monitor('model.profile', 'model.connected') ownerChanged(): void {
    this.activityVisit++;
    this.sourceVisit++;
    this.resetThumbnail();
    if (this.model.connected && this.active && this.mounted) this.load();
  }

  @Monitor('itemId', 'remotePath', 'sourceKey') imageChanged(): void {
    this.revision++;
    this.activityVisit++;
    this.sourceVisit++;
    this.resetThumbnail();
    this.load();
  }

  @Monitor('galleryEntries', 'galleryIndex') galleryChanged(): void {
    const signature = this.gallerySignature();
    if (signature === this.observedGallery) return;
    this.observedGallery = signature;
    this.galleryVisit++;
    this.activityVisit++;
    this.sourceVisit++;
    this.busy = false;
    if (this.active && this.mounted) this.load();
  }

  private loadedOwnerCurrent(): boolean {
    const loaded = this.loaded;
    return !!loaded && loaded.target === this.model.target() && loaded.scope === this.model.scope() &&
      loaded.generation === this.model.connectionGeneration() && loaded.hub === (this.model.profile.hubId || this.model.profile.id) &&
      this.model.connected && this.model.owns(loaded.generation, loaded.scope);
  }

  private resetThumbnail(): void {
    this.pixels?.release(); this.pixels = undefined; this.loaded = undefined;
    this.busy = false; this.failed = false;
  }

  private sourceEntry(): MessageImageGalleryEntry {
    const key = this.sourceKey || JSON.stringify([this.itemId, this.remotePath]);
    return { key: key, itemId: this.itemId, remotePath: this.remotePath, target: this.model.target() };
  }

  private galleryWindow(): MessageImageGalleryWindow | undefined {
    if (this.galleryEntries.length === 0) return messageImageGalleryWindow([this.sourceEntry()], 0);
    return messageImageGalleryWindow(this.galleryEntries, this.galleryIndex);
  }

  private gallerySignature(): string { return this.galleryWindow()?.signature || ''; }

  private current(model: ClientModel, revision: number, visit: number, generation: number,
    scope: string, target: string, hub: string, galleryVisit: number, gallerySignature: string): boolean {
    return this.active && this.activityVisit === visit &&
      this.currentOwner(model, revision, this.sourceVisit, generation, scope, target, hub, galleryVisit, gallerySignature);
  }

  private currentOwner(model: ClientModel, revision: number, sourceVisit: number, generation: number,
    scope: string, target: string, hub: string, galleryVisit: number, gallerySignature: string): boolean {
    return this.mounted && this.model === model && this.revision === revision && this.sourceVisit === sourceVisit &&
      this.galleryVisit === galleryVisit && model.connected && model.connectionGeneration() === generation &&
      model.scope() === scope && model.target() === target && model.owns(generation, scope) &&
      (model.profile.hubId || model.profile.id) === hub && (!gallerySignature || this.gallerySignature() === gallerySignature);
  }

  private cachePath(): string {
    if (this.maximumThumbnailWidth <= 0) return '';
    try {
      const host = this.getUIContext().getHostContext();
      const entry = this.sourceEntry();
      return host ? thumbnailCachePath(host as common.UIAbilityContext, this.model.profile.hubId || this.model.profile.id,
        JSON.stringify([this.model.scope(), entry.target, entry.key, entry.itemId, entry.remotePath])) : '';
    } catch (_) { return ''; }
  }

  private async drawThumbnail(data: ArrayBuffer, valid: () => boolean): Promise<boolean> {
    const source = image.createImageSource(data);
    try {
      const info = await source.getImageInfo();
      if (!valid()) return false;
      if (info.size.width <= 0 || info.size.height <= 0 || info.size.width * info.size.height > 40000000) throw new Error('response_too_large');
      const edge = this.maximumThumbnailWidth > 0 ? this.getUIContext().vp2px(Math.max(this.maximumThumbnailWidth, this.maximumThumbnailHeight)) : 1600;
      const scale = Math.min(1, edge / Math.max(info.size.width, info.size.height));
      const pixels = await source.createPixelMap({ desiredSize:{ width:Math.max(1, Math.round(info.size.width * scale)), height:Math.max(1, Math.round(info.size.height * scale)) } });
      if (!valid()) { await pixels.release(); return false; }
      const previous = this.pixels;
      this.pixels = pixels; this.ratio = info.size.width / info.size.height;
      previous?.release();
      return true;
    } finally { await source.release(); }
  }

  private async load(requested: boolean = false): Promise<void> {
    if (this.maximumThumbnailWidth > 0 && !requested && !this.thumbnailVisible) return;
    if (this.pixels || this.busy || !this.active || !this.mounted || !this.model.isChat()) return;
    const model = this.model;
    const revision = this.revision;
    const visit = this.activityVisit;
    const target = model.target();
    const generation = model.connectionGeneration();
    const scope = model.scope();
    const hub = model.profile.hubId || model.profile.id;
    const galleryVisit = this.galleryVisit;
    const valid = (): boolean => this.current(model, revision, visit, generation, scope, target, hub, galleryVisit, '');
    const cache = this.cachePath();
    const local = (): boolean => this.active && this.mounted && this.activityVisit === visit && this.revision === revision &&
      this.model === model && model.owns(generation, scope) && model.target() === target && (model.profile.hubId || model.profile.id) === hub;
    this.busy = true;
    this.failed = false;
    try {
      const saved = cache ? await readDisplayThumbnail(cache) : undefined;
      if (saved && local()) {
        try {
          const drawn = await this.drawThumbnail(saved, local);
          // A recorded image item is immutable; a file at a workspace path can change.
          if (drawn && this.itemId && !this.remotePath) return;
        } catch (_) { /* Refresh a stale cache file when online. */ }
      }
      if (!valid()) return;
      const response = await loadMessageImage(model, this.itemId, this.remotePath, this.maximumThumbnailWidth > 0, this.thumbnailEdge);
      if (!valid() || !await this.drawThumbnail(response.data, valid) || !valid()) return;
      if (response.original !== false) this.loaded = { source:this.sourceEntry(), target, scope, generation, hub, data:response.data, mime:response.mime };
      if (cache) {
        if (response.data.byteLength <= thumbnailByteLimit) await writeThumbnail(cache, response.data, valid);
        else if (this.pixels) {
          const packer = image.createImagePacker();
          try {
            const small = await packer.packToData(this.pixels, {format:'image/jpeg',quality:76});
            if (valid()) await writeThumbnail(cache, small, valid);
          } finally { await packer.release(); }
        }
      }
    } catch (_) {
      if (valid()) this.failed = true;
    } finally {
      if (revision === this.revision && visit === this.activityVisit) this.busy = false;
    }
  }

  aboutToDisappear(): void {
    this.mounted = false;
    this.thumbnailVisible = false;
    this.activityVisit++;
    this.sourceVisit++;
    this.revision++;
    this.busy = false;
    this.pixels?.release();
    this.pixels = undefined;
    this.loaded = undefined;
  }

  private preview(): void {
    const gallery = this.galleryWindow();
    if (!this.active || !this.mounted || !this.pixels || !gallery ||
      !sameMessageImageSource(gallery.entries[gallery.selectedIndex], this.sourceEntry())) return;
    const path = this.cachePath();
    this.onPreview({ gallery, scope:this.model.scope(), hub:this.model.profile.hubId || this.model.profile.id,
      thumbnail:path ? readThumbnail(path) : undefined, original:this.loadedOwnerCurrent() ? this.loaded : undefined });
  }

  private async editLoaded(): Promise<void> {
    const model = this.model, generation = model.connectionGeneration(), scope = model.scope(), target = model.target();
    const revision = this.revision, visit = this.activityVisit, gallery = this.galleryVisit, hub = model.profile.hubId || model.profile.id;
    let loaded = this.loaded;
    if (!loaded && model.connected && this.pixels) {
      try {
        const data = await loadMessageImage(model, this.itemId, this.remotePath);
        if (!this.current(model, revision, visit, generation, scope, target, hub, gallery, '')) return;
        if (!['image/jpeg','image/png'].includes(data.mime)) { this.getUIContext().getPromptAction().showToast({message:tr('preview_unavailable')}); return; }
        loaded = { source:this.sourceEntry(), target, scope, generation, hub, data:data.data, mime:data.mime };
        this.loaded = loaded;
      } catch (_) { return; }
    }
    const window = this.galleryWindow();
    if (!this.active || !this.mounted || !loaded || !window || !sameMessageImageSource(window.entries[window.selectedIndex], loaded.source) ||
      loaded.target !== this.model.target() || loaded.scope !== this.model.scope() ||
      loaded.generation !== this.model.connectionGeneration() || loaded.hub !== (this.model.profile.hubId || this.model.profile.id) ||
      !this.model.owns(loaded.generation, loaded.scope)) return;
    this.onEdit(loaded);
  }

  @Builder imageMenu() {
    Menu() {
      MenuItem({ content: tr('edit') }).enabled(!!this.pixels && this.model.connected && !this.busy &&
        (!this.loaded || ['image/jpeg', 'image/png'].includes(this.loaded.mime))).onClick(() => { void this.editLoaded(); })
    }
  }

  @Computed get thumbnailSize(): number[] {
    return fittedThumbnail(this.pixels ? this.ratio : 1.25, this.maximumThumbnailWidth, this.maximumThumbnailHeight);
  }
  build() {
    Column() {
      if (this.pixels) {
        Stack({ alignContent: Alignment.BottomEnd }) {
          Image(this.pixels).width(this.maximumThumbnailWidth > 0 ? this.thumbnailSize[0] : '100%')
            .aspectRatio(this.ratio).objectFit(ImageFit.Contain).borderRadius(this.maximumThumbnailWidth > 0 ? Tokens.gap : 0)
          SymbolGlyph($r('sys.symbol.picture')).fontSize(13).fontColor([Color.White])
            .padding(4).backgroundColor('#99000000').borderRadius(5).margin(5)
            .hitTestBehavior(HitTestMode.None).accessibilityLevel('no')
        }.width('100%').height(this.maximumThumbnailWidth > 0 ? '100%' : undefined)
          .onClick(() => this.preview()).bindContextMenu(this.imageMenu, ResponseType.LongPress)
          .accessibilityText(this.label || tr('image')).id('chat.image.thumbnail')
      } else if (this.busy) {
        LoadingProgress().width(this.maximumThumbnailWidth > 0 ? Tokens.symbol : Tokens.touch)
          .height(this.maximumThumbnailWidth > 0 ? Tokens.symbol : Tokens.touch)
      } else {
        Button({ type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL }) {
          Column({space:Tokens.fine}) {
            ToolGlyph({name:'image', ink:Colors.activity})
            if (this.label) Text(this.label).fontSize(Tokens.toolMeta).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
            if (!this.model.connected || this.failed) Text(tr(this.model.connected ? 'retry' : 'chat_photo_offline'))
              .fontSize(Tokens.toolMeta).fontColor(Colors.secondary).maxLines(2).textAlign(TextAlign.Center)
          }
        }.padding(Tokens.fine).backgroundColor(Color.Transparent)
          .enabled(this.model.connected).accessibilityText(tr(!this.model.connected ? 'chat_photo_offline' : this.failed ? 'retry' : 'image'))
          .onClick(() => { void this.load(true); }).id('chat.image.placeholder')
      }
    }.width(this.maximumThumbnailWidth > 0 ? Math.max(Math.min(Tokens.touch, this.maximumThumbnailWidth), this.thumbnailSize[0]) : '100%')
      .height(this.maximumThumbnailWidth > 0 ? Math.max(Math.min(Tokens.touch, this.maximumThumbnailHeight), this.thumbnailSize[1]) : undefined)
      .justifyContent(FlexAlign.Center).alignItems(HorizontalAlign.Center)
      .backgroundColor(this.maximumThumbnailWidth > 0 && !this.pixels ? Colors.grouped : Color.Transparent)
      .borderRadius(this.maximumThumbnailWidth > 0 ? Tokens.gap : 0)
      .border({ width:this.maximumThumbnailWidth > 0 ? Tokens.hairline : 0, color:Colors.divider, radius:Tokens.gap })
      .clip(this.maximumThumbnailWidth > 0)
      .onVisibleAreaChange([0, 0.01, 1], (_increasing: boolean, ratio: number) => {
        this.thumbnailVisible = ratio > 0;
        if (this.thumbnailVisible && !this.failed) this.load();
      })
  }
}
