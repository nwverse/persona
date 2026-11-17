// What a Markdown block tells its runs of pictures (ReplyImages.tsx): its text (whose pictures the viewer pages through)
// and whether they wait for a tap (`ask`: what other people wrote, where a picture may be a tracking pixel).
// First-screen code: only a context, the runs themselves load with a reply's first picture.
import { createContext } from "react";

export interface ReplyPictures {
  text: string;
  ask: boolean;
}

export const ReplyPicturesContext = createContext<ReplyPictures>({ text: "", ask: false });

/** Set by Export image (#9469, views/chat/ExportImage.tsx): a run draws all its pictures, through the hub only, for a
 *  picture that cannot be tapped (no "+N", no viewer, no waiting for a tap). */
export const ReplyPicturesExport = createContext(false);
