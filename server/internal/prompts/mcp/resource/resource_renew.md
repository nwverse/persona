{{if eq .Lang "en"}}Renew this session’s granted lease using its fencing token. Stop the runner client when fenced.{{else if eq .Lang "ja"}}フェンシングトークンで所有リースを更新します。fenced の場合はクライアントを停止してください。{{else}}使用 fencing token 续期本会话持有的租约；收到 fenced 时停止门禁客户端。{{end}}

```json
{
  "type": "object",
  "properties": {
    "resource": {
      "type": "string"
    },
    "lease_id": {
      "type": "string"
    },
    "token": {
      "type": "integer"
    }
  },
  "required": [
    "resource",
    "lease_id",
    "token"
  ]
}
```
