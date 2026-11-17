// Fixture-only data and in-memory sources for the configuration showcase (web/e2e/configuration-showcase.html, docs/web/web.md
// "Configuration forms"): several independent mock module documents, drawn by the real ConfigurationForm so a person can
// open, operate and compare them with no hub, server or real setting. Every source keeps its values in this page's memory
// and reads or writes no storage; no file here is part of a build.
//
// The mock documents' text is written in the three client languages here because it is fixture data, not interface
// strings: the app's own shell (buttons, errors) still comes from the real catalogs, and a configuration source localizes
// its document for the caller, as schema/configuration requires. The values exercise every control kind, every select
// presentation, bounds at min/max/zero, empty and full multiselects, read-only fields, notices and both schema-version
// outcomes. schema/configuration/all-types.json is reused unchanged as one module.
import { allTypes } from "../src/lib/configuration/fixtures/shared.ts";
import { checkUpdate, CONFIGURATION_VERSION, ConfigurationError } from "../src/lib/configuration/contract.ts";

export const SCENARIO_IDS = ["default", "slow", "load-fail", "reject", "conflict", "unconfirmed", "empty", "unsupported"];

/** URL aliases: the preview can link the full set as ?scenario=overview, the name people would expect. */
const SCENARIO_ALIASES = { overview: "default" };

const clone = (v) => JSON.parse(JSON.stringify(v));
/** The fixture's own three languages; the page passes the app's current one. */
const T = (lang, en, zh, ja) => (lang === "en" ? en : lang === "ja" ? ja : zh);

/** The page's chrome, in the reader's language. Not catalog keys: this page is never built and adds no interface text. */
export function shell(lang) {
  return {
    title: T(lang, "Configuration showcase", "配置展示", "設定ショーケース"),
    lead: T(
      lang,
      "Module-injected configuration documents, drawn by the real configuration form. These are mock modules over in-memory sources: there is no hub, no server and no real setting behind any of them.",
      "由模块注入的配置文档，用真实的通用配置表单绘制。这里的模块都是模拟的，数据只保存在页面内存中：没有 hub、没有服务器，也没有任何真实设置。",
      "モジュールが注入する設定ドキュメントを、実際の汎用設定フォームで表示します。ここにあるのはメモリ上のソースを使うモックモジュールで、hub もサーバーも実際の設定もありません。",
    ),
    mockBanner: T(
      lang,
      "Mock data. Changes are kept in this page's memory until you refresh; the forms read and write only these mock sources.",
      "模拟数据。更改只保存在本页的内存中，刷新页面即会丢失；各表单只读写这些模拟数据源。",
      "モックデータです。変更はこのページのメモリに保持され、再読み込みすると失われます。フォームが読み書きするのはこれらのモックソースだけです。",
    ),
    instructions: T(
      lang,
      "Pick a scenario, then edit any form. Save writes only to that module's memory; Use default and Reload work the same way. The paragraph beside the form says what the module does.",
      "先选择一个场景，然后编辑任意表单。保存只会写入该模块的内存；使用默认值和重新载入也一样。表单旁的说明会介绍该模块。",
      "シナリオを選んでから、どのフォームでも編集できます。保存はそのモジュールのメモリにだけ書き込み、既定値に戻す・再読み込みも同じように動きます。フォームの横の説明がそのモジュールの内容です。",
    ),
    scenarioLabel: T(lang, "Scenario", "场景", "シナリオ"),
    scenarioNote: T(lang, "What to try", "可以试试", "試すこと"),
    mock: T(lang, "Mock", "模拟", "モック"),
    otherPage: T(lang, "Single-document fixture page", "单文档示例页", "単一ドキュメントの例ページ"),
    fixtureProblem: T(lang, "Fixture problem: a mock document or value is invalid", "示例数据有问题：某个模拟文档或取值无效", "フィクスチャの問題: モックドキュメントまたは値が無効です"),
  };
}

