# {{name}}

Persona 扩展（`{{id}}`），从 Go 模板生成。写法见 Persona 的 `docs/extensions/extensions.md`「Writing extensions: SDK, templates and command line」。

| 命令 | 做什么 |
| --- | --- |
| `go test ./...` | 单元测试和一致性检查（`exttest`） |
| `personad extension dev` | 挂到这台 hub 上试：改了代码自动重建、重启，日志打到终端 |
| `personad extension test` | 用 hub 的那套检查跑构建好的程序 |
| `personad extension pack` | 打四个平台的包，打印 sha256 和可以贴进市场索引的条目 |

`sdk/go/` 是 SDK 的一份拷贝，不要改；要更新用 Persona 仓库的 `scripts/gen/sync-sdk.sh`。
