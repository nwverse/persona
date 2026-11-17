// A permission card that declares fields (a password the tool needs, the hub's `fields`): a chunk of its own, loaded
// with the first such card (Interactions.tsx InteractionCard).
import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { denyBody } from "../../lib/quick-answers";
import { cardToolName } from "../../lib/turns";
import type { Interaction, InteractionField } from "../../lib/types";
import { Button, SECRET_INPUT } from "../../ui/atoms";
import { useChatCardTapGuard } from "./card-tap-guard";
import { Card, PermissionBody, resolve, useCardKeys } from "./Interactions";

/** Control characters never go into a field: one pasted line break would hide behind a secret field's mask. */
const CONTROL = /\p{Cc}/gu;

/**
 * A permission card with fields to fill in (iOS PermissionInputForm, Android PermissionFieldsActions): what the tool
 * does, one input per field (a secret one masked), then Deny and Fill In & Allow. No Always allow, and no key allows
 * it: Allow is a press of its button once every required field holds more than spaces, and it sends the values with
 * the answer (the hub refuses an Allow without them). The values live only in this card's state: emptied as either
 * answer goes and when the card goes (a card with another id is a new FieldsPermissionCard, see InteractionCard).
 */
export function FieldsPermissionCard({ ix }: { ix: Interaction }) {
  const inputs: InteractionField[] = ix.fields ?? [];
  const [busy, setBusy] = useState<"allow" | "deny" | "hand_back" | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const box = useRef<HTMLDivElement>(null);
  const keys = useCardKeys();
  const tapGuard = useChatCardTapGuard(ix);
  // a field taken off the page keeps its value until collected, and Firefox offers to save a formless password field
  // removed while it holds one: they are emptied before React removes them (as SecretRunCard's)
  useLayoutEffect(() => {
    const el = box.current;
    return () => el?.querySelectorAll("input").forEach((input) => { input.value = ""; });
  }, []);
  const ready = inputs.every((f) => f.optional || (values[f.key] ?? "").trim() !== "");
  const send = async (body: Record<string, unknown>, key: "allow" | "deny" | "hand_back") => {
    setValues({});
    setBusy(key);
    await resolve(ix, body);
    setBusy(null);
  };
  const allow = () => {
    const filled = Object.fromEntries(inputs.filter((f) => (values[f.key] ?? "") !== "").map((f) => [f.key, values[f.key]]));
    void send({ allow: true, values: filled }, "allow");
  };
  return (
    <Card
      kicker={t("chat-interactions.permission_kicker")}
      title={t("chat-interactions.permission_title", { tool: cardToolName(ix) })}
      cardKeys={keys}
      tapGuard={tapGuard}
      onDismiss={() => send({ dismiss: true }, "hand_back")}
      busy={busy !== null}
      footer={
        <>
          <Button tone="quiet" busy={busy === "deny"} disabled={busy !== null} onClick={() => void send(denyBody(), "deny")}>
            {t("chat-interactions.deny")}
          </Button>
          <Button tone="primary" busy={busy === "allow"} disabled={busy !== null || !ready} onClick={allow}>
            {t("chat-interactions.fill_in_allow")}
          </Button>
        </>
      }
    >
      <PermissionBody ix={ix} />
      <div ref={box} className="mt-2 flex flex-col gap-2" data-permission-fields>
        {inputs.map((f) => (
          <label key={f.key} className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink-3">{f.label}</span>
            {/* No name and no form; a secret one is "one-time-code" so the browser neither offers to save it as this
                site's password nor suggests a generated one (SecretRunCard) */}
            <input
              type={f.secret ? "password" : "text"}
              value={values[f.key] ?? ""}
              onChange={(e) => {
                const typed = f.secret ? e.target.value.replace(CONTROL, "") : e.target.value;
                setValues((v) => ({ ...v, [f.key]: typed }));
              }}
              // Enter only puts the keyboard away: a press of Fill In & Allow answers, never a key while typing
              onKeyDown={(e) => {
                if (e.key !== "Enter") return;
                e.preventDefault();
                e.currentTarget.blur();
              }}
              placeholder={f.placeholder || f.label}
              disabled={busy !== null}
              {...(f.secret ? { ...SECRET_INPUT, autoComplete: "one-time-code" } : { autoComplete: "off" })}
              autoCapitalize="off"
              autoCorrect="off"
              enterKeyHint="done"
              data-field={f.key}
              className="h-8 w-full min-w-0 rounded-control bg-surface px-2.5 text-[13px] text-ink shadow-btn outline-none placeholder:text-ink-3"
            />
          </label>
        ))}
      </div>
    </Card>
  );
}
