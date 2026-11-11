#!/usr/bin/env python3
"""End-to-end check of Settings › Model providers (#2455, docs/integrations/launchers.md): the three-step add wizard (pick → connect → models,
"Advanced" folded), "Test" passing and failing, "Fetch models", the advanced fields coming back when a provider is reopened, a PUT
that leaves out what was not touched, and the import from a computer's aliases (#5024: the header button, the preview with the file
each alias came from, variables carried over and left out, a group's "A provider for each alias" / "One provider" choice by mouse
and keyboard, hiding the imported aliases with why the others stay, merge, the outdated and offline computers).

Needs the web page against the fake hub with MOCK_PROVIDERS=1 and Playwright for Python:

    MOCK_PROVIDERS=1 PORT=19011 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19011 npx vite --port 5411 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_providers_e2e.py http://127.0.0.1:5411 \
        --hub http://127.0.0.1:19011 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves 1280 / 390 wide, light and dark.
Drives the installed Chrome (channel="chrome").
"""
import argparse
import json
import os
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright


def ok(msg):
    print("ok  " + msg, flush=True)


class Run:
    def __init__(self, page, base, hub, token, shots):
        self.page, self.base, self.hub, self.token, self.shots = page, base, hub, token, shots

    def log(self):
        req = urllib.request.Request(self.hub + "/mock/providers/log", headers={"Authorization": "Bearer " + self.token})
        with urllib.request.urlopen(req, timeout=5) as r:
            return json.load(r)

    def shot(self, name):
        if not self.shots:
            return
        w = self.page.viewport_size["width"]
        for theme in ("light", "dark"):
            self.page.evaluate(f"document.documentElement.classList.toggle('dark', {'true' if theme == 'dark' else 'false'})")
            self.page.wait_for_timeout(350)
            self.page.screenshot(path=os.path.join(self.shots, f"{name}-{w}-{theme}.png"))
        self.page.evaluate("document.documentElement.classList.remove('dark')")

    def open_models(self):
        self.page.goto(self.base + "/overview?token=" + self.token)
        self.page.locator("aside").first.wait_for()
        self.page.goto(self.base + "/settings?tab=models")
        expect(self.page.get_by_text("gw_full", exact=True).first).to_be_visible()

    def dialog(self, title):
        d = self.page.get_by_role("dialog", name=title)
        expect(d).to_be_visible()
        return d


