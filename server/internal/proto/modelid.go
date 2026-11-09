package proto

// MaxModelIDLen bounds a model id Persona passes to an agent (a Bedrock inference-profile ARN is under 200).
const MaxModelIDLen = 256

// ModelIDRule says ValidModelID's rule in a tool's error for the model that called it (the apps' users read
// assistant-models.err_model_invalid).
const ModelIDRule = "start with a letter or digit (or ~ and then one), then only letters, digits and . _ - / : @ [ ] (at most 256 bytes)"

// ValidModelID says whether id may be passed to an agent as its model (#1644): `--model <id>` when it starts, `/model
// <id>` typed into a running Claude Code, a Codex app-server's model field. Claude Code and Codex take any string, so
// a typed id is only held to what real ids are made of: a letter or digit first (or a `~` and then one: a gateway's
// "latest" alias such as ~vendor/model-latest), then letters, digits and `. _ - / : @ [ ]`, at most MaxModelIDLen
// bytes. That covers aliases (opus), bracketed variants (opus[1m]), a gateway's vendor/model, tags (qwen3:32b), dated
// ids (claude-x@20250101) and ARNs, and keeps out what could become more than one argument or a flag on the way:
// whitespace, control characters, quotes, `$`, backticks, `;`, `|`, `&`, `*`, a leading `-`. Every place a model goes
// still quotes it (a leading `~` is never expanded); this is the rule all of them share, the same in the hub, the
// manager and the node.
func ValidModelID(id string) bool {
	if id == "" || len(id) > MaxModelIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case alnum(c):
		case i == 0 && c == '~' && len(id) > 1 && alnum(id[1]):
		case i > 0 && (c == '.' || c == '_' || c == '-' || c == '/' || c == ':' || c == '@' || c == '[' || c == ']'):
		default:
			return false
		}
	}
	return true
}

// alnum: an ASCII letter or digit.
func alnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
