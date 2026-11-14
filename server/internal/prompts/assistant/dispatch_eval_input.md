{{if .Machines}}在线电脑：
{{range .Machines}}- {{.ID}}{{with .Note}}（{{.}}）{{end}}
{{end}}{{end}}{{if .Projects}}知识库的项目映射：
{{range .Projects}}- {{.Name}} → {{.Dir}}
{{end}}{{end}}用户的消息：
「{{.Message}}」
