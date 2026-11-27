import type { ConversationPhotoRequest, LoadedMessageImage } from '../core/MessageImageGallery';
import { ClientModel } from '../models/ClientModel';
import { AssistantFeedModel } from '../models/AssistantFeed';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { parseAssistantViewBlocks } from '../models/AssistantView';
import { assistantCardResource } from '../models/AssistantCardLinks';
import { ChatMessage, ResourceRow, Wire } from '../models/Wire';
import { AssistantMessagePart, assistantMessageParts } from '../core/AssistantCardText';
import { AssistantViewCard } from './AssistantViewCard';
import { ConversationMarkdownCache } from '../core/Markdown';
import { MarkdownText } from './MarkdownText';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct AssistantMessage {
  @Require @Param model: ClientModel;
  @Require @Param feed: AssistantFeedModel;
  @Require @Param message: ChatMessage;
  @Param active: boolean = false;
  @Param workspaceRoot: string = '';
  @Param mediaWidth: number = 0;
  @Param documents: ConversationMarkdownCache | undefined = undefined;
  @Event onEditImage: (image: LoadedMessageImage) => void = () => {};
  @Event onPreview: (request: ConversationPhotoRequest) => void = () => {};
  @Event onOpenFile: (path: string) => void = (_path: string) => {};
  @Event onLink: (address: string) => void = () => {};
  @Event onOpenResource: (row: ResourceRow) => void = () => {};
  @Local parts: BoundRow<AssistantMessagePart>[] = [];
  @Local item: Record<string, Object> = {};
  private partsOwner: ClientModel | undefined;
  private partsScope: string = '';
  private sourceText: string = '';

  aboutToAppear(): void { this.syncSource(); }
  @Monitor('message', 'model', 'model.profile', 'model.selected') sourceChanged(): void { this.syncSource(); }
  private syncSource(): void {
    const target = this.model.target(), id = this.message.itemId;
    const scope = JSON.stringify([target, id, this.message.ownerTarget]);
    if (this.partsOwner === this.model && this.partsScope === scope && this.sourceText === this.message.text) return;
    const trusted = this.model.selected.kind === 'conversation' && this.message.role === 'assistant' && this.message.ownerTarget === target && !!id;
    this.item = Wire.payload(['id', 'item_id', 'kind', 'role', 'text', 'conversation', 'manager'],
      [id, id, 'text', 'assistant', this.message.text, this.model.selected.id, this.model.profile.manager]);
    const parts = trusted ? assistantMessageParts(this.message.text, parseAssistantViewBlocks(id, this.message.text), tr('assistant_view_unavailable')) :
      [new AssistantMessagePart('text:0', this.message.text)];
    this.parts = reconcileBoundRows(this.parts, parts, (part: AssistantMessagePart) => part.id,
      this.partsOwner, this.model, this.partsScope, scope);
    this.partsOwner = this.model; this.partsScope = scope; this.sourceText = this.message.text;
  }
  private openResource(address: string): void {
    if (!this.active || this.message.ownerTarget !== this.model.target()) return;
    const row = assistantCardResource(address, this.model.profile.id, this.model.profile.manager, this.model.sessions.concat(this.model.conversations));
    if (!row) { this.model.error = 'assistant_view_invalid_target'; return; }
    if (!row.title) row.title = tr(row.kind === 'todo' ? 'todos' : row.kind === 'knowledge' ? 'knowledge' : row.kind === 'conversation' ? 'main_chat' : 'agents');
    this.onOpenResource(row);
  }
  build() {
    Column({ space: Tokens.gap }) {
      ForEach(this.parts, (part: BoundRow<AssistantMessagePart>) => {
        if (part.value.cardKey) AssistantViewCard({ model: this.model, feed: this.feed, item: this.item, cardKey: part.value.cardKey,
          active: this.active && this.model.connected, onLink: (address: string) => this.onLink(address), onOpenResource: (address: string) => this.openResource(address) })
        else if (part.value.text) MarkdownText({ transcript: true, chatReply: true, documents:this.documents, narration:this.message.narration, text: part.value.text, mediaModel:this.model, mediaActive:this.active, mediaWidth:this.mediaWidth,
          mediaTarget:this.message.ownerTarget, messageId:this.message.id, onPreview:this.onPreview, onEditImage:this.onEditImage, workspaceRoot:this.workspaceRoot, onOpenFile:(path: string) => this.onOpenFile(path), onLink: (address: string) => this.onLink(address) })
      }, (part: BoundRow<AssistantMessagePart>) => part.key)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
