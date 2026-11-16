// The step that lifts a reply's block pictures into runs (reply-images-plugin.ts), loaded the first time a reply names
// a web picture (lazy-plugin.ts), with the runs' own component (ReplyImages.tsx) in the same request, so the run is
// drawn as soon as the step has made it. Replies without pictures never carry either.
import type { Pluggable } from "unified";
import { lazyPlugin } from "./stream/lazy-plugin";

const images = lazyPlugin<Pluggable>(() =>
  Promise.all([import("./reply-images-plugin"), import("./ReplyImages")]).then(([m]) => m.rehypeReplyImages as Pluggable),
);

/** The step once loaded, when `wanted` (asking for it then). */
export const useReplyImages = images.use;
/** Loads it (once): tests, and anything that wants it before the page is idle. */
export const loadReplyImages = images.load;
