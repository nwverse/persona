{{if eq .Lang "en"}}Watch card "{{.Title}}" (id {{.ID}}): the owner asked you to check this and keep the answer on their Overview card.

Question: {{.Question}}

Find the current answer, then call watch_report once with id "{{.ID}}" and {{template "assistant/watch_run_fields" .}} Add `status` (ok / warn / bad) whenever you can judge it. Keep `text` to one short line the owner reads at a glance; say plainly when the answer could not be found, with status warn, rather than guessing.
Do not call notify_user: Persona pushes the alert itself when the card's rule trips. Reply in one line.{{else if eq .Lang "ja"}}ウォッチカード「{{.Title}}」（id {{.ID}}）：オーナーがこれを確認し、結果を概要のカードに表示しておくよう頼んでいます。

確認すること：{{.Question}}

いまの答えを調べたら、watch_report を id "{{.ID}}" で一度だけ呼び、{{template "assistant/watch_run_fields" .}}判断できるときは `status`（ok / warn / bad）も付けます。`text` はひと目で読める短い一行にし、答えが見つからなかったときは推測せず、そう書いて status を warn にします。
notify_user は呼ばないでください。カードのルールに当てはまったときは Persona が自分で通知します。返答は一行で。{{else}}关注卡「{{.Title}}」（id {{.ID}}）：用户请你查这件事，并把结果放在他概览页的卡片上。

要查的：{{.Question}}

查到现在的答案后，用 id "{{.ID}}" 调用一次 watch_report，{{template "assistant/watch_run_fields" .}}能判断时加上 `status`（ok / warn / bad）。`text` 写成一眼能看完的短短一行；查不到就照实说，status 用 warn，不要猜。
不要调用 notify_user：卡片的规则触发时 Persona 会自己推送提醒。回复一行就够。{{end}}
