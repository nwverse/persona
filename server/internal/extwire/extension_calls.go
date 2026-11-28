package extwire

import (
	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/buswire"
)

// These methods run in the extension process.
const (
	MethodAuthRelayComplete = "auth_relay.complete"
	MethodSecretsDescribe   = "secrets.describe"
	MethodSecretsResolve    = "secrets.resolve"
	MethodSecretsStore      = "secrets.store"
	MethodUIImage           = "ui.image"
)

var (
	AuthRelayCompleteMethod = buswire.Method[wire.AuthRelayCompleteParams, wire.AuthRelayResult]{Name: MethodAuthRelayComplete}
	SecretsDescribeMethod   = buswire.Method[wire.SecretsDescribeParams, wire.SecretsDescribeResult]{Name: MethodSecretsDescribe}
	SecretsResolveMethod    = buswire.Method[wire.SecretsResolveParams, wire.SecretsResolveResult]{Name: MethodSecretsResolve}
	SecretStoreMethod       = buswire.Method[wire.SecretStoreParams, wire.SecretStoreResult]{Name: MethodSecretsStore}
	UIImageMethod           = buswire.Method[wire.UIImageParams, wire.UIImage]{Name: MethodUIImage}
)