def wizard(r):
    page = r.page
    page.get_by_role("button", name="新增").click()
    d = r.dialog("新增供应商")
    kinds = d.get_by_role("radiogroup", name="供应商类型")
    expect(kinds.get_by_role("radio", name="自定义 Anthropic 兼容网关")).to_have_attribute("aria-checked", "true")
    kinds.get_by_role("radio", name="Anthropic 官方").click()
    expect(kinds.get_by_role("radio", name="Anthropic 官方")).to_have_attribute("aria-checked", "true")
    r.shot("wizard-pick")
    kinds.get_by_role("radio", name="自定义 Anthropic 兼容网关").click()
    d.get_by_role("button", name="下一步").click()
    ok("step 1 offers Anthropic 官方 and a compatible gateway")

    # step 2: Bearer by default, the key a secret
    expect(d.get_by_role("radio", name="Bearer")).to_have_attribute("aria-checked", "true")
    d.get_by_label("名称", exact=True).fill("e2e_gw")
    d.get_by_label("地址", exact=True).fill("https://bad.example.com")
    key = d.get_by_label("API Key", exact=True)
    key.fill("sk-test-e2e")
    expect(key).to_have_attribute("type", "password")
    d.get_by_role("button", name="测试连接").click()
    expect(d.get_by_role("status").filter(has_text="401 认证失败")).to_be_visible()
    expect(d.get_by_text("可能产生少量费用")).to_be_visible()
    d.get_by_role("button", name="获取模型列表").click()
    expect(d.get_by_text("拉不到模型列表")).to_be_visible()
    ok("测试连接 shows the failure; a failed 获取模型列表 says models can be added by hand")

    d.get_by_label("地址", exact=True).fill("https://relay.example.com")
    d.get_by_role("button", name="获取模型列表").click()
    expect(d.get_by_text("找到 3 个模型")).to_be_visible()
    d.get_by_role("button", name="测试连接").click()
    expect(d.get_by_text("连接成功（412 ms）")).to_be_visible()
    test = r.log()["test"][-1]
    assert test["api_key"] == "<set>" and test["base_url"] == "https://relay.example.com" and test["model"] == "example-opus", test
    r.shot("wizard-connect")
    ok("测试连接 passes in 412 ms with the first discovered model; 获取模型列表 finds 3")

    # step 3: pick, set the default, edit a context window, add one by hand, fill "Advanced"
    d.get_by_role("button", name="下一步").click()
    rows = d.get_by_role("list", name="模型").locator("li")
    expect(rows).to_have_count(3)
    d.get_by_role("checkbox", name="选用 example-haiku").click()
    sonnet = d.locator('[data-model="example-sonnet"]')
    sonnet.get_by_role("button", name="设为默认").click()
    expect(sonnet.get_by_text("默认", exact=True)).to_be_visible()
    d.get_by_label("example-sonnet 的上下文窗口").fill("300000")
    d.get_by_role("group", name="example-sonnet 支持的思考档位").get_by_role("button", name="很高").click()
    d.get_by_label("手动添加模型 ID").fill("my-extra")
    d.get_by_role("button", name="添加").click()
    expect(rows).to_have_count(4)
    adv = d.get_by_role("button", name="高级")
    expect(adv).to_have_attribute("aria-expanded", "false")
    adv.click()
    d.get_by_label("Opus 档模型").fill("example-opus")
    d.get_by_label("代理", exact=True).fill("http://127.0.0.1:7890")
    d.get_by_label("超时（毫秒）").fill("120000")
    d.get_by_role("switch", name="强制子代理用这个模型").click()
    d.get_by_label("请求头", exact=True).fill("X-Team: e2e")
    d.get_by_label("默认思考档位", exact=True).select_option("max")
    r.shot("wizard-models")
    d.get_by_role("button", name="保存", exact=True).click()
    expect(d).to_be_hidden()
    post = r.log()["post"]
    assert post["model"] == "example-sonnet", post
    assert [m["id"] for m in post["models"]] == ["example-opus", "example-sonnet", "my-extra"], post["models"]
    assert next(m for m in post["models"] if m["id"] == "example-sonnet")["context_window"] == 300000, post["models"]
    assert post["timeout_ms"] == 120000 and post["subagent_force"] is True and post["headers"] == "<set>" and post["auth"] == "bearer", post
    assert post["opus_model"] == "example-opus" and post["proxy"] == "http://127.0.0.1:7890" and post["effort"] == "max", post
    ok("保存 posts the checked models, the default, the edited window and the advanced fields")

    # reopened: everything comes back
    edit(r, "e2e_gw")
    d = r.dialog("编辑 e2e_gw")
    d.get_by_role("button", name="下一步").click()
    expect(d.locator('[data-model="example-sonnet"]').get_by_text("默认", exact=True)).to_be_visible()
    expect(d.get_by_label("example-sonnet 的上下文窗口")).to_have_value("300000")
    d.get_by_role("button", name="高级").click()
    expect(d.get_by_label("超时（毫秒）")).to_have_value("120000")
    expect(d.get_by_label("代理", exact=True)).to_have_value("http://127.0.0.1:7890")
    expect(d.get_by_role("switch", name="强制子代理用这个模型")).to_have_attribute("aria-checked", "true")
    expect(d.get_by_label("默认思考档位", exact=True)).to_have_value("max")
    expect(d.get_by_label("请求头", exact=True)).to_have_attribute("placeholder", "已设置（不显示）；输入新的会整体替换")
    d.get_by_role("button", name="取消").click()
    ok("reopening the new provider brings back its models and advanced fields")


def edit(r, name):
    r.page.get_by_role("button", name=name).first.click()
    detail = r.page.get_by_role("dialog").filter(has_text=name)
    detail.get_by_role("button", name="编辑").click()


