{{if eq .Language "日本語"}}今回の実行開始時に Persona が集計したノーススター指標です。次の数値をそのまま使い、変更や推測はしないでください。日報の冒頭で、今日の割り込み回数と可用率、昨日の同じ時間帯からの変化、データの出所を示し、その後に本文を続けてください。不明な値は不明と書いてください。{{range .Lines}}
- {{.}}{{end}}{{else if .Language}}North star metrics calculated by Persona at the start of this run. Use these exact figures; never change or invent them. Open this daily report with today's interruptions and availability, the change from the same period yesterday, and the sources. Continue with the rest of the report. Keep unknowns explicit:{{range .Lines}}
- {{.}}{{end}}{{else}}Persona 算好的北极星指标（这次开跑时的快照，数字以此为准，别改也别另编）。这份日报开头先照这几行报：今天的打扰次数和可用率、和昨天同一时段比的变化、数据来源，再接下面的内容；数字原样，未知就说未知：{{range .Lines}}
- {{.}}{{end}}{{end}}
