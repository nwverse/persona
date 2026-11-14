{{if eq .Kind "dm"}}类型：通过 lark-cli 收到的 Lark 私聊，另一方是人
{{else if eq .Kind "bot"}}类型：通过 lark-cli 收到的 Lark 私聊，另一方是机器人或应用
{{else if eq .Kind "mention"}}类型：通过 lark-cli 收到的群消息，提到了你或所有人
{{else if eq .Kind "group"}}类型：通过 lark-cli 收到的普通群消息，包括你参与的话题回复
{{else if eq .Kind "doc"}}类型：通过 lark-cli 监视的 Lark 云文档评论；评论提到了用户或回复了用户参与过的评论
{{else if eq .Kind "bot_handoff"}}Type: the owner explicitly mentioned the connected Lark bot to hand this message to Persona's main conversation. This is a handoff, not a new request for the owner. Any reply to Lark requires the owner's confirmation.
{{else}}类型：通过 lark-cli 收到的 Lark 消息{{end}}
