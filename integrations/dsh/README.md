# Persona：DSH 接入

私有 npm 包名为 `persona-dsh-runtime`（#753）；自动更新包 ID 与已安装 loader 身份 `persona-dsh-runtime` 保留。`package.json.name` 改名不改变更新目录、加载标识或 `persona-runtime.v1` 子协议。本文使用迁移版本的 `personad` 和 `~/.persona`；旧 `personad`、`~/.persona` 与 `PERSONA_*` 继续兼容，已迁移的 `PERSONA_*` 优先，见 [配置与兼容说明](../../docs/operations/config.md#命名与兼容) 和 [完整保留清单](../../docs/operations/rename.md)。

DSH（DeepSeek Harness）的 Web 插件：把这台电脑上 `dsh web` 的会话接进 Persona（连 personad node 的 `~/.persona/runtime.sock`）。
它是按 `docs/integrations/runtimes.md` 规范接入的第一个 runtime，建在 [`integrations/runtime-sdk`](../runtime-sdk/README.md) 上。
说明、安装、配置和限制见 [`docs/integrations/dsh.md`](../../docs/integrations/dsh.md)。

| 文件 | 内容 |
| --- | --- |
| `index.js` | Cordis 插件入口，只是加载器（SDK 的 `createLoader`）：DSH 留着它，插件代码从 `~/.persona/runtime-packages/` 里带哈希的版本目录加载，node 带来新版本时自己换上（`docs/integrations/dsh.md`「Automatic updates」）。尽量不改它：改了要在 DSH 里手动重新加载一次 |
| `lib/plugin.js` | 实现的入口（加载器的 `api` / `start`）：取 `sessionController` 等服务，校验配置，启动 runtime |
| `lib/dsh.js` | DSH 的 driver：列会话、翻页、发送 / 中断 / 新建 / 改名，事件和两条应答链接到 SDK |
| `lib/map.js` | DSH 会话事件 → Persona item 的纯映射（基于 DSH 的平台可以复用） |
| `test/` | `npm test`（`node --test`，不需要 DSH） |
| `e2e/run.mjs` | 端到端：真的 `dsh web` + 真的 `personad node`，见 `docs/integrations/dsh.md`「Verification」 |
| `e2e/update.mjs` | 端到端：node 带另一个版本的插件时，插件自己换上、照常工作，DSH 重启后跑换上的那个 |

没有依赖、不用构建：Node 22.19+ 直接跑。SDK 按相对路径引用，从仓库的检出目录装。
