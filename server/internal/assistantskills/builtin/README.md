# 内置 skill

每个子目录是一个随 personad 发布的 skill。只放 Persona 能再分发的（许可证允许、我们自己写的），并登记在 `embed.go` 的
`Redistributable` 里（`TestBuiltinsAreRedistributable` 守着）。从别处拷来、不能再分发的（比如飞书网关的 skill）放用户自己的空间
`~/.persona/skills/`（`docs/operations/hub-update.md`「Skills move out of the binary」），仓库里的副本在 `skills/`。
