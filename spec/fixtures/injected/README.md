# Persona 注入的消息夹具

hub 以用户输入的身份发给模型的消息（任务通知、定时任务、叫醒、node 重启）怎么认、认出什么，规则见 [docs/protocol/protocol.md「Messages injected by Persona」](../../../docs/protocol/protocol.md)。

| 文件 | 覆盖内容 |
| --- | --- |
| `cases.json` | 每种模板的各个状态、标题里带括号、前面带快照块，以及用户自己打的相似文字（不认） |

- 结构：`{"version": 1, "cases": [...]}`；`input.text` 是 transcript 里那一行用户文字，`expected` 是 hub 下发的 `injected`（`null` 是不认，照旧是用户气泡）；`todo_id` 只有 hub 能补，夹具里没有。
- 用例由 Go 测试按模板渲染生成，模板改了要重新生成：`cd server && UPDATE_GOLDEN=1 go test ./internal/prompts -run Injected`。
- 各端（网页 `lib/injected.ts`、Android `core/domain/InjectedMessages.kt`、Apple `Shared/Injected.swift` 的 `InjectedTests`）旧 hub 没有 `injected` 字段时的兜底识别都跑这一份；比较时字段缺省等于空。
