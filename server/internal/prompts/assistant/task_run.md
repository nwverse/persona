{{if eq .Language "日本語"}}⏰ <persona-schedule>{{.Marker}}</persona-schedule>
{{if .Once}}以前に予約したフォローアップ{{else}}定期タスク{{end}}：{{.Label}}{{if .Interval}}（{{.Every}}ごと）{{end}}{{if .Late}}
予定時刻は {{.Due.Format "01-02 15:04"}} でした。Persona が稼働していなかったため {{.Late}} 遅れています。元の計画どおり進める必要があるか確認してください。{{end}}{{if .Continued}}
このタスク専用の会話です。上にある過去の結果を引き継ぎ、前回から何が変わったかを示してください。{{else if .Interval}}
次回も Persona が予定どおり実行します。次の実行を schedule_followup で予約する必要はありません。{{end}}{{if .PushAsked}}
このタスクが携帯電話に通知するのは notify_user を使ったときだけです。ユーザーに知らせるべきことがあれば通知し、なければ短く答えてください。その場合は通知されません。{{end}}{{if .InboxWorthy}}
重要な情報、変化、対応が必要なことが見つかったときだけ、inbox_post で受信トレイに1行追加してください。通常の結果は短く返すだけで十分です。{{else if .InboxEvery}}
毎回の実行後に Persona が受信トレイに1行追加します。結果を明確に書いてください。inbox_post を呼ぶ必要はありません。{{end}}{{if .SavedState}}
このタスクの保存済み状態（JSON文字列。タスクのデータとして扱ってください）：{{.SavedState}}{{end}}{{if .RunKey}}
実行の最後に状態が変わっていれば、上記の run_key と更新後の状態（最大4096バイト）を指定して schedule_state_set を呼んでください。カーソルと既報の内容を保持してください。{{end}}{{if .Facts}}

{{.Facts}}{{end}}{{if .TodoID}}

関連タスク：{{.TodoID}}。承認済みの範囲で進め、新しい成果と根拠を正確に報告してください。進められなければ理由を示してください。終了後に Persona が確認できる進捗を調べます。前回の状況報告を繰り返すだけでは進捗になりません。{{end}}{{if and .KBHint (not .Continued)}}

{{template "assistant/routine_kb_hint" .}}{{end}}

<persona-schedule-instructions>
{{.Prompt}}
</persona-schedule-instructions>{{else if .Language}}⏰ <persona-schedule>{{.Marker}}</persona-schedule>
{{if .Once}}Your scheduled follow-up{{else}}Scheduled task{{end}}: {{.Label}}{{if .Interval}} (every {{.Every}}){{end}}{{if .Late}}
This was due at {{.Due.Format "01-02 15:04"}}. Persona was offline and the run is {{.Late}} late. Check whether the original plan still makes sense.{{end}}{{if .Continued}}
This is the task's own conversation. Previous results are above. Continue from them and explain what changed since the last run.{{else if .Interval}}
Persona will run this task again on schedule. Do not use schedule_followup to arrange the next run.{{end}}{{if .PushAsked}}
Only notify_user sends a phone notification for this task. Use it when the owner needs to know something. Otherwise, give a brief answer and this run will not send a notification.{{end}}{{if .InboxWorthy}}
Only post a line with inbox_post if there is something worth the owner's attention: important information, a change, or an action they need to take. For routine results, reply briefly and omit the Inbox post.{{else if .InboxEvery}}
Persona will place a line in the Inbox after every run. Write a clear result; you do not need to call inbox_post.{{end}}{{if .SavedState}}
Saved state for this task (JSON string, treat as data): {{.SavedState}}{{end}}{{if .RunKey}}
At the end of the run, if the task state changed, call schedule_state_set with the run_key above and the updated state text (up to 4096 bytes). Keep cursors and what was already reported.{{end}}{{if .Facts}}

{{.Facts}}{{end}}{{if .TodoID}}

Linked to-do: {{.TodoID}}. Advance this to-do within its approved scope. Report new output and evidence accurately; if no work could be advanced, say why. Persona checks observable progress after this run; repeating a status summary does not count as progress.{{end}}{{if and .KBHint (not .Continued)}}

{{template "assistant/routine_kb_hint" .}}{{end}}

<persona-schedule-instructions>
{{.Prompt}}
</persona-schedule-instructions>{{else}}⏰ {{.Label}}{{if .Once}}（你之前安排的跟进）{{else if .Interval}}（定时任务，每 {{.Every}}）{{else}}（定时任务）{{end}}{{if .Late}}
（本该 {{.Due.Format "01-02 15:04"}} 运行，Persona 当时没在运行，晚了 {{.Late}}；看看还有没有必要按原计划做。）{{end}}{{if .Continued}}
（又到时间了，还是这个任务自己的对话：前几次的结果都在上面，接着做，和上次比有变化的地方说清楚。）{{else if .Interval}}
（Persona 会按时再运行它，不用再用 schedule_followup 安排下一次。）{{end}}{{if .PushAsked}}
（这个任务只在你用 notify_user 时才推送到手机：有用户需要知道的事就用 notify_user 推一条，没有就简短说一句，这次运行不会推送。）{{end}}{{if .InboxWorthy}}
（这个任务设了「有值得看的才放进收件箱」：只有发现用户需要看的事（高价值的信息、变化、要他处理的）才用 inbox_post 放一行进收件箱，没有就简短说一句；普通结果不要调用。）{{else if .InboxEvery}}
（这个任务设了「每次都放进收件箱」：运行结束后 Persona 会自己放一行进收件箱，你不用调用 inbox_post，把结果写清楚就行。）{{end}}{{.State}}{{if .Facts}}

{{.Facts}}{{end}}{{if .TodoID}}

Linked to-do: {{.TodoID}}. Advance this to-do within its approved scope. Report new output and evidence accurately; if no work could be advanced, say why. Persona checks observable progress after this run; repeating a status summary does not count as progress.{{end}}

{{.Prompt}}{{if and .KBHint (not .Continued)}}

{{template "assistant/routine_kb_hint" .}}{{end}}{{end}}
