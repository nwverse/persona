{{if eq .Tag "sender"}}发送人{{else if eq .Tag "chat"}}会话{{else}}来源{{end}}：{{.Name}}
用户改过的事：{{range .Corrections}}
- {{template "inbox/correction" .}}{{end}}