/** The selectable scenarios, with what each one demonstrates. */
export function scenarios(lang) {
  return [
    {
      id: "default",
      label: T(lang, "All modules", "全部模块", "すべてのモジュール"),
      note: T(lang, "Every mock module on one page; each form is independent. Save, Use default and Reload all work.", "一页展示所有模拟模块，各表单互不影响。保存、使用默认值和重新载入都可以直接试。", "すべてのモックモジュールを1ページに。各フォームは独立しています。保存・既定値に戻す・再読み込みを試せます。"),
    },
    {
      id: "slow",
      label: T(lang, "Slow source", "缓慢的数据源", "遅いソース"),
      note: T(lang, "Reads and saves take 1.6 seconds, so Loading… and Saving… stay visible long enough to see.", "读取和保存各需要 1.6 秒，可以清楚看到「正在载入」和「正在保存」状态。", "読み込みと保存に各1.6秒かかるため、「読み込み中」「保存中」がはっきり見えます。"),
    },
    {
      id: "load-fail",
      label: T(lang, "Failed first load", "首次载入失败", "初回読み込みの失敗"),
      note: T(lang, "Only the shared example is shown: its first read fails, and Try again loads it.", "只显示共享示例：它第一次读取会失败，点「重试」即可载入。", "共有例だけを表示しています。最初の読み込みが失敗します。「再試行」で読み込めます。"),
    },
    {
      id: "reject",
      label: T(lang, "Field rejected", "字段被拒绝", "項目の拒否"),
      note: T(lang, "Only Daily digest is shown. Change the topic and Save: the first save is rejected with the module's own message; saving again succeeds.", "只显示「每日摘要」。修改主题并保存：第一次保存会被拒绝，并显示模块自己的提示；再次保存即可成功。", "「デイリーダイジェスト」だけを表示しています。件名を変えて保存すると、最初の保存はモジュール自身のメッセージで拒否されます。もう一度保存すると成功します。"),
    },
    {
      id: "conflict",
      label: T(lang, "Revision conflict", "版本冲突", "リビジョンの競合"),
      note: T(lang, "Only Daily digest is shown. Change the topic and Save: another writer got there first, so the save reports a conflict. Reload refreshes the baseline while your text stays; review it, then save again.", "只显示「每日摘要」。修改主题并保存：另一个写入方抢先保存，因此报告冲突。重新载入会刷新基线，你输入的内容仍会保留；确认后再保存一次。", "「デイリーダイジェスト」だけを表示しています。件名を変えて保存すると、別の書き手が先に保存したため競合します。再読み込みすると基準が更新され、入力内容は残ります。確認してからもう一度保存してください。"),
    },
    {
      id: "unconfirmed",
      label: T(lang, "Unconfirmed save", "保存结果未知", "保存の未確認"),
      note: T(lang, "Only Daily digest is shown. Change the topic and Save: the request never gets an answer, so the form says the save is unconfirmed and Reload is required before trying again.", "只显示「每日摘要」。修改主题并保存：请求得不到回应，表单会提示保存结果未知，必须先重新载入才能再试。", "「デイリーダイジェスト」だけを表示しています。件名を変えて保存すると、応答が返らず「保存を確認できません」と表示されます。再試行の前に再読み込みが必要です。"),
    },
    {
      id: "empty",
      label: T(lang, "Empty form", "空表单", "空のフォーム"),
      note: T(lang, "A valid document with no groups: the form shows its title and description, and no fields.", "一个没有分组但有效的文档：表单只显示标题和说明，没有字段。", "グループのない有効なドキュメントです。タイトルと説明だけが表示され、項目はありません。"),
    },
    {
      id: "unsupported",
      label: T(lang, "Newer builds", "更新版本", "新しいビルド"),
      note: T(lang, "A field kind this build doesn't know stays visible read-only, and a newer schema version is shown but never written.", "此版本不认识的字段类型会以只读方式显示；更新的协议版本会展示，但永远不会被写入。", "このビルドが知らない項目種別は読み取り専用で表示され、新しいスキーマバージョンは表示のみで書き込みません。"),
    },
  ];
}

