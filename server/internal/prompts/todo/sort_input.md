{{if eq .Language "日本語"}}## プロジェクト
{{range .Projects}}- slug {{.Slug}}：{{.Name}}{{with .OneLine}}（{{.}}）{{end}}{{with .Dirs}}；フォルダ：{{.}}{{end}}
{{else}}（なし）
{{end}}
## 仕分ける ToDo
{{range .Todos}}- id {{.ID}}：{{.Title}}{{with .Notes}}；メモ：{{.}}{{end}}{{with .Dirs}}；作業フォルダ：{{.}}{{end}}
{{end}}{{else if .Language}}## Projects
{{range .Projects}}- slug {{.Slug}}: {{.Name}}{{with .OneLine}} ({{.}}){{end}}{{with .Dirs}}; folders: {{.}}{{end}}
{{else}}(none)
{{end}}
## To-dos to sort
{{range .Todos}}- id {{.ID}}: {{.Title}}{{with .Notes}}; notes: {{.}}{{end}}{{with .Dirs}}; works in: {{.}}{{end}}
{{end}}{{else}}## 项目
{{range .Projects}}- slug {{.Slug}}：{{.Name}}{{with .OneLine}}（{{.}}）{{end}}{{with .Dirs}}；目录：{{.}}{{end}}
{{else}}（没有）
{{end}}
## 待归类的待办
{{range .Todos}}- id {{.ID}}：{{.Title}}{{with .Notes}}；备注：{{.}}{{end}}{{with .Dirs}}；工作目录：{{.}}{{end}}
{{end}}{{end}}