def edit_full(r):
    edit(r, "gw_full")
    d = r.dialog("编辑 gw_full")
    expect(d.get_by_label("名称", exact=True)).to_be_disabled()
    d.get_by_role("button", name="选择模型").click()
    d.get_by_role("button", name="高级").click()
    for label, value in (("Opus 档模型", "example-opus"), ("Sonnet 档模型", "example-sonnet"), ("Haiku 档模型", "example-haiku"),
                         ("Fable 档模型", "example-opus"), ("小模型", "example-haiku"), ("子代理模型", "example-sonnet"),
                         ("代理", "http://127.0.0.1:7890"), ("NO_PROXY", "localhost,127.0.0.1"), ("超时（毫秒）", "600000"),
                         ("自动压缩窗口", "400000"), ("最大上下文", "800000")):
        expect(d.get_by_label(label, exact=True)).to_have_value(value)
    expect(d.get_by_label("默认思考档位", exact=True)).to_have_value("high")
    r.shot("edit-advanced")
    d.get_by_label("超时（毫秒）").fill("900000")
    d.get_by_role("button", name="保存", exact=True).click()
    expect(d).to_be_hidden()
    put = r.log()["put"]
    assert put["timeout_ms"] == 900000, put
    for k in ("headers", "api_key", "models", "proxy", "opus_model", "effort", "subagent_force"):
        assert k not in put, (k, put)
    ok("editing gw_full restores every advanced field; the PUT carries only the changed timeout")

    edit(r, "gw_full")
    d = r.dialog("编辑 gw_full")
    d.get_by_role("button", name="选择模型").click()
    d.get_by_role("button", name="高级").click()
    expect(d.get_by_label("超时（毫秒）")).to_have_value("900000")
    d.get_by_role("button", name="清除已保存的请求头").click()
    d.get_by_role("button", name="保存", exact=True).click()
    expect(d).to_be_hidden()
    assert r.log()["put"]["headers"] == "", r.log()["put"]
    ok("the saved timeout reopens; 清除已保存的请求头 sends headers as an empty string")


def zshrc_import(r):
    page = r.page
    page.get_by_role("button", name="导入", exact=True).click()
    d = r.dialog("从电脑上的 alias 导入")
    expect(d.get_by_text("~/.zshrc 和 ~/.bashrc", exact=False)).to_be_visible()
    d.get_by_label("电脑", exact=True).select_option("macbook")
    rows = d.get_by_role("list", name="可以导入的供应商").get_by_role("checkbox")
    expect(rows).to_have_count(4)
    full = d.locator('[data-import="cc_full"]')
    expect(full).to_contain_text("合并到 gw_full")
    expect(full).to_contain_text("gw.example.com")
    expect(full).to_contain_text("来自 ~/.zshrc 里的 cc_full、cc_full_haiku")
    kimi = d.locator('[data-import="cc_kimi"]')
    expect(kimi).to_contain_text("2 个模型")
    expect(kimi).to_contain_text("2 个供应商")
    ok("the preview lists 4 providers: host, model count, the file the aliases came from, one 合并到 gw_full")

    relay = d.locator('[data-import="cc_relay"]')
    expect(relay).to_contain_text("来自 ~/.bashrc 里的 cc_relay")
    relay_detail = d.locator('[data-import-detail="cc_relay"]')
    expect(relay_detail).to_contain_text("一并带上 CLAUDE_CODE_MAX_OUTPUT_TOKENS、DISABLE_TELEMETRY。")
    expect(relay_detail).to_contain_text("不带上 RELAY_TOKEN：看起来是密钥或密码。")
    expect(relay_detail).to_contain_text("不带上 WORKDIR：值要在 shell 里才能算出来。")
    expect(relay_detail).to_contain_text("不带上 HOME：只适用于那台电脑。")
    ok("a ~/.bashrc alias says which variables it carries over and why the others are left out")

    choice = d.get_by_role("radiogroup", name="怎样导入 cc_kimi")
    each, one = choice.get_by_role("radio", name="每个 alias 一个供应商"), choice.get_by_role("radio", name="合成一个供应商")
    expect(d.locator('[data-import-detail="cc_kimi"]')).to_contain_text("这几个 alias 的各档模型和默认思考档位不一样。")
    expect(each).to_have_attribute("aria-checked", "true")
    variants = d.get_by_role("list", name="cc_kimi 会变成的供应商")
    expect(variants.locator("li")).to_have_count(2)
    # each alias is listed in Persona already, so the hub saves its provider as <alias>-2, and the preview says so
    expect(variants.locator("li").nth(0)).to_contain_text("cc_kimi-2")
    expect(variants.locator("li").nth(1)).to_contain_text("cc_kimi_fast-2")
    expect(relay.locator(".font-mono").first).to_have_text("cc_relay-2")
    expect(d.get_by_role("button", name="导入 5 个")).to_be_visible()
    one.click()
    expect(one).to_have_attribute("aria-checked", "true")
    expect(variants).to_have_count(0)
    expect(d.get_by_role("button", name="导入 4 个")).to_be_visible()
    one.focus()
    page.keyboard.press("ArrowUp")
    expect(each).to_have_attribute("aria-checked", "true")
    expect(each).to_be_focused()
    expect(d.get_by_role("button", name="导入 5 个")).to_be_visible()
    ok("a group whose aliases differ starts on 每个 alias 一个供应商 (counted per alias); 合成一个供应商 and back by click and arrow key")

    hide = d.get_by_role("checkbox", name="在 Persona 里隐藏已导入的 alias")
    expect(hide).to_have_attribute("aria-checked", "true")
    stays = d.locator("[data-import-stays]")
    expect(stays).to_contain_text("会隐藏 cc_full、cc_glm和cc_kimi。")
    expect(stays).to_contain_text("cc_full_haiku 保留：模型档位「性能」的 Agent 预设「strong」在用它。")
    expect(stays).to_contain_text("cc_kimi_fast 保留：主对话在用它。")
    expect(stays).to_contain_text("cc_relay 保留：新的供应商无法在服务器上替代它。")
    r.shot("import-preview")
    ok("hiding the imported aliases is on, names what it hides and why the others stay")

    d.get_by_label("电脑", exact=True).select_option("studio")
    expect(d.get_by_role("alert")).to_have_text("这台电脑的 Persona 需要更新")
    d.get_by_label("电脑", exact=True).select_option("nas")
    expect(d.get_by_role("alert")).to_have_text("这台电脑不在线")
    ok("409 node_outdated and 503 offline are told apart")

    d.get_by_label("电脑", exact=True).select_option("macbook")
    expect(rows).to_have_count(4)
    d.locator('[data-import="cc_glm"]').click()
    expect(d.locator('[data-import="cc_glm"]')).to_have_attribute("aria-checked", "false")
    expect(stays).not_to_contain_text("cc_glm")
    d.get_by_role("button", name="导入 4 个").click()
    expect(page.get_by_text("新建 3 个，合并 1 个；隐藏 alias 2 个，保留 3 个")).to_be_visible()
    expect(d).to_be_hidden()
    sent = r.log()["import"]
    assert sorted(sent["providers"]) == ["cc_full", "cc_kimi", "cc_relay"], sent
    assert sent["split"] == ["cc_kimi"] and sent["hide_aliases"] is True, sent
    expect(page.get_by_text("cc_kimi_fast-2", exact=True).first).to_be_visible()
    ok("importing 4 posts split and hide_aliases, says 新建 3 个，合并 1 个；隐藏 alias 2 个，保留 3 个 and lists the new providers")


