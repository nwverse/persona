Read structured decisions from the unified ledger using after pagination, provenance, trust, status, scope and tag filters. Expired entries are omitted unless requested. Keep user-authored decisions distinct from agent notes and inferred quotes.{{template "shared/mcp_language" .}}

```json
{"type":"object","additionalProperties":false,"properties":{"after":{"type":"string"},"limit":{"type":"integer"},"by":{"type":"string"},"trust":{"type":"string"},"status":{"type":"string"},"tag":{"type":"string"},"scope":{"type":"object","properties":{"executor":{"type":"string"},"machine":{"type":"array","items":{"type":"string"}},"project":{"type":"array","items":{"type":"string"}},"personal":{"type":"boolean"}}}}}
```
