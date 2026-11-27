// Enlarged image stage shown above the elevated single live Composer (#17095): FLIP-animates both the clicked
// thumbnail and the live composer container, dynamically tracks the growing composer's height, supports multi-image
// navigation and drag-and-drop, and launches ImageEditor while keeping the expanded view alive underneath.
import { Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, type HTMLAttributes, type RefObject } from "react";
import { t } from "../../i18n";
import { useCloseOnBack } from "../../lib/back-close";
import type { DraftImage } from "../../lib/drafts";
import type { EditedImage, EditState } from "../../lib/image-edit";
import { lazyPage } from "../../lib/lazy-page";
import { NARROW, useMedia } from "../../lib/viewport";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const ImageEditor = lazyPage(() => import("./ImageEditor").then((m) => m.ImageEditor));

export interface ComposerImageStageItem {
  id: string;
  name: string;
  url: string;
  edited?: EditedImage;
}

export interface ComposerImageStageProps {
  attachments: readonly ComposerImageStageItem[];
  activeId: string;
  closing: boolean;
  compact: boolean;
  composerRef: RefObject<HTMLDivElement | null>;
  slotRef: RefObject<HTMLDivElement | null>;
  innerOverlayOpen: boolean;
  dragging: boolean;
  drag: Pick<HTMLAttributes<HTMLDivElement>, "onDragOver" | "onDragLeave" | "onDrop">;
  onSelect: (id: string) => void;
  onUpdateAttachment: (id: string, img: DraftImage, state: EditState) => void;
  onClose: () => void;
  onCloseComplete: () => void;
}

const OPEN_MS = 280;
const CLOSE_MS = 210;
const SWITCH_MS = 200;
const STAGE_EASE = "cubic-bezier(0.22, 1, 0.36, 1)";
const CLOSE_EASE = "cubic-bezier(0.4, 0, 0.2, 1)";

function reducedMotion(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function canAnimate(el: Element | null): el is HTMLElement {
  return Boolean(el && typeof (el as HTMLElement).animate === "function" && !reducedMotion());
}

function clamp(v: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, v));
}

function hasExternalOverlay(): boolean {
  return Boolean(document.querySelector("[role=dialog],[role=menu]"));
}

function findTileRect(composer: HTMLElement | null, id: string): DOMRect | null {
  if (!composer) return null;
  for (const el of composer.querySelectorAll<HTMLElement>("[data-attachment-id]")) {
    if (el.dataset.attachmentId === id) {
      const rect = el.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0 ? rect : null;
    }
  }
  return null;
}