const MODULE_NOTE = (lang, key) => {
  const notes = {
    terminal: T(lang, "Browser-local style: the same kind of source the real Labs page uses, with scope, activation and rollback details.", "类似浏览器本地的数据源，与真实 Labs 页面的方式相同，带适用范围、生效时机和回退说明。", "ブラウザ内ソース風。実際の Labs ページと同じ種類のソースで、適用範囲・反映のタイミング・元に戻す方法の説明付きです。"),
    digest: T(lang, "Server-style source: values look written by another service, and notices explain what the module controls.", "类似服务器的数据源：取值像是另一个服务写入的，并用提示说明模块控制的内容。", "サーバー風のソース。値は別のサービスが書いたように見え、通知がモジュールの管理内容を説明します。"),
    editor: T(lang, "Local editor module: all three multiselect counts (none, full, exactly one) and a fractional number.", "本地编辑器模块：三种多选数量（不限、已满、恰好一个），以及一个小数。", "ローカルなエディターモジュール。3種類の複数選択（なし・上限・ちょうど1つ）と小数の数値があります。"),
    shared: T(lang, "The shared contract example (schema/configuration/all-types.json), unchanged and in English: every kind and every select presentation.", "共享契约示例（schema/configuration/all-types.json），保持原样、英文内容：包含所有控件类型和所有下拉呈现方式。", "共有契約の例（schema/configuration/all-types.json）をそのまま英語で表示します。すべての種類と選択の表示形式を含みます。"),
    empty: T(lang, "A valid document with no groups.", "一个没有分组但有效的文档。", "グループのない有効なドキュメントです。"),
    newerKind: T(lang, "One field has a kind from a newer build: shown with its words, never changed, and its value preserved.", "有一个字段来自更新版本：会显示它的文字说明，不能修改，取值会保留。", "1つの項目が新しいビルドの種別です。説明は表示されますが変更できず、値は保持されます。"),
    newerVersion: T(lang, "A document from a newer schema version: shown read-only, never written.", "来自更新协议版本的文档：只读展示，永远不会被写入。", "新しいスキーマバージョンのドキュメントです。読み取り専用で表示され、書き込みません。"),
  };
  return notes[key];
};

function terminalDoc(lang) {
  return {
    schema_version: CONFIGURATION_VERSION,
    id: "showcase-terminal",
    revision: "terminal-1",
    title: T(lang, "Terminal renderer", "终端渲染方式", "ターミナルの描画方式"),
    description: T(lang, "How single-pane terminals are drawn in this browser.", "此浏览器中单窗格终端的绘制方式。", "このブラウザでのシングルペイン表示のターミナルの描画方法です。"),
    groups: [
      {
        id: "renderer",
        title: T(lang, "Renderer", "渲染方式", "描画方式"),
        fields: [
          {
            id: "renderer",
            kind: "select",
            title: T(lang, "Terminal renderer", "终端渲染方式", "ターミナルの描画方式"),
            description: T(lang, "WebGL can be smoother with lots of output, but doesn't work in every browser.", "输出很多时 WebGL 可能更流畅，但并非所有浏览器都支持。", "出力が多いときに WebGL は滑らかになりますが、すべてのブラウザで使えるわけではありません。"),
            default: "standard",
            select: {
              presentation: "segmented",
              options: [
                { value: "standard", title: T(lang, "Standard", "标准", "標準") },
                { value: "webgl", title: "WebGL" },
              ],
            },
            details: [
              { title: T(lang, "Applies to", "适用范围", "対象"), description: T(lang, "This browser only.", "仅此浏览器。", "このブラウザのみ。") },
              { title: T(lang, "Takes effect", "生效时机", "反映されるタイミング"), description: T(lang, "Terminals you open after changing it.", "更改后新打开的终端。", "変更後に開くターミナル。") },
              { title: T(lang, "Going back", "如何回退", "元に戻すには"), description: T(lang, "Choose Standard; a terminal that loses WebGL switches back by itself.", "选择「标准」；WebGL 出错的终端会自动改回标准。", "「標準」を選びます。WebGL が使えなくなったターミナルは自動で標準に戻ります。") },
            ],
          },
        ],
      },
    ],
    values: { renderer: "webgl" },
  };
}

