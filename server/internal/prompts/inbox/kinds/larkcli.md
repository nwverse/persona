{{if eq .Kind "message"}}类型：通过 lark-cli 收到的 Lark 消息；可能是私聊、@ 用户或参与过的话题回复
{{else if eq .Kind "doc"}}类型：通过 lark-cli 监视的 Lark 云文档评论；评论提到了用户或回复了用户参与过的评论
{{else}}类型：通过 lark-cli 收到的 Lark 消息{{end}}
