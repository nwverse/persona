package node

import "github.com/nwverse/persona/server/internal/i18n"

func readonlyStartUnsupported() error {
	return refusal("readonly_unsupported", i18n.T(i18n.Default, "rpc-errors.readonly_unsupported"), nil)
}
