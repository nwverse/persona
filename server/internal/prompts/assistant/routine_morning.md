{{if eq .Language "日本語"}}今日の朝のブリーフィングを、結論から始めて1画面に収めてください。
私の判断待ち、過去12時間に完了したこととうまくいかなかったこと、私の確認待ちのタスクは、上の「Persona が集計した事実」にある件数と記載方法に従ってください。その欄がない場合は、スナップショットからこれらを最初に1件ずつ示し、私が何をするか、どこを開くかを書いてください。
1. 今日最初に取り組むべきこととその理由を1～2文でまとめてください。
2. 今日のLarkとiPhoneの予定をまとめて重複を除き、準備が必要な会議を示してください。
3. 私が対応すべきLarkのメンション、返信や判断が必要な未読の個人メッセージ、重要なメールを挙げてください。
準備する価値のある会議は、それぞれ開始10分前に schedule_followup で準備を1回予約してください。資料、前回の議事録、話す要点を扱ってください。{{else if .Language}}Give me today's morning briefing in one screen. Lead with the conclusion.
For decisions waiting on me, work completed or unsuccessful in the past 12 hours, and work awaiting my acceptance, use the counts and listing instructions in “Facts calculated by Persona” above. If there is no facts section, put these items first using the snapshot, one per line. Say what I need to do and where to click.
1. In one or two sentences, tell me what to do first today and why.
2. Combine today's Lark and iPhone calendars, remove duplicates and mark meetings that need preparation.
3. List items I need to handle: Lark mentions and unread direct messages needing my reply or decision, and important emails.
For each meeting worth preparing for, use schedule_followup to schedule one preparation session 10 minutes before it starts, covering materials, previous minutes and talking points.{{else}}给我今天的早报，一屏以内，结论先行。
等我决定的事、过去 12 小时做完和没成的、等我验收的任务：上面有「Persona 算好的事实」时，数字以它为准，要不要你列、怎么列按它的说明；没有这块时，你自己按快照把这几项列在最前面，每件一行，说清要我做什么、去哪点。
你写：
1. 一两句总述：今天最该先做哪一件、为什么；
2. 今天的日程（飞书日历 + iPhone 日历，合并去重），标出需要准备的会；
3. 需要我处理的：飞书里 @我 / 单聊未读里要我回复或决定的事、重要邮件。
对今天每个值得准备的会，用 schedule_followup 在会前 10 分钟安排一次会前准备（材料、上次纪要、要点）。{{end}}
