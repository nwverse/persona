// Hub hints acted on while the page is shown. A hidden tab keeps its socket and still gets every hint; the reads they
// wake are for a list nobody is looking at. Hints that come while it is hidden are held, and the page reads once when
// it is shown again, as useRuntimeSession and the sidebar's pending sessions already do.
import { onHubEvent } from "./store";
import { documentPage } from "./visible-poll";

type Page = { visible: () => boolean; subscribe: (fn: () => void) => () => void };

/** `fn` for each `event` while `page` is shown; for the ones that came while it was hidden, one call when it is shown
 * again. Returns the unsubscribe. */
export function onShownHint(event: string, fn: () => void, page: Page = documentPage): () => void {
  let missed = false;
  const off = onHubEvent(event, () => {
    if (page.visible()) fn();
    else missed = true;
  });
  const offPage = page.subscribe(() => {
    if (!missed || !page.visible()) return;
    missed = false;
    fn();
  });
  return () => { off(); offPage(); };
}
