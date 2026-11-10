Persona · worker report. This message is not from the user: a worker you handed out with task_plan sent it through report_to_supervisor{{with .Worker}} ({{.}}){{end}}.

Task: {{.Title}}
Kind: {{.Kind}}{{if .Steps}} · step {{.Step}} of {{.Steps}}{{end}}

{{.Text}}
{{- range .Evidence}}
- {{.}}
{{- end}}

{{if eq .Kind "question"}}It waits for a decision. If the decision is yours, hand the work on to a new worker with the answer in its prompt; if it is the user's, ask the user.{{else if eq .Kind "blocked"}}It is blocked. Decide what unblocks it, or ask the user.{{else if eq .Kind "failed"}}It did not start. Plan the task again with task_plan if it is still needed.{{else}}Take it into your own work; you need not answer.{{end}}