function digestDoc(lang) {
  return {
    schema_version: CONFIGURATION_VERSION,
    id: "showcase-digest",
    revision: "digest-1",
    title: T(lang, "Daily digest", "每日摘要", "デイリーダイジェスト"),
    description: T(lang, "A mock notifications module with a server-style source: one summary each day.", "模拟的通知模块，使用类似服务器的方式读写：每天发送一次摘要。", "通知のモックモジュール。サーバー風のソースで、毎日1回まとめを送ります。"),
    groups: [
      {
        id: "delivery",
        title: T(lang, "Delivery", "发送", "配信"),
        description: T(lang, "When and how the digest arrives.", "摘要何时、以什么方式送达。", "まとめをいつ、どう届けるか。"),
        fields: [
          {
            id: "enabled",
            kind: "toggle",
            title: T(lang, "Send a daily digest", "发送每日摘要", "デイリーダイジェストを送る"),
            description: T(lang, "One summary each day, at the hour below.", "每天按下面的时间发送一次摘要。", "毎日、下の時刻に1回まとめを送ります。"),
            default: false,
          },
          {
            id: "when",
            kind: "select",
            title: T(lang, "Time of day", "时段", "時間帯"),
            default: "morning",
            select: {
              presentation: "dropdown",
              options: [
                { value: "morning", title: T(lang, "Morning", "上午", "午前") },
                { value: "evening", title: T(lang, "Evening", "晚上", "夜") },
              ],
            },
          },
          {
            id: "hour",
            kind: "number",
            title: T(lang, "Hour", "小时", "時"),
            description: T(lang, "0 to 23, in the hub's time zone.", "0 到 23，按 hub 所在时区。", "0〜23。hub のタイムゾーン基準です。"),
            default: 8,
            number: { integer: true, minimum: 0, maximum: 23, step: 1 },
          },
          {
            id: "topic",
            kind: "text",
            title: T(lang, "Topic", "主题", "件名"),
            description: T(lang, "One line that starts the message; required, up to 40 characters.", "摘要的开头一行，必填，最多 40 个字符。", "本文の冒頭に使う1行。必須で40文字まで。"),
            default: "Daily summary",
            text: { min_length: 1, max_length: 40, placeholder: T(lang, "For example, Daily summary", "例如：每日摘要", "例：今日のまとめ") },
          },
          {
            id: "note",
            kind: "text",
            title: T(lang, "Extra note", "补充说明", "補足"),
            description: T(
              lang,
              "Optional text added under the summary. It can run over several lines, is sent as plain text, and is limited to 300 characters so the digest stays readable on a phone.",
              "可选，附在摘要下方的说明。可以分成多行，以纯文本发送；最多 300 个字符，以保证摘要在手机上仍然易读。",
              "まとめの下に添える任意の文章です。複数行にでき、プレーンテキストとして送られます。スマートフォンでも読みやすいよう300文字までです。",
            ),
            default: "",
            text: { multiline: true, max_length: 300, placeholder: T(lang, "Anything worth a line", "值得写一行的话", "ひとこと添えるなら") },
          },
          {
            id: "address",
            kind: "text",
            title: T(lang, "Delivery address", "送达地址", "送信先"),
            description: T(lang, "Where the digest is sent.", "摘要发送到哪里。", "まとめの送信先です。"),
            default: "",
            read_only: true,
            disabled_reason: T(lang, "Set by the account that owns this module.", "由拥有此模块的账号设置。", "このモジュールを所有するアカウントが設定します。"),
            details: [
              { title: T(lang, "Applies to", "适用范围", "対象"), description: T(lang, "This account, on every device.", "此账号的所有设备。", "このアカウントのすべてのデバイス。") },
              { title: T(lang, "Takes effect", "生效时机", "反映されるタイミング"), description: T(lang, "The next digest after the account changes it.", "账号修改后的下一份摘要。", "アカウントで変更した後の次のまとめから。") },
            ],
            text: {},
          },
        ],
      },
    ],
    values: {
      enabled: true,
      when: "evening",
      hour: 0,
      topic: T(lang, "Daily summary 🌤", "每日摘要 🌤", "今日のまとめ 🌤"),
      note: "",
      address: "me@example.com",
    },
    notices: {
      hour: { kind: "warning", message: T(lang, "The hour follows the hub's time zone, not this browser's.", "小时按 hub 所在时区，而不是此浏览器的时区。", "時刻はブラウザではなく hub のタイムゾーン基準です。") },
      note: { kind: "info", message: T(lang, "Optional; sent as plain text.", "可选，以纯文本发送。", "任意。プレーンテキストで送信されます。") },
    },
  };
}

