现在：{{.Now}}
Agent：{{.Agent}}（{{.Machine}}{{with .Cwd}}，目录 {{.}}{{end}}）{{with .Project}}
项目：{{.}}{{end}}
{{with .Ask}}
这一轮用户给它的要求：
{{.}}
{{end}}
它停下时最后的回复：
{{.Reply}}
{{with .Others}}
同一台电脑上在工作的其他 Agent：
{{range .}}- id={{.ID}}：{{.Label}}{{with .Line}}（{{.}}）{{end}}
{{end}}{{end}}{{with .ApprovedThen}}
{{if $.ApprovedByTask}}它的任务已经授权{{else}}用户已经同意{{end}}：{{with $.ApprovedWait}}等「{{.}}」好了以后{{else}}等的事好了以后{{end}}去「{{.}}」。它如果还在等同一件事、之后要做的还是这一步（同一个分支 / 环境 / 对象，没多做别的），就写 on=external、wait.approved=true，wait.then 照写这一步，不用再问用户；等的事或要做的事变了，照安全规则问用户，不写 approved。
{{end}}