def phone(browser, base, hub, token, shots):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, is_mobile=True)
    page = ctx.new_page()
    page.set_default_timeout(10000)
    r = Run(page, base, hub, token, shots)
    r.open_models()
    page.get_by_role("button", name="新增").click()
    d = r.dialog("新增供应商")
    r.shot("phone-pick")
    d.get_by_role("button", name="下一步").click()
    d.get_by_label("名称", exact=True).fill("phone_gw")
    d.get_by_label("地址", exact=True).fill("https://relay.example.com")
    d.get_by_label("API Key", exact=True).fill("sk-test-phone")
    d.get_by_role("button", name="获取模型列表").click()
    expect(d.get_by_text("找到 3 个模型")).to_be_visible()
    r.shot("phone-connect")
    d.get_by_role("button", name="下一步").click()
    d.get_by_role("button", name="高级").click()
    expect(d.get_by_label("Opus 档模型")).to_be_visible()
    r.shot("phone-models")
    d.get_by_role("button", name="取消").click()
    # the header's import button is an icon at this width, named for screen readers, on one line with search and add
    imp = page.get_by_role("button", name="导入", exact=True)
    add = page.get_by_role("button", name="新增", exact=True)
    ib, ab = imp.bounding_box(), add.bounding_box()
    assert ib["width"] < 48 and ib["height"] <= 40 and abs(ib["y"] - ab["y"]) < 2, (ib, ab)
    imp.click()
    d = r.dialog("从电脑上的 alias 导入")
    d.get_by_label("电脑", exact=True).select_option("macbook")
    expect(d.get_by_role("list", name="可以导入的供应商").get_by_role("checkbox")).to_have_count(4)
    r.shot("phone-import")
    ctx.close()
    ok("390 wide: the wizard and the import sheet fit")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", default="http://127.0.0.1:19011")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    base, hub = a.base.rstrip("/"), a.hub.rstrip("/")
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            ctx = browser.new_context(viewport={"width": 1280, "height": 860}, device_scale_factor=2)
            page = ctx.new_page()
            page.set_default_timeout(10000)
            r = Run(page, base, hub, a.token, a.shots)
            r.open_models()
            wizard(r)
            edit_full(r)
            zshrc_import(r)
            ctx.close()
            phone(browser, base, hub, a.token, a.shots)
        except Exception as e:
            print("FAIL", e, flush=True)
            sys.exit(1)
        finally:
            browser.close()
    print("PASS web providers e2e")


if __name__ == "__main__":
    main()