function editorDoc(lang) {
  return {
    schema_version: CONFIGURATION_VERSION,
    id: "showcase-editor",
    revision: "editor-1",
    title: T(lang, "Editor formatting", "编辑器格式", "エディターの書式"),
    description: T(lang, "A local editor module whose settings belong to this editor.", "本地编辑器模块，设置只属于这个编辑器。", "このエディターに属するローカルなエディターモジュールです。"),
    groups: [
      {
        id: "editor",
        title: T(lang, "Editor", "编辑器", "エディター"),
        fields: [
          {
            id: "theme",
            kind: "select",
            title: T(lang, "Color theme", "配色主题", "カラーテーマ"),
            description: T(lang, "This editor's own colors, independent of the app's appearance.", "此编辑器自己的配色，与应用外观相互独立。", "アプリの外観とは別に、このエディター自身の配色です。"),
            default: "system",
            select: {
              presentation: "list",
              options: [
                { value: "system", title: T(lang, "Match the app", "跟随应用", "アプリに合わせる"), description: T(lang, "Use the appearance already chosen for Persona.", "沿用 Persona 已选择的外观。", "Persona で選択済みの外観を使います。") },
                { value: "light", title: T(lang, "Light", "浅色", "ライト"), description: T(lang, "Paper-like background with dark text, whatever the app uses.", "无论应用使用什么外观，都使用浅色背景和深色文字。", "アプリの設定にかかわらず、紙のような背景に濃い文字を使います。") },
                { value: "dark", title: T(lang, "Dark", "深色", "ダーク"), description: T(lang, "Low-light background with light text; useful beside a terminal or at night.", "低亮度背景配浅色文字，适合在终端旁或夜间使用。", "暗い背景に明るい文字。ターミナルの横や夜間に便利です。") },
              ],
            },
          },
          {
            id: "sidebars",
            kind: "multiselect",
            title: T(lang, "Sidebars", "侧边栏", "サイドバー"),
            description: T(lang, "Any combination may be open; none is allowed.", "可以同时打开多个，也可以一个都不开。", "組み合わせは自由で、何も開かないのも可です。"),
            default: ["search"],
            multiselect: {
              min_selected: 0,
              max_selected: 3,
              options: [
                { value: "search", title: T(lang, "Search", "搜索", "検索") },
                { value: "outline", title: T(lang, "Outline", "大纲", "アウトライン") },
                { value: "files", title: T(lang, "Files", "文件", "ファイル") },
              ],
            },
          },
          {
            id: "pinned",
            kind: "multiselect",
            title: T(lang, "Pinned tools", "固定工具", "固定ツール"),
            description: T(lang, "One or two tools stay at the top; this example has both.", "最多固定两个工具；此示例已固定两个。", "1〜2個のツールを上部に固定できます。この例では2つ固定されています。"),
            default: ["search"],
            multiselect: {
              min_selected: 1,
              max_selected: 2,
              options: [
                { value: "search", title: T(lang, "Search", "搜索", "検索") },
                { value: "terminal", title: T(lang, "Terminal", "终端", "ターミナル") },
                { value: "files", title: T(lang, "Files", "文件", "ファイル") },
              ],
            },
          },
          {
            id: "snippet_language",
            kind: "multiselect",
            title: T(lang, "Snippet language", "代码片段语言", "スニペットの言語"),
            description: T(lang, "Exactly one language is attached to a snippet; choose another to replace it.", "每个代码片段只附带一种语言；选择另一种即可替换。", "スニペットには必ず1つの言語が付きます。別のものを選ぶと置き換わります。"),
            default: ["auto"],
            multiselect: {
              min_selected: 1,
              max_selected: 1,
              options: [
                { value: "auto", title: T(lang, "Detect automatically", "自动识别", "自動判別") },
                { value: "zh-Hans", title: T(lang, "Chinese", "中文", "中国語") },
                { value: "en", title: T(lang, "English", "英文", "英語") },
              ],
            },
          },
          {
            id: "line_height",
            kind: "number",
            title: T(lang, "Line height", "行高", "行の高さ"),
            description: T(lang, "A fractional value from 0.5 to 2.75, in steps of 0.25.", "0.5 到 2.75 之间的小数，步进 0.25。", "0.5〜2.75 の小数。刻みは 0.25 です。"),
            default: 1.25,
            number: { minimum: 0.5, maximum: 2.75, step: 0.25 },
          },
          {
            id: "prefix",
            kind: "text",
            title: T(lang, "Snippet prefix", "代码片段前缀", "スニペットの接頭辞"),
            description: T(lang, "Optional text put before each snippet; up to 12 characters.", "可选，放在每个代码片段前；最多 12 个字符。", "任意。各スニペットの前に置く文字列で、12文字までです。"),
            default: "",
            text: { max_length: 12 },
          },
        ],
      },
    ],
    values: {
      theme: "dark",
      sidebars: [],
      pinned: ["search", "terminal"],
      snippet_language: ["auto"],
      line_height: 2.75,
      prefix: "→ ",
    },
  };
}

