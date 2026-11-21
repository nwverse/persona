{{if eq .Event "merged"}}✅ 已关单任务「{{.Title}}」的交付 PR 已合并{{else if eq .Event "closed"}}⌧ 已关单任务「{{.Title}}」的交付 PR 已关闭（未合并）{{else if eq .Event "conflict"}}⚠️ 已关单任务「{{.Title}}」的交付 PR 出现合并冲突{{else}}🔁 已关单任务「{{.Title}}」的交付 PR 在当前 head 收到 changes-requested 评审{{end}}：{{.PRURL}}
任务保持已关单，不会被重开；这只是关单后的 PR 跟进信号。
（这是 Persona 的通知，不是用户发的消息。{{if .Realtime}}手机可能收到一次提醒；{{end}}不用回应，需要处理时再派任务。）
