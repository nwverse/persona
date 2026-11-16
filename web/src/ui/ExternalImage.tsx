// Markdown images on a third-party host stay unloaded until the reader taps them (#8162): opening a conversation must
// not send the reader's address to a host the message names. The hub's own images load inline as before. Only the
// consent gate for an off-hub image is lazy: same-origin and relative attachments draw immediately.
// With the hub's remote image route (`remote_images`, #9475) the host sees the hub, never the reader: a web picture
// inside a line of text the user's own Agents or assistant wrote loads through it at once, as tall as the line (block
// pictures are runs, ReplyImages.tsx); in what other people wrote (`askImages`) the gate stays, and loads through it.
import { lazy, Suspense, useContext, type ComponentProps, type CSSProperties } from "react";
import { getToken } from "../lib/api";
import { isOwnImageURL } from "../lib/links";
import { INLINE_SIZE, isWebImage, remoteImageURL, REMOTE_IMAGES_FEATURE, VIEWER_SIZE } from "../lib/reply-images";
import { hubHas, useStore } from "../lib/store";
import { ReplyPicturesContext } from "./reply-pictures-context";

const ExternalImageGate = lazy(() => import("./ExternalImageGate").then((m) => ({ default: m.ExternalImageGate })));

/** A picture inside a line of text: the line's height, its own width. */
const INLINE: CSSProperties = { display: "inline", height: "1.2em", width: "auto", maxWidth: "none", verticalAlign: "-0.25em", borderRadius: 3 };

export function ExternalImage({ node: _node, src = "", alt = "", ...rest }: ComponentProps<"img"> & { node?: unknown }) {
  const through = useStore((s) => hubHas(s.features, REMOTE_IMAGES_FEATURE)) && isWebImage(src);
  const { ask } = useContext(ReplyPicturesContext);
  if (isOwnImageURL(src)) return <img src={src} alt={alt} {...rest} />;
  if (through && !ask) return <img src={remoteImageURL(src, INLINE_SIZE, getToken())} alt={alt} decoding="async" loading="lazy" style={INLINE} />;
  return (
    <Suspense fallback={null}>
      <ExternalImageGate src={src} alt={alt} load={through ? remoteImageURL(src, VIEWER_SIZE, getToken()) : undefined} {...rest} />
    </Suspense>
  );
}
