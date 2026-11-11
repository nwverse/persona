{{- /* A turn of the bound Persona conversation that came from elsewhere is over (docs/assistant/voice.md「Per-round tracking」, voice/runs.go):
a chat app's bot (.Channel: its name), or Persona's own (a scheduled task, a task reporting back; .Channel ""). Its
answer went back there: the voice model only mentions it, once per channel a call, and never reads what it said. */ -}}
{{- if eq .Lang "en" -}}
{{- $to := or .To "The Persona main conversation" -}}
{{$to}} has finished a turn that {{with .Channel}}came from {{.}}{{else}}began elsewhere (a scheduled task, a task reporting back){{end}}, not from the user in this call; its reply {{if .Channel}}went back there{{else}}is in the conversation{{end}}. Only mention it in one sentence ("{{if .Channel}}there's a new reply on {{.Channel}}{{else}}there's something new in the conversation{{end}}"), do not read it out, do not call any tools, and go on with what you were talking about.
{{- else if eq .Lang "ja" -}}
{{- $to := "Persona のメイン会話" -}}{{- with .To}}{{$to = print . " "}}{{end -}}
{{$to}}が{{with .Channel}}{{.}} から来た{{else}}ほか（スケジュールしたタスク、タスクの報告など）で始まった{{end}}ターンを終えました。この通話でユーザーが頼んだものではなく、返事は{{if .Channel}}そちらに送られています{{else}}会話の中にあります{{end}}。中身は読み上げず、ツールも呼ばず、「{{if .Channel}}{{.Channel}} のほうに新しい返事があります{{else}}会話に新しい動きがあります{{end}}」と一文で触れるだけにして、元の話を続けてください。
{{- else -}}
{{- $to := "Persona 主对话" -}}{{- $sp := "" -}}
{{- with .To}}{{$to = .}}{{$sp = " "}}{{end -}}
{{$to}}{{$sp}}刚做完了一轮，是{{with .Channel}}{{.}}那边发来的{{else}}别处（定时任务、任务汇报之类）开始的{{end}}，不是用户在这次通话里交代的，回复{{if .Channel}}已经发回那边了{{else}}在对话里{{end}}。内容不用念，也不要调用工具：只用一句话提一下「{{if .Channel}}{{.Channel}}那边有新回复{{else}}对话里有新动静{{end}}」，然后接着刚才的话聊。
{{- end -}}
