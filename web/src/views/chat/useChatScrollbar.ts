// Native scrollbar presentation only: never cancel scrolling or replace the browser's thumb/keyboard behavior.
import { useEffect } from "react";

export const CHAT_SCROLLBAR = { idleMs: 1000, edgeWidth: 12 } as const;

export function useChatScrollbar(scroller: HTMLDivElement | null) {
  useEffect(() => {
    if (!scroller || !globalThis.CSS?.supports?.("scrollbar-color", "transparent transparent")) return;
    let timer = 0;
    let hovered = false;
    let dragging = false;
    const show = () => {
      clearTimeout(timer);
      if (scroller.dataset.scrollbarVisible !== "true") scroller.dataset.scrollbarVisible = "true";
    };
    const fade = () => {
      clearTimeout(timer);
      timer = window.setTimeout(() => {
        if (!hovered && !dragging) delete scroller.dataset.scrollbarVisible;
      }, CHAT_SCROLLBAR.idleMs);
    };
    const scroll = () => { show(); fade(); };
    const move = (event: MouseEvent) => {
      const right = scroller.getBoundingClientRect().right;
      const edge = Math.max(CHAT_SCROLLBAR.edgeWidth, scroller.offsetWidth - scroller.clientWidth);
      const next = event.clientX >= right - edge && event.clientX <= right;
      if (next === hovered) return;
      hovered = next;
      if (hovered) show();
      else fade();
    };
    const leave = () => { hovered = false; fade(); };
    const down = (event: MouseEvent) => {
      move(event);
      if (event.button === 0 && hovered) { dragging = true; show(); }
    };
    const up = () => { dragging = false; fade(); };
    const blur = () => { dragging = false; leave(); };
    scroller.dataset.chatScrollbar = "native";
    scroller.addEventListener("scroll", scroll, { passive: true });
    scroller.addEventListener("mousemove", move, { passive: true });
    scroller.addEventListener("mouseleave", leave, { passive: true });
    scroller.addEventListener("mousedown", down, { passive: true });
    window.addEventListener("mouseup", up, { passive: true });
    window.addEventListener("blur", blur);
    return () => {
      clearTimeout(timer);
      scroller.removeEventListener("scroll", scroll);
      scroller.removeEventListener("mousemove", move);
      scroller.removeEventListener("mouseleave", leave);
      scroller.removeEventListener("mousedown", down);
      window.removeEventListener("mouseup", up);
      window.removeEventListener("blur", blur);
      delete scroller.dataset.chatScrollbar;
      delete scroller.dataset.scrollbarVisible;
    };
  }, [scroller]);
}