export function ComposerImageStage({
  attachments,
  activeId,
  closing,
  compact,
  composerRef,
  slotRef,
  innerOverlayOpen,
  dragging,
  drag,
  onSelect,
  onUpdateAttachment,
  onClose,
  onCloseComplete,
}: ComposerImageStageProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  const headerRef = useRef<HTMLDivElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const [editingId, setEditingId] = useState<string | null>(null);

  const narrow = useMedia(NARROW);
  const index = Math.max(0, attachments.findIndex((a) => a.id === activeId));
  const active = attachments[index] ?? null;
  const editing = editingId ? attachments.find((a) => a.id === editingId) ?? null : null;
  const modalOpen = innerOverlayOpen || Boolean(editing);
  const many = attachments.length > 1;

  useEffect(() => {
    void ImageEditor.preload().catch(() => {});
  }, []);

  // Latest callbacks/state for event listeners without re-binding on every render.
  const latest = useRef({ active, attachments, index, closing, modalOpen, onSelect, onClose, onCloseComplete });
  latest.current = { active, attachments, index, closing, modalOpen, onSelect, onClose, onCloseComplete };

  // Track running Web Animations and open origin thumbnail rect captured before elevating the composer.
  const openAnims = useRef<Animation[]>([]);
  const closeAnims = useRef<Animation[]>([]);
  const originThumbRef = useRef<DOMRect | null>(null);
  const playedOpenImg = useRef(false);
  const prevShown = useRef<{ id: string; url: string; index: number } | null>(null);
  const hadOverlayAtDown = useRef(false);
  const swipeStart = useRef<{ x: number; y: number } | null>(null);
  const didSwipe = useRef(false);

  const cancelAll = (list: RefObject<Animation[]>) => {
    for (const anim of list.current) {
      try {
        anim.cancel();
      } catch {
        // already finished or unsupported
      }
    }
    list.current = [];
  };

  // Back gesture on narrow screens collapses the stage instead of leaving the conversation.
  useCloseOnBack(!closing, onClose, narrow);

  // Sync the elevated composer's width when viewport crosses the compact breakpoint.
  useLayoutEffect(() => {
    const composer = composerRef.current;
    if (!composer || composer.style.position !== "fixed") return;
    composer.style.width = compact ? "calc(100vw - 20px)" : "min(920px, calc(100vw - 48px))";
  }, [compact, composerRef]);

  // Keep the stage's bottom clearance strictly above the live elevated composer as it grows or wraps.
  // Read untransformed offsetHeight so FLIP scale transforms never under-measure clearance.
  useLayoutEffect(() => {
    const stage = stageRef.current;
    const composer = composerRef.current;
    if (!stage || !composer) return;
    const sync = () => {
      const h = composer.offsetHeight || composer.getBoundingClientRect().height;
      if (h > 0) stage.style.setProperty("--composer-stage-h", `${Math.ceil(h)}px`);
    };
    sync();
    const ro = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(sync);
    ro?.observe(composer);
    window.addEventListener("resize", sync);
    window.visualViewport?.addEventListener("resize", sync);
    return () => {
      ro?.disconnect();
      window.removeEventListener("resize", sync);
      window.visualViewport?.removeEventListener("resize", sync);
    };
  }, [composerRef]);

  // Play the FLIP animation on the enlarged image once its rendered box is measurable.
  const playOpenImageFlip = useCallback(() => {
    if (playedOpenImg.current) return;
    const img = imgRef.current;
    if (!img || !canAnimate(img)) {
      playedOpenImg.current = true;
      return;
    }
    const to = img.getBoundingClientRect();
    if (to.width <= 0 || to.height <= 0) return;
    playedOpenImg.current = true;
    const from = originThumbRef.current;
    if (from && from.width > 0 && from.height > 0) {
      const dx = (from.left + from.width / 2) - (to.left + to.width / 2);
      const dy = (from.top + from.height / 2) - (to.top + to.height / 2);
      const scale = clamp(Math.min(from.width / to.width, from.height / to.height), 0.08, 1);
      openAnims.current.push(
        img.animate(
          [
            { transform: `translate3d(${dx}px, ${dy}px, 0) scale(${scale})`, opacity: 0.65 },
            { transform: "translate3d(0, 0, 0) scale(1)", opacity: 1 },
          ],
          { duration: OPEN_MS, easing: STAGE_EASE },
        ),
      );
    } else {
      openAnims.current.push(
        img.animate(
          [
            { transform: "translate3d(0, 10px, 0) scale(0.94)", opacity: 0 },
            { transform: "translate3d(0, 0, 0) scale(1)", opacity: 1 },
          ],
          { duration: OPEN_MS, easing: STAGE_EASE },
        ),
      );
    }
  }, []);

  // Open choreography: measure the docked live composer & clicked tile before elevating the same DOM node to its
  // fixed bottom dock, hold the normal-flow placeholder height so chat scroll never jumps, and run FLIP animations.
  useLayoutEffect(() => {
    cancelAll(openAnims);
    const composer = composerRef.current;
    const slot = slotRef.current;
    const fromBox = composer?.getBoundingClientRect() ?? null;
    originThumbRef.current = findTileRect(composer, activeId);
    if (slot && fromBox && fromBox.height > 0) {
      slot.style.height = `${fromBox.height}px`;
    }
    if (composer) {
      composer.style.position = "fixed";
      composer.style.left = "0";
      composer.style.right = "0";
      composer.style.marginInline = "auto";
      composer.style.zIndex = "36";
      composer.style.width = compact ? "calc(100vw - 20px)" : "min(920px, calc(100vw - 48px))";
      composer.style.bottom = "calc(max(0px, 100dvh - var(--kb-top) - var(--kb-h)) + max(12px, env(safe-area-inset-bottom)))";
      composer.style.transformOrigin = "center bottom";
      const h = composer.offsetHeight || composer.getBoundingClientRect().height;
      if (h > 0 && stageRef.current) stageRef.current.style.setProperty("--composer-stage-h", `${Math.ceil(h)}px`);
    }
    if (canAnimate(backdropRef.current)) {
      openAnims.current.push(
        backdropRef.current.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 240, easing: STAGE_EASE }),
      );
    }
    if (canAnimate(headerRef.current)) {
      openAnims.current.push(
        headerRef.current.animate(
          [
            { opacity: 0, transform: "translate3d(0, -8px, 0)" },
            { opacity: 1, transform: "translate3d(0, 0, 0)" },
          ],
          { duration: 240, easing: STAGE_EASE },
        ),
      );
    }
    if (fromBox && fromBox.width > 0 && canAnimate(composer)) {
      const to = composer.getBoundingClientRect();
      if (to.width > 0 && to.height > 0) {
        const dx = (fromBox.left + fromBox.width / 2) - (to.left + to.width / 2);
        const dy = fromBox.bottom - to.bottom;
        const scale = clamp(fromBox.width / to.width, 0.72, 1.08);
        openAnims.current.push(
          composer.animate(
            [
              { transform: `translate3d(${dx}px, ${dy}px, 0) scale(${scale})`, opacity: 0.94 },
              { transform: "translate3d(0, 0, 0) scale(1)", opacity: 1 },
            ],
            { duration: OPEN_MS, easing: STAGE_EASE },
          ),
        );
      }
    }
    playOpenImageFlip();
    return () => {
      cancelAll(openAnims);
      if (composer) {
        composer.style.position = "";
        composer.style.left = "";
        composer.style.right = "";
        composer.style.marginInline = "";
        composer.style.zIndex = "";
        composer.style.width = "";
        composer.style.bottom = "";
        composer.style.transformOrigin = "";
      }
      if (slot) slot.style.height = "";
    };
    // Run once on mount; captures the live docked geometry before elevating the composer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Multi-image switch / post-edit refresh animation.
  useLayoutEffect(() => {
    if (!active || closing) return;
    const prev = prevShown.current;
    prevShown.current = { id: active.id, url: active.url, index };
    if (!prev) return;
    const img = imgRef.current;
    if (!canAnimate(img)) return;
    if (prev.id !== active.id) {
      const dir = index > prev.index ? 1 : index < prev.index ? -1 : 0;
      img.animate(
        [
          { transform: `translate3d(${dir * 20}px, 0, 0) scale(0.985)`, opacity: 0.25 },
          { transform: "translate3d(0, 0, 0) scale(1)", opacity: 1 },
        ],
        { duration: SWITCH_MS, easing: STAGE_EASE },
      );
    } else if (prev.url !== active.url) {
      img.animate(
        [
          { transform: "scale(0.985)", opacity: 0.8 },
          { transform: "scale(1)", opacity: 1 },
        ],
        { duration: 180, easing: STAGE_EASE },
      );
    }
  }, [active, index, closing]);

  // Collapse choreography: smoothly animate the live composer back onto its docked slot and fold the image away.
  useLayoutEffect(() => {
    if (!closing) {
      cancelAll(closeAnims);
      return;
    }
    cancelAll(openAnims);
    cancelAll(closeAnims);

    const composer = composerRef.current;
    if (!canAnimate(composer)) {
      latest.current.onCloseComplete();
      return;
    }

    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      cancelAll(closeAnims);
      latest.current.onCloseComplete();
    };

    const fromBox = composer.getBoundingClientRect();
    const toSlot = slotRef.current?.getBoundingClientRect();
    let leadAnim: Animation | undefined;

    if (toSlot && toSlot.width > 0 && fromBox.width > 0) {
      const dx = (toSlot.left + toSlot.width / 2) - (fromBox.left + fromBox.width / 2);
      const dy = toSlot.bottom - fromBox.bottom;
      const scale = clamp(toSlot.width / fromBox.width, 0.72, 1.08);
      leadAnim = composer.animate(
        [
          { transform: "translate3d(0, 0, 0) scale(1)" },
          { transform: `translate3d(${dx}px, ${dy}px, 0) scale(${scale})` },
        ],
        { duration: CLOSE_MS, easing: CLOSE_EASE, fill: "forwards" },
      );
      closeAnims.current.push(leadAnim);
    }

    if (canAnimate(backdropRef.current)) {
      const anim = backdropRef.current.animate([{ opacity: 1 }, { opacity: 0 }], {
        duration: CLOSE_MS,
        easing: CLOSE_EASE,
        fill: "forwards",
      });
      closeAnims.current.push(anim);
      leadAnim ??= anim;
    }

    if (canAnimate(headerRef.current)) {
      closeAnims.current.push(
        headerRef.current.animate(
          [
            { opacity: 1, transform: "translate3d(0, 0, 0)" },
            { opacity: 0, transform: "translate3d(0, -6px, 0)" },
          ],
          { duration: 160, easing: CLOSE_EASE, fill: "forwards" },
        ),
      );
    }

    const img = imgRef.current;
    if (active && canAnimate(img)) {
      const fromImg = img.getBoundingClientRect();
      const tileRect = findTileRect(composer, active.id);
      if (fromImg.width > 0 && fromImg.height > 0 && tileRect && toSlot && toSlot.width > 0 && fromBox.width > 0) {
        const boxScale = clamp(toSlot.width / fromBox.width, 0.72, 1.08);
        const targetCx = toSlot.left + (tileRect.left - fromBox.left) * boxScale + (tileRect.width * boxScale) / 2;
        const targetCy = toSlot.bottom - (fromBox.bottom - tileRect.bottom) * boxScale - (tileRect.height * boxScale) / 2;
        const imgDx = targetCx - (fromImg.left + fromImg.width / 2);
        const imgDy = targetCy - (fromImg.top + fromImg.height / 2);
        const imgScale = clamp(Math.min(tileRect.width / fromImg.width, tileRect.height / fromImg.height), 0.08, 0.7);
        closeAnims.current.push(
          img.animate(
            [
              { transform: "translate3d(0, 0, 0) scale(1)", opacity: 1 },
              { transform: `translate3d(${imgDx}px, ${imgDy}px, 0) scale(${imgScale})`, opacity: 0 },
            ],
            { duration: CLOSE_MS, easing: CLOSE_EASE, fill: "forwards" },
          ),
        );
      } else {
        closeAnims.current.push(
          img.animate(
            [
              { transform: "scale(1)", opacity: 1 },
              { transform: "scale(0.92)", opacity: 0 },
            ],
            { duration: CLOSE_MS, easing: CLOSE_EASE, fill: "forwards" },
          ),
        );
      }
    }

    const fallback = window.setTimeout(finish, CLOSE_MS + 60);
    if (leadAnim) {
      leadAnim.finished.then(finish, () => {});
    } else {
      finish();
    }

    return () => {
      window.clearTimeout(fallback);
      if (!done) cancelAll(closeAnims);
    };
  }, [closing, active, composerRef, slotRef]);

  // Handle Esc (yielding to popovers/dialogs/IME, beating ChatView's window listener and SidePanel) and Left/Right navigation.
  const handleKeyDown = useCallback((e: KeyboardEvent | React.KeyboardEvent) => {
    const cur = latest.current;
    const native = "nativeEvent" in e ? e.nativeEvent : e;
    if (cur.closing || e.defaultPrevented || native.isComposing || native.keyCode === 229) return;
    if (cur.modalOpen || hasExternalOverlay()) return;

    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      cur.onClose();
      return;
    }

    if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && cur.attachments.length > 1) {
      const target = e.target instanceof HTMLElement ? e.target : null;
      if (target && (target.closest("input, textarea, select, [contenteditable='true']") || target.isContentEditable)) return;
      const delta = e.key === "ArrowLeft" ? -1 : 1;
      const next = cur.attachments[cur.index + delta];
      if (!next) return;
      e.preventDefault();
      cur.onSelect(next.id);
    }
  }, []);

  useEffect(() => {
    // Listen on document (bubble) so it runs after inner React controls and yields to menus/dialogs,
    // while still firing before ChatView's window-level Escape interrupt listener.
    const onDocKey = (e: KeyboardEvent) => handleKeyDown(e);
    document.addEventListener("keydown", onDocKey);
    return () => document.removeEventListener("keydown", onDocKey);
  }, [handleKeyDown]);

  const step = (delta: number) => {
    const next = attachments[index + delta];
    if (next) onSelect(next.id);
  };

  const handleBackdropPointerDown = (e: React.PointerEvent) => {
    hadOverlayAtDown.current = modalOpen || hasExternalOverlay();
    didSwipe.current = false;
    if (e.pointerType !== "mouse" && many) {
      swipeStart.current = { x: e.clientX, y: e.clientY };
    }
  };

  const handleBackdropPointerUp = (e: React.PointerEvent) => {
    const start = swipeStart.current;
    swipeStart.current = null;
    if (!start || !many || closing) return;
    const dx = e.clientX - start.x;
    const dy = e.clientY - start.y;
    if (Math.abs(dx) > 48 && Math.abs(dy) < 72) {
      didSwipe.current = true;
      step(dx < 0 ? 1 : -1);
    }
  };

  const handleBackdropClick = (e: React.MouseEvent) => {
    if (closing || e.target !== e.currentTarget) return;
    if (didSwipe.current) {
      didSwipe.current = false;
      return;
    }
    if (hadOverlayAtDown.current) {
      hadOverlayAtDown.current = false;
      return;
    }
    onClose();
  };

  return (
    <div
      ref={rootRef}
      role="region"
      aria-label={t("chat-composeroverlays.image_stage_label")}
      data-composer-image-stage={closing ? "closing" : "open"}
      className={cx("fixed inset-x-0 flex flex-col select-none", closing && "pointer-events-none")}
      style={{ top: "var(--kb-top)", height: "var(--kb-h)", zIndex: 35 }}
      onKeyDown={handleKeyDown}
      {...drag}
    >
      {/* Dimmed frosted backdrop */}
      <div
        ref={backdropRef}
        data-testid="composer-image-backdrop"
        onPointerDown={handleBackdropPointerDown}
        onClick={handleBackdropClick}
        className={cx(
          "absolute inset-0 backdrop-blur-md",
          dragging && "ring-2 ring-inset ring-accent",
        )}
        style={{ backgroundColor: "rgba(0,0,0,0.8)" }}
      />

      {/* Top toolbar: image name, counter, Annotate, Close */}
      <div
        ref={headerRef}
        className="relative z-10 flex shrink-0 items-center justify-between gap-2 px-3 pb-2 sm:px-5"
        style={{ paddingTop: "max(12px, env(safe-area-inset-top))" }}
      >
        <div className="flex min-w-0 items-center gap-2">
          {active && (
            <span data-testid="composer-image-name" title={active.name} className="min-w-0 truncate text-meta font-medium text-white">
              {active.name}
            </span>
          )}
          {many && (
            <span
              data-testid="composer-image-counter"
              aria-live="polite"
              className="shrink-0 rounded-full bg-white/15 px-2.5 py-0.5 text-label font-medium tabular-nums whitespace-nowrap text-white"
            >
              {index + 1} / {attachments.length}
            </span>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-2">
          {active && (
            <button
              type="button"
              data-testid="composer-image-annotate"
              onClick={() => setEditingId(active.id)}
              className="touch-hit flex h-9 shrink-0 items-center gap-1.5 rounded-full bg-white/15 px-3 text-meta font-medium whitespace-nowrap text-white transition-colors hover:bg-white/25"
            >
              <Icon name="pencil" size={14} stroke={2} className="shrink-0" />
              <span>{t("chat-composeroverlays.image_annotate")}</span>
            </button>
          )}
          <button
            type="button"
            data-testid="composer-image-close"
            aria-label={t("chat-composeroverlays.image_close")}
            title={t("chat-composeroverlays.image_close")}
            onMouseDown={(e) => e.preventDefault()}
            onClick={onClose}
            className="touch-hit flex size-9 shrink-0 items-center justify-center rounded-full bg-white/15 text-white transition-colors hover:bg-white/25"
          >
            <Icon name="x" size={15} stroke={2.2} />
          </button>
        </div>
      </div>

      {/* Upper enlarged image stage, dynamically padded above the live composer */}
      <div
        ref={stageRef}
        data-testid="composer-image-stage-area"
        onPointerDown={handleBackdropPointerDown}
        onPointerUp={handleBackdropPointerUp}
        onPointerCancel={() => {
          swipeStart.current = null;
        }}
        onClick={handleBackdropClick}
        className="relative z-10 flex min-h-0 flex-1 items-center justify-center px-4 pt-1 sm:px-16"
        style={{ paddingBottom: "calc(var(--composer-stage-h, 152px) + max(12px, env(safe-area-inset-bottom)) + 16px)" }}
      >
        <div
          onPointerDown={handleBackdropPointerDown}
          onClick={handleBackdropClick}
          className="relative flex size-full min-h-0 items-center justify-center"
        >
          {active && (
            <img
              ref={imgRef}
              data-testid="composer-image-preview"
              src={active.url}
              alt={active.name}
              draggable={false}
              onLoad={playOpenImageFlip}
              className="max-h-full max-w-full rounded-card object-contain shadow-overlay"
              style={{ boxShadow: "0 0 0 1px rgba(255,255,255,0.12), var(--shadow-overlay)" }}
            />
          )}

          {many && !closing && (
            <>
              <button
                type="button"
                data-testid="composer-image-prev"
                aria-label={t("chat-composeroverlays.image_previous")}
                title={t("chat-composeroverlays.image_previous")}
                disabled={index <= 0}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => step(-1)}
                className="touch-hit absolute top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-white/15 text-white backdrop-blur-sm transition-colors hover:bg-white/25 disabled:pointer-events-none disabled:opacity-0"
                style={narrow ? { left: 6, backgroundColor: "rgba(0,0,0,0.5)", boxShadow: "inset 0 0 0 1px rgba(255,255,255,0.2)" } : { left: -44 }}
              >
                <Icon name="chevronLeft" size={18} stroke={2.2} />
              </button>
              <button
                type="button"
                data-testid="composer-image-next"
                aria-label={t("chat-composeroverlays.image_next")}
                title={t("chat-composeroverlays.image_next")}
                disabled={index >= attachments.length - 1}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => step(1)}
                className="touch-hit absolute top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full bg-white/15 text-white backdrop-blur-sm transition-colors hover:bg-white/25 disabled:pointer-events-none disabled:opacity-0"
                style={narrow ? { right: 6, backgroundColor: "rgba(0,0,0,0.5)", boxShadow: "inset 0 0 0 1px rgba(255,255,255,0.2)" } : { right: -44 }}
              >
                <Icon name="chevronRight" size={18} stroke={2.2} />
              </button>
            </>
          )}
        </div>
      </div>
      {editing && (
        <Suspense fallback={null}>
          <ImageEditor
            src={editing.edited?.source ?? editing.url}
            start={editing.edited}
            onCancel={() => setEditingId(null)}
            onDone={(img, state) => {
              const id = editing.id;
              setEditingId(null);
              onUpdateAttachment(id, img, state);
            }}
          />
        </Suspense>
      )}
    </div>
  );
}
