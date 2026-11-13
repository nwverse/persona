{{- /* What a call bound to an agent sends it (docs/assistant/voice.md「Sending, gates, and 409」): the task on its first line (a run finds
the message by it when the transcript has no client_msg_id), then a context block. Not "keep the reply short": the
agent's work is what it is, the voice says what it needs of it. */ -}}
{{- if eq .Lang "en" -}}
{{.Task}}

<persona-context kind="context" title="Voice conversation" url="">
I said this in a voice conversation and the voice assistant passed it on, so it may be misheard: when something is ambiguous, ask me before acting on it.{{if .History}}

Said in the voice conversation just before:{{range .History}}
{{if eq .Role "user"}}Me{{else}}Voice assistant{{end}}: {{ctx .Text}}{{end}}{{end}}
</persona-context>
{{- else if eq .Lang "ja" -}}
{{.Task}}

<persona-context kind="context" title="音声会話" url="">
音声会話で言ったことを音声アシスタントが伝えています。聞き間違いがあるかもしれないので、あいまいなときは手を動かす前に確認してください。{{if .History}}

音声会話で直前に話したこと：{{range .History}}
{{if eq .Role "user"}}私{{else}}音声アシスタント{{end}}：{{ctx .Text}}{{end}}{{end}}
</persona-context>
{{- else -}}
{{.Task}}

<persona-context kind="context" title="语音对话" url="">
这是我在语音对话里说的，由语音助手转述，可能有识别错误；有歧义时先问我再动手。{{if .History}}

语音对话里刚才说的：{{range .History}}
{{if eq .Role "user"}}我{{else}}语音助手{{end}}：{{ctx .Text}}{{end}}{{end}}
</persona-context>
{{- end -}}