function emptyDoc(lang) {
  return {
    schema_version: CONFIGURATION_VERSION,
    id: "showcase-empty",
    revision: "empty-1",
    title: T(lang, "An empty form", "空表单", "空のフォーム"),
    description: T(lang, "A valid document whose module has no settings yet.", "一个有效文档，但模块目前没有任何设置。", "有効なドキュメントですが、モジュールにまだ設定がありません。"),
    groups: [],
    values: {},
  };
}

function newerKindDoc(lang) {
  const doc = clone(allTypes);
  doc.id = "showcase-newer-kind";
  doc.revision = "newer-kind-1";
  doc.title = T(lang, "Shared example with a newer field", "共享示例（含新版本字段）", "新しい項目を含む共有例");
  doc.groups[0].fields.push({
    id: "palette",
    kind: "colour",
    title: T(lang, "Palette from a newer build", "新版本的颜色方案", "新しいビルドの配色"),
    description: T(lang, "This build can't draw or change it, and leaves its value alone.", "此版本无法显示或修改它，但会保留它的值。", "このビルドでは表示も変更もできませんが、値はそのまま残します。"),
    default: "#1f6feb",
  });
  doc.values.palette = "#1f6feb";
  return doc;
}

function newerVersionDoc(lang) {
  return {
    schema_version: CONFIGURATION_VERSION + 1,
    id: "showcase-newer-version",
    revision: "future-1",
    title: T(lang, "From a newer Persona", "来自更新版本的 Persona", "新しい Persona のもの"),
    description: T(lang, "A document of a later schema version: shown read-only.", "更新协议版本的文档：只读展示。", "新しいスキーマバージョンのドキュメントです。読み取り専用で表示します。"),
    groups: [
      {
        id: "future",
        title: T(lang, "Future settings", "未来的设置", "将来の設定"),
        fields: [
          {
            id: "enabled",
            kind: "toggle",
            title: T(lang, "An upcoming feature", "即将推出的功能", "近日公開の機能"),
            default: true,
          },
          {
            id: "mode",
            kind: "select",
            title: T(lang, "Mode", "模式", "モード"),
            default: "balanced",
            select: {
              presentation: "segmented",
              options: [
                { value: "fast", title: T(lang, "Fast", "快速", "高速") },
                { value: "balanced", title: T(lang, "Balanced", "均衡", "バランス") },
              ],
            },
          },
        ],
      },
    ],
    values: { enabled: true, mode: "balanced" },
  };
}

