{{if eq .Language "日本語"}}今日の夜の振り返りをお願いします。
まだ私の判断を待っていること、過去12時間に完了したこととうまくいかなかったこと、私の確認待ちのタスクは、上の「Persona が集計した事実」にある件数と記載方法に従ってください。その欄がない場合は、スナップショットからこれらを最初に1件ずつ示し、私が何をするかを書いてください。
1. 今日まず片付けるべきことを1～2文でまとめてください。
2. 今日の会議、議事録やLarkメッセージにある未完了のタスクや私が交わした約束、各コンピュータで実行中または停止しているAgentを挙げてください。
3. 明日の最初の予定を示してください。
Larkのカレンダースナップショットに載るのは、まだ終わっていない会議だけです。今日すでに開かれた会議と明日の予定はカレンダーを直接確認してください。iPhoneには phone_calendar_events、設定済みのLarkには lark-cli calendar +agenda を使い、明日の開始時刻と準備物を書いてください。
最後に、タスクをリマインダーに追加するか私に聞いてください。{{else if .Language}}Give me today's evening review.
For decisions still waiting on me, work completed or unsuccessful in the past 12 hours, and work awaiting my acceptance, use the counts and listing instructions in “Facts calculated by Persona” above. If there is no facts section, put these items first using the snapshot, one per line, and say what I need to do.
1. In one or two sentences, say what I should wrap up first today.
2. List today's meetings, outstanding to-dos and promises from meeting minutes or Lark messages, and Agents still running or stuck on each computer.
3. Give tomorrow's earliest commitment.
The Lark calendar snapshot contains only meetings that have not ended. Read the calendars for past meetings today and tomorrow's schedule: use phone_calendar_events for iPhone Calendar and, if configured, lark-cli calendar +agenda for Lark Calendar. Include tomorrow's times and what to prepare.
Finish by asking whether I want the to-dos added to Reminders.{{else}}今天的晚间回顾。
还等我决定的事、过去 12 小时做完和没成的、等我验收的任务：上面有「Persona 算好的事实」时，数字以它为准，要不要你列、怎么列按它的说明；没有这块时，你自己按快照把这几项列在最前面，每件一行，说清要我做什么。
你写：
1. 一两句总述：今天最该收尾的是哪件事；
2. 今天开了哪些会、有哪些待办和承诺没落实（会议纪要、飞书消息里我答应别人的事）、各台电脑上还在跑或卡住的 Agent；
3. 明天最早的安排。
快照里的飞书日历只有还没结束的会，已经开完的不在这里；今天开过的会和明天的安排要自己读日历（iPhone 日历用 phone_calendar_events；配了 lark-cli 的话飞书日历用 lark-cli calendar +agenda），明天的写清几点、要准备什么。
最后问我要不要把待办加进提醒事项。{{end}}
