// @vitest-environment jsdom
// The Agents title row's fold-all button (#6447): every click changes what is on screen. It folds and opens the groups;
// in the chat-list view, whose only groups are its end sections, it folds the whole list under the title and opens it
// again; with the list folded under the title it opens the list and every group; where nothing can fold it is not there.
// A hint names the action and what it changes. The groups here stand in for the sidebar's: headings driven by useFolded.
import { act, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { t } from "../i18n";
import { AGENTS_FOLD } from "../lib/sidebar-api";
import { FoldScope, useFolded } from "../lib/use-folded";
import { GroupFoldButton } from "./SidebarParts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function Group({ k, initial }: { k: string; initial: boolean }) {
  const [folded, toggle] = useFolded(k, initial);
  return <div data-sidebar-group-heading><button type="button" aria-expanded={!folded} onClick={toggle}>{k}</button></div>;
}

/** The Agents block in small: its controls, then its groups, inert while the block is folded (as Sidebar.tsx's). */
function Block({ groups, foldable = true, flat = false, blockFolded = false }: { groups: { k: string; folded?: boolean }[]; foldable?: boolean; flat?: boolean; blockFolded?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [folded, setFolded] = useState(blockFolded);
  return (
    <div ref={ref}>
      <div data-sidebar-agents-controls>
        <span data-block={folded ? "folded" : "open"} />
        <GroupFoldButton groups={ref} onAction={() => {}} block={foldable ? { folded, toggle: () => setFolded((f) => !f) } : undefined} flat={flat} />
      </div>
      <div inert={folded || undefined}>
        <FoldScope.Provider value={AGENTS_FOLD}>{groups.map((g) => <Group key={g.k} k={g.k} initial={g.folded ?? false} />)}</FoldScope.Provider>
      </div>
    </div>
  );
}

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};
const render = async (el: React.ReactElement) => {
  await act(async () => root.render(el));
  await flush();
};
const button = () => host.querySelector<HTMLButtonElement>("[data-testid=agents-fold-all]");
const hint = () => document.getElementById(button()!.getAttribute("aria-describedby")!)?.textContent;
const block = () => host.querySelector("[data-block]")!.getAttribute("data-block");
const opened = () => [...host.querySelectorAll("[data-sidebar-group-heading] button")].map((b) => b.getAttribute("aria-expanded"));
const press = async () => {
  await act(async () => button()!.click());
  await flush();
};

beforeEach(() => {
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("the Agents fold-all button", () => {
  it("folds every group while any is open, and opens them all once none is", async () => {
    await render(<Block groups={[{ k: "machine:a" }, { k: "machine:b", folded: true }]} />);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.fold_all"));
    expect(hint()).toBe(t("sidebar.fold_all_hint"));
    await press();
    expect(opened()).toEqual(["false", "false"]);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.open_all"));
    expect(hint()).toBe(t("sidebar.open_all_hint"));
    await press();
    expect(opened()).toEqual(["true", "true"]);
    expect(block()).toBe("open");
  });

  it("folds the whole chat list under its title and opens it again, leaving its Recent section alone", async () => {
    // before #6447 this opened Recent, out of sight at the list's end, and the list itself did not move
    await render(<Block flat groups={[{ k: "recent", folded: true }]} />);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.fold_all"));
    expect(hint()).toBe(t("sidebar.fold_list_hint"));
    await press();
    expect(block()).toBe("folded");
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.open_all"));
    expect(hint()).toBe(t("sidebar.open_list_hint"));
    await press();
    expect(block()).toBe("open");
  });

  it("folds a list with no groups under its title, where a click once changed only its own label", async () => {
    await render(<Block groups={[]} />);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.fold_all"));
    await press();
    expect(block()).toBe("folded");
  });

  it("opens the list folded under its title together with every group in it", async () => {
    // before #6447 the click opened the groups inside the hidden list, and the list stayed folded
    await render(<Block blockFolded groups={[{ k: "machine:a", folded: true }, { k: "machine:b", folded: true }]} />);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.open_all"));
    expect(hint()).toBe(t("sidebar.open_list_hint"));
    await press();
    expect(block()).toBe("open");
    expect(opened()).toEqual(["true", "true"]);
    expect(button()?.getAttribute("aria-label")).toBe(t("sidebar.fold_all"));
  });

  it("is not there when nothing on the list can fold (the phone's Agents page in the chat-list view)", async () => {
    await render(<Block foldable={false} flat groups={[{ k: "recent", folded: true }]} />);
    expect(button()).toBeNull();
    await render(<Block foldable={false} groups={[]} />);
    expect(button()).toBeNull();
  });

  it("shows its hint in a tooltip right after it, named by the button", async () => {
    await render(<Block groups={[{ k: "machine:a" }]} />);
    const tip = host.querySelector<HTMLElement>("[data-testid=agents-fold-all-hint]")!;
    expect(tip.getAttribute("role")).toBe("tooltip");
    expect(button()!.nextElementSibling).toBe(tip);
    expect(tip.textContent).toBe(t("sidebar.fold_all") + t("sidebar.fold_all_hint"));
    expect(button()!.hasAttribute("title")).toBe(false);
  });
});