/** One source over one document, kept in memory, with the scenario's latency and first-answer behavior. */
function memorySource({ key, doc, lang, latency, readFailures = 0, behavior = null }) {
  let current = clone(doc);
  let failedReads = 0;
  let answered = false;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  return {
    key,
    async read() {
      await wait(latency.read);
      if (failedReads < readFailures) {
        failedReads += 1;
        throw new ConfigurationError(
          "configuration_unavailable",
          T(lang, "The preview service didn't answer. Try again.", "预览服务没有响应，请重试。", "プレビューサービスが応答しませんでした。もう一度お試しください。"),
        );
      }
      return clone(current);
    },
    async update(update) {
      await wait(latency.save);
      // Scenario behaviors answer once, then behave like an ordinary writer.
      if (behavior === "reject" && !answered && update.changes && Object.hasOwn(update.changes, "topic")) {
        answered = true;
        throw new ConfigurationError("configuration_invalid", "", [
          { id: "topic", code: "taken", message: T(lang, "That topic is already used by another digest. Pick another.", "这个主题已被另一份摘要使用，请换一个。", "その件名は別のまとめで使われています。別の件名にしてください。") },
        ]);
      }
      if (behavior === "conflict" && !answered) {
        answered = true;
        // Another writer committed first: the next read sees a newer revision and their value.
        current = clone(current);
        current.revision = `${current.revision}+other`;
        current.values.topic = T(lang, "Weekly digest", "每周摘要", "週間まとめ");
        throw new ConfigurationError("configuration_conflict");
      }
      if (behavior === "unconfirmed" && !answered) {
        answered = true;
        throw new TypeError("Failed to fetch");
      }
      const refused = checkUpdate(current, update);
      if (refused) throw refused;
      const next = clone(current);
      const fields = next.groups.flatMap((group) => group.fields);
      Object.assign(next.values, update.changes ?? {});
      for (const id of update.reset ?? []) {
        const field = fields.find((f) => f.id === id);
        next.values[id] = field ? clone(field.default) : next.values[id];
      }
      next.revision = `${current.revision}+`;
      current = next;
      return clone(current);
    },
  };
}

const moduleOf = (lang, key, doc, { note, readFailures = 0, behavior = null, sourceLabel, latency }) => ({
  key,
  document: doc,
  note,
  sourceLabel: sourceLabel ?? T(lang, "In-memory source", "内存数据源", "メモリ上のソース"),
  source: memorySource({ key, doc, lang, latency, readFailures, behavior }),
});

/**
 * `scenario` and `lang` choose what to draw; `options.latency` overrides the delays for tests. Each call builds fresh
 * documents and fresh in-memory sources, so pages and tests never share state.
 */
export function buildShowcase(scenario = "default", lang = "zh-Hans", options = {}) {
  const requested = SCENARIO_ALIASES[scenario] ?? scenario;
  const chosen = SCENARIO_IDS.includes(requested) ? requested : "default";
  const latency = options.latency ?? (chosen === "slow" ? { read: 1600, save: 1600 } : { read: 140, save: 320 });
  const mod = (key, doc, extra = {}) => moduleOf(lang, key, doc, { ...extra, latency });
  const note = (key) => MODULE_NOTE(lang, key);

  if (chosen === "empty") {
    return { scenario: chosen, modules: [mod("showcase-empty", emptyDoc(lang), { note: note("empty") })] };
  }
  if (chosen === "unsupported") {
    return {
      scenario: chosen,
      modules: [
        mod("showcase-newer-kind", newerKindDoc(lang), { note: note("newerKind") }),
        mod("showcase-newer-version", newerVersionDoc(lang), { note: note("newerVersion") }),
      ],
    };
  }
  const behavior = chosen === "reject" ? "reject" : chosen === "conflict" ? "conflict" : chosen === "unconfirmed" ? "unconfirmed" : null;
  const sharedModule = () =>
    mod("showcase-shared", clone(allTypes), {
      note: note("shared"),
      sourceLabel: T(lang, "Shared contract example", "共享契约示例", "共有契約の例"),
      readFailures: chosen === "load-fail" ? 1 : 0,
    });
  const digestModule = () => mod("showcase-digest", digestDoc(lang), { note: note("digest"), behavior });
  // A failure scenario shows only the module it happens in, so its error controls are visible without scrolling through
  // every other form. The full set keeps all five.
  if (chosen === "load-fail") return { scenario: chosen, modules: [sharedModule()] };
  if (behavior) return { scenario: chosen, modules: [digestModule()] };
  return {
    scenario: chosen,
    modules: [
      mod("showcase-terminal", terminalDoc(lang), { note: note("terminal") }),
      digestModule(),
      mod("showcase-editor", editorDoc(lang), { note: note("editor") }),
      sharedModule(),
      mod("showcase-empty", emptyDoc(lang), { note: note("empty") }),
    ],
  };
}
