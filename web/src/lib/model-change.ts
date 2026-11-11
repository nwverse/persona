// Where the chat marks that the replies' model changed (#5034, cindy's model-only switch divider): between two visible
// replies whose models are both known and read differently. Only what the transcript says wrote a reply counts
// (Item.model), never the model picker; a reply whose model is unknown is skipped, and nothing goes inside an exchange.

import { modelShort } from "./context-usage";
import { modelName } from "./launcher-models";
import type { ChatLine, Segment } from "./turns";

/** The model the replies were written by before an exchange (`from`) and in it (`to`), as the transcript names them. */
export interface ModelChange {
  from: string;
  to: string;
}

/** A model id as the chat names it: Claude's "claude-sonnet-4-5-20250929" → "Sonnet 4.5" (modelShort), a raw GPT id
 *  made readable (modelName), any other id as it is. */
export function replyModelName(id: string): string {
  const short = modelShort(id);
  if (short && short !== id) return short[0].toUpperCase() + short.slice(1);
  return modelName({ id });
}

/** The model that wrote an exchange's reply: its answer's, else that of its latest step that names one. */
export function replyModel(seg: Segment): string | undefined {
  if (seg.answer?.model) return seg.answer.model;
  for (let i = seg.steps.length - 1; i >= 0; i--) {
    const st = seg.steps[i];
    const it = st.kind === "tool" ? st.use : st.item;
    if (it.role === "assistant" && it.model) return it.model;
  }
  return undefined;
}

/** `lines` with each visible exchange whose reply's model reads differently from the previous visible reply's marked
 *  (Segment.modelChange). Compared by name, so a dated id and its alias are one model. A folded round (supervision,
 *  background) is no visible reply; an exchange whose model is unknown neither gets a mark nor ends the run. */
export function markModelChanges(lines: ChatLine[]): ChatLine[] {
  let previous: { id: string; name: string } | undefined;
  return lines.map((line) => {
    if (line.kind !== "segment") return line;
    const model = replyModel(line.seg);
    if (!model) return line;
    const from = previous;
    previous = { id: model, name: replyModelName(model) };
    if (!from || from.name === previous.name) return line;
    return { ...line, seg: { ...line.seg, modelChange: { from: from.id, to: model } } };
  });
}
