App: {{.App}}
Source: {{.Source}}
Tool: {{.Tool}}{{if .Description}}: {{.Description}}{{end}}
Arguments: {{.Args}}
{{if .Text -}}
The tool answers text, not JSON. Its start:
{{- else -}}
Its answer, cut:
{{- end}}

{{.Sample}}
