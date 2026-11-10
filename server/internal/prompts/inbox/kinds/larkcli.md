{{if eq .Kind "dm"}}类型：通过 lark-cli 收到的 Lark 私聊，另一方是人
{{else if eq .Kind "bot"}}类型：通过 lark-cli 收到的 Lark 私聊，另一方是机器人或应用
{{else if eq .Kind "mention"}}类型：通过 lark-cli 收到的群消息，提到了你或所有人
{{else if eq .Kind "group"}}类型：通过 lark-cli 收到的普通群消息，包括你参与的话题回复
{{else}}类型：通过 lark-cli 收到的 Lark 消息{{end}}
