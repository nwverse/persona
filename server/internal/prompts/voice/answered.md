{{- /* A question asked by voice is over (docs/assistant/voice.md「Questions and cards」): its answer went to the conversation (voice: on
the delegation that answered it; .Unfollowed: no run follows that conversation, so its result is not told by itself),
it was answered on the screen or another device while it was asked (elsewhere: said by itself, only for the one being
asked) or before the user's answer got there (late), the answer did not get there (failed: it is asked again when the
user says it again; .Code input_draft / dialog_open, a node's refusal, in words of its own, never the hub's, which
hold the box's draft or a dialog's text; else .Reason, the hub's own words), the connection broke off and the question
no longer waits (unknown), or it was not heard twice and is left to the screen (screen). No agent's words: nothing here
needs the <agent_output> framing. Data: VoiceAnswered. */ -}}
{{- if eq .Lang "en" -}}
{{- $to := or .To "the Persona main conversation" -}}
{{- $To := or .To "The Persona main conversation" -}}
{{- $where := print "on the phone, in " $to -}}{{if .Here}}{{$where = "on the screen"}}{{end -}}
{{- if eq .By "voice" -}}
The user's answer went to {{$to}}: it goes on with its work, it is not finished yet.{{if .Unfollowed}} What it does next is not followed in this call: you will not be told when it is done, so do not offer to tell the user; they can ask how far it is.{{end}} Tell the user briefly, in one sentence, that you passed it on; do not ask the question again.
{{- else if eq .By "elsewhere" -}}
The question {{$to}} asked was answered on the screen or elsewhere: it goes on with its work, it is not finished yet. Do not ask the question again; just confirm it briefly in one sentence.
{{- else if eq .By "late" -}}
What the user just said was not passed to {{$to}}: its question had already been answered on the screen or elsewhere. It goes on with its work, it is not finished yet. Tell the user in one sentence that the question was already answered; do not ask it again.
{{- else if eq .By "unknown" -}}
Not sure whether the user's answer got to {{$to}}: the connection broke off while it was passed on, and the question no longer waits; it is not finished yet. Do not ask it again; tell the user in one sentence to look {{$where}} whether it took their answer.
{{- else if eq .By "failed" -}}
The user's answer did not get to {{$to}}{{if eq .Code "input_draft"}}: its input box on the computer holds words typed there and not sent, so nothing was typed.{{else if eq .Code "dialog_open"}}: another dialog is open on the computer first, so nothing was typed.{{else if .Reason}}: {{.Reason}}{{else}}.{{end}}
It still waits for the answer, it is not finished. {{if eq .Code "input_draft"}}Ask the user in one sentence to send or clear those words on the computer, then say the answer again.{{else if eq .Code "dialog_open"}}Ask the user in one sentence to deal with that dialog on the computer, then say the answer again.{{else}}Ask the user in one sentence to say it again, or to answer it {{$where}}.{{end}}
{{- else -}}
The user's answer was not heard again, so the question is no longer asked by voice: {{$to}} still waits for it, it is not finished. Ask the user in one sentence to answer the card {{$where}}.
{{- end -}}
{{- else if eq .Lang "ja" -}}
{{- $to := "Persona のメイン会話" -}}{{- $at := "Persona メイン会話" -}}
{{- with .To}}{{$to = print . " "}}{{$at = print . " "}}{{end -}}
{{- $where := print "スマホの " $at "で" -}}{{if .Here}}{{$where = "画面で"}}{{end -}}
{{- if eq .By "voice" -}}
ユーザーの答えを{{$to}}に伝えました。続きの作業をするので、まだ終わっていません。{{if .Unfollowed}}この通話ではこの先の作業を追っていないので、終わっても知らせは来ません。終わったら伝えるとは言わないでください。進み具合はユーザーが聞けばわかります。{{end}}伝えたことを一文で短くユーザーに言ってください。同じ質問はもうしないでください。
{{- else if eq .By "elsewhere" -}}
{{$to}}の質問は画面か別の場所で答えられました。続きの作業をするので、まだ終わっていません。同じ質問はもうせず、一文で短く確認するだけにしてください。
{{- else if eq .By "late" -}}
ユーザーがいま言ったことは{{$to}}に伝えていません：その質問はもう画面か別の場所で答えられていました。続きの作業をするので、まだ終わっていません。もう答えてあることを一文でユーザーに伝え、同じ質問はしないでください。
{{- else if eq .By "unknown" -}}
ユーザーの答えが{{$to}}に届いたかわかりません：伝える途中で接続が切れ、その質問はもう待たれていません。続きの作業をしていて、まだ終わっていません。同じ質問はせず、答えが届いたか{{$where}}確認するよう一文でユーザーに伝えてください。
{{- else if eq .By "failed" -}}
ユーザーの答えが{{$to}}に届きませんでした{{if eq .Code "input_draft"}}：パソコン側の入力欄に、そこで打って送っていない文字が残っているので、何も入力していません。{{else if eq .Code "dialog_open"}}：パソコンで先に別のダイアログが開いているので、何も入力していません。{{else if .Reason}}：{{.Reason}}{{else}}。{{end}}
まだ答えを待っていて、終わっていません。{{if eq .Code "input_draft"}}パソコンでその文字を送るか消してから、もう一度答えを言うよう一文でユーザーに頼んでください。{{else if eq .Code "dialog_open"}}パソコンでそのダイアログを片付けてから、もう一度答えを言うよう一文でユーザーに頼んでください。{{else}}もう一度言うか、{{$where}}答えるよう一文でユーザーに頼んでください。{{end}}
{{- else -}}
またユーザーの答えが聞き取れなかったので、この質問はもう音声では聞きません：{{$to}}はまだ答えを待っていて、終わっていません。{{$where}}カードに答えるよう一文でユーザーに頼んでください。
{{- end -}}
{{- else -}}
{{- $to := "Persona 主对话" -}}{{- $sp := "" -}}
{{- with .To}}{{$to = .}}{{$sp = " "}}{{end -}}
{{- $where := print "手机上的 " $to $sp "里" -}}{{if .Here}}{{$where = "屏幕上"}}{{end -}}
{{- if eq .By "voice" -}}
用户的回答已经交给 {{$to}}{{$sp}}了：它会接着做，还没做完。{{if .Unfollowed}}这次通话没有跟着它接下来做的事，做完了不会告诉你，所以不要说「做完了告诉你」；用户想知道进度可以问。{{end}}用一句话简短告诉用户「好，告诉它了」，不要再问这个问题。
{{- else if eq .By "elsewhere" -}}
{{$to}}{{$sp}}的问题已经在屏幕上或者别的地方答了：它会接着做，还没做完。不要再问这个问题，用一句话简短确认一下就好。
{{- else if eq .By "late" -}}
用户刚才这句没有交给 {{$to}}：它的问题已经在屏幕上或者别的地方答过了，它会接着做，还没做完。用一句话告诉用户这个问题已经答过了，不要再问。
{{- else if eq .By "unknown" -}}
不确定用户的回答有没有交到 {{$to}}{{$sp}}：交的途中连接断了，而它已经不在等这个问题了，还没做完。不要再问，用一句话请用户在{{$where}}看一下它收到回答没有。
{{- else if eq .By "failed" -}}
用户的回答没能交给 {{$to}}{{if eq .Code "input_draft"}}：它在电脑上的输入框里有没发出去的字，什么都没输入。{{else if eq .Code "dialog_open"}}：电脑上先开着另一个对话框，什么都没输入。{{else if .Reason}}：{{.Reason}}{{else}}。{{end}}
它还在等这个回答，还没做完。{{if eq .Code "input_draft"}}用一句话请用户先在电脑上把那些字发出去或者清空，再说一遍回答。{{else if eq .Code "dialog_open"}}用一句话请用户先在电脑上处理那个对话框，再说一遍回答。{{else}}用一句话请用户再说一遍，或者在{{$where}}答。{{end}}
{{- else -}}
还是没听清用户的回答，这个问题不再用语音问了：{{$to}}{{$sp}}还在等，还没做完。用一句话请用户在{{$where}}点卡片回答。
{{- end -}}
{{- end -}}
