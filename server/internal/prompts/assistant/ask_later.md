🔔 你 {{.When}} 用 ask_user 问过用户（wait: false，当时没有等回答）：
{{range .Questions}}- {{.}}
{{end}}{{if .Answers}}
用户在卡片上回答了：
{{range .Answers}}- {{.Question}} → {{.Choice}}
{{end}}
照用户的回答接着做，做了什么、还要用户做什么，简短告诉用户。{{if .Missing}}当时问了几个问题，上面没列出的用户没有答，需要的话再问（ask_user）。{{end}}
{{else if .Expired}}
卡片到点没人回：{{.Expired}}
{{else}}
这张卡片没有得到回答就结束了（用户把它关了，或者它已经失效）。用户已经在对话里直接答过的，照那个答案做；没答过的，不要按其中任何一个选项去做，也别再问同一个问题，事情要是还卡在这里，简短告诉用户卡在哪。
{{end}}
（这是 Persona 的通知，不是用户在这里发的消息。）
