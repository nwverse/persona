// End-to-end check of the web client's parts of the main chat's availability follow-ups (docs/web/web.md "Suggested
// rules", "Decisions held for the briefing", "Model configuration"): a suggested rule's line under each kind of card
// (#5802: the open plan, a decision, a question a supervisor passed up) with what Always stores, Always and Not now
// answered on the hub, what Always made and where it is listed, and a stale suggestion (404) that only clears the
// line; the mark on a decision held for the briefing (#5803), on its card in Needs you and on its To-dos row; and each
// model purpose's description under its name, with the background supervisor right after Main chat (#5806).
//
// Needs the web page against the fake hub with the suggestions, and Chrome (or any Chromium through --executable):
//
//   MOCK_TASKS=1 MOCK_RULE_SUGGESTIONS=1 PORT=18992 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18992 npx vite --port 5992 --strictPort &)
//   node web/e2e/rule-suggestions.mjs http://127.0.0.1:5992 --hub http://127.0.0.1:18992 [--shots <dir>] [--executable <chromium>] [--stale]
//
// Runs wide (1280) in light and dark in English and once each in 简体中文 and 日本語 for the strings, then the tray at a
// phone's width (390) in the same four; --shots saves each screen. --stale is for a hub started with
// MOCK_RULE_SUGGESTIONS=stale: Always is answered 404 and the line goes without a word. The hub's suggestions are put
// back (GET /mock/rule_suggestions?reset=1) before each pass and at the end. Prints one line per check and a verdict;
// exits 1 on a failure and after four minutes.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
const stale = args.includes("--stale");
const mockHub = (flag("--hub") ?? "").replace(/\/$/, "");
if (!base || !mockHub) {
  console.error("usage: node web/e2e/rule-suggestions.mjs <web URL> --hub <fake hub URL> [--shots <dir>] [--executable <chromium>] [--stale]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });
setTimeout(() => {
  console.log("FAIL timed out after 4 minutes");
  process.exit(1);
}, 240_000).unref();

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}
const check = (cond, msg) => {
  if (!cond) fail(msg);
};

const hub = (path, init) => fetch(`${base}${path}`, { ...init, headers: { Authorization: "Bearer mock", "Content-Type": "application/json" }, signal: AbortSignal.timeout(5000) }).then((r) => r.json());
/** The fake hub's own routes (/mock/…), which the web server does not pass on. */
const mock = (path) => fetch(`${mockHub}/mock/${path}`, { signal: AbortSignal.timeout(5000) }).then((r) => r.json());
const reset = () => mock("rule_suggestions?reset=1");
const assistant = await hub("/api/v1/assistant");
const todos = (await hub("/api/v1/todos")).todos;
if (!todos.some((t) => t.id === "todo_task_merge")) fail("the fake hub has no suggested rules (start it with MOCK_TASKS=1 MOCK_RULE_SUGGESTIONS=1)");

const WORDS = {
  en: { always: "Always", notNow: "Not now", needs: "Needs you", creates: "Always adds a standing authorization", trust: "Always remembers this kind of task", none: "Always stores no rule here", made: "Standing authorization added.", where: "Standing authorizations", hidden: "Not suggested again for 30 days.", briefing: "With the next briefing", pushed: "nothing was pushed", describes: "Talks with you" },
  "zh-Hans": { always: "总是", notNow: "暂不", needs: "需要你", creates: "新增一条长期授权", trust: "会记住这类任务", none: "不会在这里存下规则", made: "已新增长期授权。", where: "长期授权", hidden: "30 天内不再建议。", briefing: "随下次简报送达", pushed: "没有推送提醒", describes: "和你对话" },
  ja: { always: "常に", notNow: "今はしない", needs: "要対応", creates: "継続的な承認を追加します", trust: "この種類のタスクを記憶します", none: "ここにルールは保存されません", made: "継続的な承認を追加しました。", where: "継続的な承認", hidden: "30 日間は提案しません。", briefing: "次のブリーフィングで届きます", pushed: "通知は送っていません", describes: "あなたと話し" },
};

async function open(browser, theme, lang, width = 1280, height = 900) {
  const ctx = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 2, colorScheme: theme, reducedMotion: "reduce", hasTouch: width < 600, locale: lang === "zh-Hans" ? "zh-CN" : lang });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate(([t, l]) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", l);
  }, [theme, lang]);
  return { ctx, page };
}
const shot = async (page, name, target) => {
  if (!shots) return;
  await page.waitForTimeout(350); // fonts and the row highlight settle
  if (target) await target.screenshot({ path: join(shots, `${name}.png`) });
  else await page.screenshot({ path: join(shots, `${name}.png`) });
};

