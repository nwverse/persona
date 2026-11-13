App: {{.App}}
Source: {{.Source}}{{if .Service}} ({{.Service}}){{end}}

Read-only tools of this source:
{{- range .Tools}}

- {{.Name}}{{if .Title}} ({{.Title}}){{end}}{{if .Description}}: {{.Description}}{{end}}
  input_schema: {{.Schema}}
{{- end}}
