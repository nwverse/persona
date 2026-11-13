{{- /* Cards answered only on the screen (docs/assistant/voice.md「Questions and cards」, decision 5): a plan, a permission, a terminal
dialog, a form, a question voice cannot answer. Only their kinds, never a command, a path or what a plan says; voice
never answers them, and a spoken "approve" is not sent (voice/refused on_screen). Said by itself (a say). .Here: the
cards show on the screen of this call; else they are on the phone, in the conversation. Data: VoiceNeedsYou. */ -}}
{{- if eq .Lang "en" -}}
{{- $to := or .To "the Persona main conversation" -}}
{{- $To := or .To "The Persona main conversation" -}}
{{- $where := print "on the phone, in " $to -}}{{- $there := "on the phone" -}}{{if .Here}}{{$where = "on the screen"}}{{$there = $where}}{{end -}}
{{$To}} has stopped and waits for the user: {{range $i, $c := .Cards}}{{if $i}}; {{end}}{{if eq .Kind "permission"}}a permission to {{if eq .Tool "command"}}run a command{{else if eq .Tool "edit"}}change files{{else if eq .Tool "web"}}go online{{else if eq .Tool "mcp"}}use an MCP server's tool{{else}}use a tool{{end}}{{else if eq .Kind "plan"}}a plan to approve{{else if eq .Kind "screen"}}a dialog in its terminal{{else if eq .Kind "elicitation"}}a form to fill in{{else if eq .Kind "question"}}a question to answer {{$there}}{{else}}a card to deal with{{end}}{{end}}. It is not finished: it goes on once the user taps it {{$where}}.
Tell the user in one sentence what it waits for and to tap it {{$there}}, like "{{$To}} wants to run a command, approve it {{$there}}". Say only what sort it is: do not read out a command, a path or what the plan says, and do not answer it for the user; when the user says "approve" or "yes", ask them to tap it {{$there}}.
{{- else if eq .Lang "ja" -}}
{{- $to := "Persona のメイン会話" -}}{{- $at := "Persona メイン会話" -}}
{{- with .To}}{{$to = print . " "}}{{$at = print . " "}}{{end -}}
{{- $where := print "スマホの " $at "で" -}}{{- $there := "スマホで" -}}{{if .Here}}{{$where = "画面で"}}{{$there = $where}}{{end -}}
{{$to}}が止まって、ユーザーの対応を待っています：{{range $i, $c := .Cards}}{{if $i}}、{{end}}{{if eq .Kind "permission"}}{{if eq .Tool "command"}}コマンドの実行{{else if eq .Tool "edit"}}ファイルの変更{{else if eq .Tool "web"}}ネットへの接続{{else if eq .Tool "mcp"}}MCP サーバーのツールの使用{{else}}ツールの使用{{end}}の許可{{else if eq .Kind "plan"}}承認待ちの計画{{else if eq .Kind "screen"}}ターミナルのダイアログ{{else if eq .Kind "elicitation"}}入力するフォーム{{else if eq .Kind "question"}}{{$there}}答える質問{{else}}対応するカード{{end}}{{end}}。まだ終わっていません。ユーザーが{{$where}}押せば続きをやります。
何を待っているかと、{{$there}}押してほしいことを一文でユーザーに伝えてください。例：「{{$to}}がコマンドを実行したいそうです。{{$there}}承認してください」。種類だけを言い、コマンドやパス、計画の中身は読まず、ユーザーの代わりに答えないでください。ユーザーが口で「承認」「いいよ」と言ったら、{{$there}}押すよう伝えてください。
{{- else -}}
{{- $to := "Persona 主对话" -}}{{- $sp := "" -}}
{{- with .To}}{{$to = .}}{{$sp = " "}}{{end -}}
{{- $where := print "手机上的 " $to $sp "里" -}}{{- $there := "手机上" -}}{{if .Here}}{{$where = "屏幕上"}}{{$there = $where}}{{end -}}
{{$to}}{{$sp}}停下来在等用户处理：{{range $i, $c := .Cards}}{{if $i}}、{{end}}{{if eq .Kind "permission"}}{{if eq .Tool "command"}}运行一条命令{{else if eq .Tool "edit"}}改文件{{else if eq .Tool "web"}}联网{{else if eq .Tool "mcp"}}用一个 MCP 服务的工具{{else}}用一个工具{{end}}的授权{{else if eq .Kind "plan"}}一份等批准的计划{{else if eq .Kind "screen"}}终端里的一个对话框{{else if eq .Kind "elicitation"}}一张要填的表{{else if eq .Kind "question"}}一个要在{{$there}}答的问题{{else}}一张要处理的卡片{{end}}{{end}}。它还没做完，用户在{{$where}}点了才会接着做。
用一句话告诉用户它在等什么、请他在{{$there}}点一下，比如「{{$to}}{{$sp}}要运行一条命令，在{{$there}}批准一下」。只说是什么类别：不要念命令、路径和计划的内容，也不要替用户答；用户口头说「批准」「可以」时，请他在{{$there}}点。
{{- end -}}