/** The main chat with the Needs you tray open, once every card with a suggestion drew its line. */
async function openTray(page, where) {
  await page.goto(`${base}/persona/${assistant.main}`);
  const entry = page.locator('[data-tray-entry="needs"]');
  await entry.waitFor({ state: "visible", timeout: 20000 }).catch(() => fail(`${where}: the main chat has no Needs you entry`));
  if ((await entry.getAttribute("aria-expanded")) !== "true") await entry.click();
  await page.locator("[data-rule-suggestion]").first().waitFor({ state: "visible", timeout: 15000 }).catch(() => fail(`${where}: no card in Needs you carries a suggested rule's line`));
  await page.waitForFunction(() => document.querySelectorAll("[data-rule-suggestion]").length >= 3, null, { timeout: 10000 }).catch(() => {});
}
const line = (page, kind) => page.locator(`[data-rule-suggestion="${kind}"]`).first();
/** A picture of a line with the card it is under (its previous sibling, or what that one holds when it is `contents`),
 *  as much of both as is on screen. */
async function shotWithCard(page, name, l) {
  if (!shots) return;
  await l.scrollIntoViewIfNeeded();
  await page.waitForTimeout(350);
  const clip = await l.evaluate((el) => {
    const own = el.getBoundingClientRect();
    let card = el.previousElementSibling;
    while (card && !card.getBoundingClientRect().height) card = card.firstElementChild;
    const c = (card ?? el).getBoundingClientRect();
    const top = Math.max(0, Math.min(own.top, c.top) - 4), left = Math.max(0, Math.min(own.left, c.left) - 4);
    return { x: left, y: top, width: Math.min(innerWidth, Math.max(own.right, c.right) + 4) - left, height: Math.min(innerHeight, own.bottom + 4) - top };
  });
  await page.screenshot({ path: join(shots, `${name}.png`), clip });
}
/** Nothing of the line is wider than the screen, and its buttons are one line each. */
async function fits(page, l, where) {
  const m = await l.evaluate((el) => {
    const r = el.getBoundingClientRect();
    const buttons = [...el.querySelectorAll("button")].map((b) => ({ h: b.getBoundingClientRect().height, wraps: getComputedStyle(b).whiteSpace !== "nowrap", right: b.getBoundingClientRect().right }));
    return { left: r.left, right: r.right, width: innerWidth, scroll: el.scrollWidth - el.clientWidth, buttons };
  });
  check(m.left >= 0 && m.right <= m.width + 0.5 && m.scroll <= 1, `${where}: the line is wider than the screen (${JSON.stringify(m)})`);
  check(m.buttons.length === 2 && m.buttons.every((b) => !b.wraps && b.h <= 34 && b.right <= m.width + 0.5), `${where}: the two buttons are not single-line inside the screen (${JSON.stringify(m.buttons)})`);
}

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
try {
  for (const [theme, lang] of [["light", "en"], ["dark", "en"], ["light", "zh-Hans"], ["light", "ja"]]) {
    const w = WORDS[lang];
    const where = `${theme} ${lang}`;
    await reset();
    const { ctx, page } = await open(browser, theme, lang);

    // 1. Needs you: a line under each kind of card, saying what Always stores
    await openTray(page, where);
    const kinds = await page.$$eval("[data-rule-suggestion]", (xs) => xs.map((x) => x.dataset.ruleSuggestion));
    for (const kind of ["plan_start", "standing_authorization", "delegate"]) check(kinds.includes(kind), `${where}: no ${kind} line in Needs you (${kinds.join(",")})`);
    for (const [kind, creates] of [["plan_start", w.trust], ["standing_authorization", w.creates], ["delegate", w.none]]) {
      const l = line(page, kind);
      const text = await l.locator("[data-rule-creates]").textContent();
      check(text.includes(creates), `${where}: the ${kind} line does not say what Always stores: ${text}`);
      const buttons = await l.locator("button").allTextContents();
      check(buttons.join("|") === `${w.always}|${w.notNow}`, `${where}: the ${kind} line's buttons are ${buttons.join("|")}`);
      await fits(page, l, `${where} ${kind}`);
      await l.scrollIntoViewIfNeeded();
      await shotWithCard(page, `suggestion-${kind}-${theme}-${lang}`, l);
    }
    const quote = await line(page, "standing_authorization").locator("[data-rule-creates]").textContent();
    check(/[“「].+→.+[”」]/.test(quote), `${where}: the standing authorization's line does not quote the owner's words: ${quote}`);
    await line(page, "plan_start").scrollIntoViewIfNeeded();
    await shot(page, `needs-you-${theme}-${lang}`);
    ok(`${where}: the open plan, the decision and the passed-up question each carry a line saying what Always stores, with ${w.always} / ${w.notNow}`);

    // 2. the decision held for the briefing: its card says so, with what that means
    const held = page.locator('[data-needs-row="task:todo_task_copy"]');
    await held.waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${where}: the held decision is not in Needs you`));
    const mark = held.locator("[data-for-briefing]");
    check((await mark.count()) === 1, `${where}: the held decision's card has no mark`);
    const said = await mark.textContent();
    check(said.includes(w.briefing) && said.includes(w.pushed), `${where}: the mark does not say it comes with the briefing and why: ${said}`);
    check((await page.locator('[data-needs-row="task:todo_task_merge"] [data-for-briefing]').count()) === 0, `${where}: a decision that was pushed carries the mark`);
    await held.scrollIntoViewIfNeeded();
    await shot(page, `held-card-${theme}-${lang}`, held);
    ok(`${where}: the held decision's card says “${w.briefing}” with its definition; the pushed one does not`);

    // 3. Not now on the question's line: the hub is told, the line goes, a word says for how long
    await line(page, "delegate").getByRole("button", { name: w.notNow, exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(`[data-rule-suggestion="delegate"]`), null, { timeout: 10000 }).catch(() => fail(`${where}: Not now did not clear the line`));
    if (!stale) {
      check((await mock("rule_suggestions")).answered.rs_mock_delegate === "not-now", `${where}: the hub did not get Not now`);
      await page.getByText(w.hidden).first().waitFor({ state: "visible", timeout: 5000 }).catch(() => fail(`${where}: nothing said “${w.hidden}”`));
    }
    ok(`${where}: Not now clears the line${stale ? " (stale: 404)" : " and is saved on the hub"}`);

    // 4. Always on the decision's line: the rule is made and the line says where it is listed and removed
    const merge = line(page, "standing_authorization");
    await merge.getByRole("button", { name: w.always, exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(`[data-rule-suggestion="standing_authorization"]`), null, { timeout: 10000 }).catch(() => fail(`${where}: Always did not clear the line`));
    if (stale) {
      await page.waitForTimeout(600);
      check((await page.locator("[data-rule-result]").count()) === 0, `${where}: a stale suggestion (404) still said a rule was made`);
      check((await hub("/api/v1/assistant/standing-authorizations")).authorizations.length === 0, `${where}: the hub made a rule for a stale suggestion`);
      await shot(page, `stale-${theme}-${lang}`);
      ok(`${where}: a stale suggestion (404) clears the line and says nothing was made`);
    } else {
      const result = page.locator("[data-rule-result]").first(); // (the line it replaced is gone: found on its own)
      await result.waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${where}: nothing said what Always made`));
      const text = await result.textContent();
      check(text.includes(w.made) && text.includes(w.where), `${where}: the result does not say what was made and where it is listed: ${text}`);
      const grants = (await hub("/api/v1/assistant/standing-authorizations")).authorizations;
      check(grants.length === 1 && grants[0].actions[0] === "merge", `${where}: the hub has ${grants.length} standing authorizations after Always`);
      // the to-dos were read again without the suggestion: what was made stays said
      await page.waitForTimeout(1200);
      check(await result.isVisible(), `${where}: the result went when the hub took the suggestion off the card`);
      await result.scrollIntoViewIfNeeded();
      await shotWithCard(page, `result-${theme}-${lang}`, result);
      await shot(page, `needs-you-after-${theme}-${lang}`);
      // where it is listed: Settings › Assistant
      await result.getByRole("button").click();
      await page.waitForURL(/\/settings\?tab=persona/, { timeout: 10000 }).catch(() => fail(`${where}: the result's button did not open Settings`));
      const listed = page.locator('[data-settings-group="standing-authorizations"]');
      await listed.waitFor({ state: "visible", timeout: 15000 }).catch(() => fail(`${where}: Settings has no standing authorizations`));
      await page.getByText(grants[0].quote).first().waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${where}: the grant Always made is not listed in Settings`));
      await listed.scrollIntoViewIfNeeded();
      await shot(page, `listed-${theme}-${lang}`);
      ok(`${where}: Always makes the standing authorization, the line says “${w.made}” and where it is listed, and Settings lists it`);
    }

    // 5. To-dos: the held decision's row carries the quiet mark, with its definition on hover
    await page.goto(`${base}/todos`);
    const row = page.locator('[data-for-briefing="mark"]').first(); // right under the row's ask line
    await row.waitFor({ state: "visible", timeout: 20000 }).catch(() => fail(`${where}: no To-dos row carries the held-for-briefing mark`));
    const rowMark = row;
    check((await rowMark.textContent()) === w.briefing, `${where}: the row's mark says ${await rowMark.textContent()}`);
    check((await rowMark.getAttribute("title")).includes(w.pushed), `${where}: the row's mark has no definition on hover`);
    check((await page.locator('[data-for-briefing="mark"]').count()) === 1, `${where}: more than the held decision's row carries the mark`);
    await row.scrollIntoViewIfNeeded();
    await shot(page, `todos-${theme}-${lang}`);
    ok(`${where}: the held decision's To-dos row carries “${w.briefing}”, defined on hover; no other row does`);

    // 6. Model settings: each purpose's description under its name; the background supervisor right after Main chat
    await page.goto(`${base}/settings?tab=model-config`);
    await page.waitForSelector("[data-model-purpose]", { timeout: 15000 });
    const ids = await page.$$eval("[data-model-purpose]", (xs) => xs.map((x) => x.dataset.modelPurpose));
    check(ids[ids.indexOf("assistant") + 1] === "assistant.supervise", `${where}: Model settings lists ${ids.join(",")}: the background supervisor is not right after Main chat`);
    check(ids.filter((id) => id === "assistant.supervise").length === 1, `${where}: the background supervisor is listed ${ids.filter((id) => id === "assistant.supervise").length} times`);
    const described = await page.$$eval("[data-model-purpose]", (xs) => xs.map((x) => [x.dataset.modelPurpose, x.querySelector('[data-testid="purpose-description"]')?.textContent ?? ""]));
    check(described.every(([, d]) => d.length > 10), `${where}: a purpose has no description: ${JSON.stringify(described.filter(([, d]) => d.length <= 10))}`);
    check(described[0][1].includes(w.describes), `${where}: Main chat's description is not the hub's in this language: ${described[0][1]}`);
    const under = await page.$$eval("[data-model-purpose]", (xs) => xs.every((x) => {
      const d = x.querySelector('[data-testid="purpose-description"]');
      return d && d.parentElement.firstElementChild === d;
    }));
    check(under, `${where}: a description is not the first line under its purpose's name`);
    await shot(page, `models-${theme}-${lang}`);
    ok(`${where}: Model settings shows each purpose's description under its name, the background supervisor once, right after Main chat`);
    await ctx.close();
  }

  // 7. a phone's width (390): the line wraps inside the screen and its two buttons stay whole on one line each
  for (const [theme, lang] of [["light", "en"], ["dark", "en"], ["light", "zh-Hans"], ["light", "ja"]]) {
    const where = `390 ${theme} ${lang}`;
    await reset();
    const { ctx, page } = await open(browser, theme, lang, 390, 844);
    await openTray(page, where);
    for (const kind of ["plan_start", "standing_authorization", "delegate"]) {
      const l = line(page, kind);
      await l.scrollIntoViewIfNeeded();
      await fits(page, l, `${where} ${kind}`);
      await shot(page, `narrow-${kind}-${theme}-${lang}`);
    }
    const held = page.locator('[data-needs-row="task:todo_task_copy"]');
    await held.scrollIntoViewIfNeeded();
    const m = await held.locator("[data-for-briefing]").evaluate((el) => ({ right: el.getBoundingClientRect().right, width: innerWidth, scroll: el.scrollWidth - el.clientWidth }));
    check(m.right <= m.width + 0.5 && m.scroll <= 1, `${where}: the held decision's caption is wider than the screen (${JSON.stringify(m)})`);
    await shot(page, `narrow-held-${theme}-${lang}`);
    if (!stale) {
      const merge = line(page, "standing_authorization");
      await merge.scrollIntoViewIfNeeded();
      await merge.getByRole("button", { name: WORDS[lang].always, exact: true }).click();
      const result = page.locator("[data-rule-result]").first(); // (the line it replaced is gone: found on its own)
      await result.waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${where}: nothing said what Always made`));
      const r = await result.evaluate((el) => ({ right: el.getBoundingClientRect().right, width: innerWidth, scroll: el.scrollWidth - el.clientWidth }));
      check(r.right <= r.width + 0.5 && r.scroll <= 1, `${where}: the result is wider than the screen (${JSON.stringify(r)})`);
      await result.scrollIntoViewIfNeeded();
      await shot(page, `narrow-result-${theme}-${lang}`);
    }
    ok(`${where}: every line and the held decision's caption stay inside the screen, the buttons on one line`);
    await ctx.close();
  }
  console.log("PASS");
} finally {
  await reset().catch(() => {});
  await browser.close();
}
