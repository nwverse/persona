// A thought's words with their lightweight inline emphasis (lib/thinking-text.ts, after cindy's ThinkingText.tsx): paired
// `**strong**` in medium weight, backtick code in monospace and upright; nothing else is interpreted.
import { Fragment, memo, useMemo } from "react";
import { tokenizeThinkingText } from "../../lib/thinking-text";

export const ThinkingText = memo(function ThinkingText({ content }: { content: string }) {
  const tokens = useMemo(() => tokenizeThinkingText(content), [content]);
  return (
    <>
      {tokens.map((token, i) =>
        token.kind === "strong" ? (
          <span key={i} className="font-medium" data-thinking-markup="strong">
            {token.value}
          </span>
        ) : token.kind === "code" ? (
          <code key={i} className="font-mono not-italic" data-thinking-markup="code">
            {token.value}
          </code>
        ) : (
          <Fragment key={i}>{token.value}</Fragment>
        ),
      )}
    </>
  );
});
