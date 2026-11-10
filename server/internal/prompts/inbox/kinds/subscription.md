{{if eq .Kind "sub_mail"}}订阅「{{.Label}}」定时拉来的邮件
{{- else if eq .Kind "sub_message"}}订阅「{{.Label}}」定时拉来的聊天消息
{{- else if eq .Kind "sub_notice"}}订阅「{{.Label}}」定时拉来的公告 / 通知，多数知悉即可；要用户报名、确认、付款等才需要用户
{{- else if eq .Kind "sub_deadline"}}订阅「{{.Label}}」定时拉来的截止事项（作业、账单、申请等），临近截止、还没完成的需要用户
{{- else if eq .Kind "sub_event"}}订阅「{{.Label}}」定时拉来的日程 / 活动；要用户回复邀请或临近开始的需要用户
{{- else if eq .Kind "sub_update"}}订阅「{{.Label}}」拉到的状态变化（余额、持仓、订单状态等的最新快照）；用户的规则给了阈值时按规则判断，否则知悉即可
{{- else if eq .Kind "sub_digest"}}订阅「{{.Label}}」刚打开时补拉的前一天的条目，合成一条汇总，每行一条；摘要里点出哪几条需要用户
{{- else if eq .Kind "sub_failing"}}Persona 的提醒：订阅「{{.Label}}」连续一天拉取失败
{{- else}}订阅「{{.Label}}」定时拉来的条目{{end}}
