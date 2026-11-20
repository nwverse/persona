// Run with the repository's mock hub and the Web dev server. Only the project responses are supplied here.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [base, out, mode] = process.argv.slice(2);
if (!base || !out) throw new Error("Usage: node e2e/knowledge-project-evidence.mjs <Web URL> <screenshot directory> [baseline]");
await mkdir(out, { recursive: true });
const baseline = mode === "baseline";
const contexts = baseline ? [["desktop", "light", "standard", "en"]] : [
  ["desktop", "light", "standard", "en"], ["desktop", "dark", "standard", "en"],
  ["phone", "light", "large", "en"], ["phone", "dark", "large", "en"],
  ["phone", "light", "large", "zh-Hans"], ["phone", "dark", "large", "ja"],
];
const localized = {
  en: { review: "Summary needs review", gap: "3 newer updates", title: "Sources and coverage", edit: "Edit storage", save: "Save", refs: "Source references", mode: "References only" },
  "zh-Hans": { review: "摘要待复核", gap: "有 3 条新进展", title: "来源与覆盖范围", edit: "编辑存储方式", save: "保存", refs: "来源引用", mode: "仅保留引用" },
  ja: { review: "要約に確認が必要", gap: "新しい更新 3 件", title: "情報源と対象範囲", edit: "保存方法を編集", save: "保存", refs: "参照先", mode: "参照のみ" },
};
const name = "Community garden";
const overview = "The planting plan has been approved.";
const summary = "The planting plan is waiting for approval.";
const pointer = "https://example.org/garden/planting-plan";
const linked = "chats/garden-review";
const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const [layout, theme, scale, language] of contexts) {
    const phone = layout === "phone";
    const context = await browser.newContext({ viewport: phone ? { width: 390, height: 844 } : { width: 1360, height: 900 }, deviceScaleFactor: 1, colorScheme: theme, reducedMotion: "reduce", hasTouch: phone, isMobile: phone });
    await context.addInitScript(([theme, scale, language]) => {
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.text-size", scale);
      localStorage.setItem("persona.language", language);
    }, [theme, scale, language]);
    let policy;
    let refs = [pointer];
    const writes = [];
    const evidence = () => ({
      needs_review: policy !== "reference-only", timeline_end: 218,
      fields: {
        summary: { needs_review: policy !== "reference-only", review_reason: policy === "reference-only" ? "reference-only" : "new_evidence", cutoff: { timeline_from: 0, timeline_through: 72, sources: [{ ref: linked, position: "initial-review" }] }, uncovered_evidence_count: policy === "reference-only" ? undefined : 3, lock_reason: "field_locked", review_by: "user" },
        one_line: { needs_review: false, cutoff: { timeline_from: 0, timeline_through: 218, sources: [{ ref: linked, position: "merged-review" }] }, writer: "persona", source: "kb.project", review_by: "persona" },
      },
    });
    const sources = () => [{ id: "github:example/garden#4", url: "https://github.com/example/garden/pull/4", refs: [linked], delivery_state: "merged", delivery_at: "2026-11-18T10:00:00Z", environment_verification: "unverified" }, ...refs.map(url => ({ id: url, url }))];
    const entry = { kind: "projects", id: "garden", name, one_line: overview, relevance: 65, importance: "P2", extra: "short · active", tags: ["garden"] };
    await context.route(/\/api\/v1\/kb\/projects(?:[/?]|$)/, async route => {
      const url = new URL(route.request().url());
      if (url.pathname === "/api/v1/kb/projects") return route.fulfill({ json: { items: [{ ...entry, freshness: evidence(), project_mode: policy, sources: sources() }] } });
      if (url.pathname !== "/api/v1/kb/projects/garden") return route.fallback();
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        writes.push(body);
        await new Promise(resolve => setTimeout(resolve, 100));
        if (body.project_mode) policy = body.project_mode === "full" ? undefined : body.project_mode;
        if (body.source_urls) refs = body.source_urls;
        return route.fulfill({ json: { id: "garden" } });
      }
      return route.fulfill({ json: { detail: {
        kind: "projects", id: "garden", path: "knowledge/projects/garden",
        meta: { ...entry, kind: "short", status: "active", pinned: "Keep the east entrance clear.", source_urls: refs },
        summary, log: ["2026-11-18 10:00 · Garden committee · The plan was approved."],
        links: [{ kind: "chats", id: "garden-review", name: "Planting plan review", relevance: 0 }],
        project_mode: policy, freshness: evidence(), sources: sources(),
      } } });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    const words = localized[language];
    const shot = async stage => {
      await page.screenshot({ path: join(out, `web-${baseline ? "before-" : ""}${layout}-${theme}-${scale}-${language}-${stage}.png`), animations: "disabled" });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "page overflows horizontally");
    };
    await page.goto(`${base}/kb/projects?token=mock`);
    const row = page.getByRole("link").filter({ hasText: name });
    await row.first().waitFor({ timeout: 30_000 });
    assert.equal(await row.first().getByText(words.review, { exact: false }).count(), baseline ? 0 : 1);
    if (!baseline) assert.match(await row.first().innerText(), new RegExp(words.gap));
    await shot("list");
    await row.first().click();
    await page.getByRole("heading", { name, exact: true }).waitFor({ timeout: 30_000 });
    await page.getByText(summary, { exact: false }).last().waitFor();
    if (baseline) {
      assert.equal(await page.locator("summary").filter({ hasText: words.title }).count(), 0);
      await shot("detail");
    } else {
      const disclosure = page.locator("summary").filter({ hasText: words.title });
      assert.equal(await disclosure.evaluate(n => n.parentElement.open), false);
      await shot("detail");
      await disclosure.click();
      const region = page.locator("details").filter({ has: disclosure });
      await region.getByText("Planting plan review", { exact: true }).waitFor();
      await region.scrollIntoViewIfNeeded();
      await shot("sources");
      await region.getByRole("button", { name: words.edit }).click();
      const field = region.getByRole("textbox", { name: words.refs });
      assert.equal(await field.inputValue(), pointer);
      await region.getByRole("switch").click();
      assert.equal(writes.length, 0);
      await region.getByRole("button", { name: words.save }).scrollIntoViewIfNeeded();
      await shot("storage");
      await region.getByRole("button", { name: words.save }).click();
      await region.getByRole("button", { name: words.edit }).waitFor();
      assert.deepEqual(writes, [{ project_mode: "reference-only" }]);
      await region.getByRole("heading", { name: words.mode, exact: true }).waitFor();
      await shot("reference-only");
      await region.getByRole("button", { name: words.edit }).click();
      const sw = region.getByRole("switch");
      assert.equal(await sw.getAttribute("aria-checked"), "true");
      await sw.click();
      await region.getByRole("button", { name: words.save }).click();
      await region.getByRole("button", { name: words.edit }).waitFor();
      assert.deepEqual(writes[1], { project_mode: "full" });
    }
    assert.deepEqual(errors, []);
    console.log(`PASS ${layout} ${theme} ${scale} ${language}: ${baseline ? "baseline without review or storage controls" : "review shown, sources opened, policy toggled both ways"}`);
    await context.close();
  }
} finally {
  await browser.close();
}
