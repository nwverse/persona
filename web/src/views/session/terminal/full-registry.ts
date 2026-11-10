// One full herdr feed per machine (docs/integrations/terminal.md "Focus synchronization"). herdr's focus is a single state shared by all its clients,
// so one client per machine can show any pane of it: going from one session (or the "Terminal" page) of a machine to another
// moves herdr's focus instead of closing a client and opening the next. The surfaces that come and go with the pages
// (FullTerminal, one per session page or kept terminal) take the machine's feed in turn: the one in front claims it
// (xterm moves into its box), the others only hold a place. A feed with no surface left lingers for a moment before it is
// let go, since a route change unmounts the old page and mounts the new one in the same breath.
import { FullFeed } from "./full";

/** How long a feed with no surface is kept (ms): the page that follows it claims it within this. */
export const LINGER_MS = 1500;

interface Entry {
  feed: FullFeed;
  users: number;
  timer: number;
}

const feeds = new Map<string, Entry>();

/** The machine's feed for a surface that now exists; `fresh` when it was just made (the surface mounts xterm into it). `sid` is the address it opens on. */
export function acquireFeed(mid: string, sid: string): { feed: FullFeed; fresh: boolean } {
  let e = feeds.get(mid);
  if (e) {
    clearTimeout(e.timer);
    e.users++;
    return { feed: e.feed, fresh: false };
  }
  e = { feed: new FullFeed(mid, sid), users: 1, timer: 0 };
  feeds.set(mid, e);
  return { feed: e.feed, fresh: true };
}

/** A surface of the machine is gone; the feed goes with its last one, after a moment. */
export function releaseFeed(mid: string, feed: FullFeed) {
  const e = feeds.get(mid);
  if (!e || e.feed !== feed) return;
  e.users = Math.max(0, e.users - 1);
  if (e.users > 0) return;
  clearTimeout(e.timer);
  e.timer = window.setTimeout(() => {
    if (feeds.get(mid) === e && e.users === 0) {
      feeds.delete(mid);
      e.feed.dispose();
    }
  }, LINGER_MS);
}

/** The feeds there are now (tests, and the page's own checks). */
export function liveFeeds(): FullFeed[] {
  return [...feeds.values()].map((e) => e.feed);
}
