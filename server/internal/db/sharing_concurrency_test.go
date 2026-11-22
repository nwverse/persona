package db

import (
	"context"
	"testing"
	"time"
)

func TestShareOwnershipReadUsesTransactionConnection(t *testing.T) {
	d := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner := SessionShareOwnership{Machine: "synthetic-pool-machine", SessionID: "synthetic-pool-session", Issuer: "synthetic-pool-issuer", Subject: "60000000-0000-4000-8000-000000000001", Incarnation: "synthetic-pool-incarnation", Generation: "synthetic-pool-generation"}
	owner, err := d.AttributeSession(ctx, owner, "synthetic-admin", "Synthetic pool test", "synthetic-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	// Leave exactly one connection free. A transaction which takes a second
	// connection for its ownership check would wait until the deadline.
	for i := int32(0); i < d.pool.Config().MaxConns-1; i++ {
		conn, err := d.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(conn.Release)
	}
	if err := d.ShareTransaction(ctx, func(tx *ShareTx) error {
		got, err := tx.SessionShareOwner(owner.Machine, owner.SessionID)
		if err == nil && (got == nil || *got != owner) {
			t.Error("transaction ownership differs from the persisted row")
		}
		return err
	}); err != nil {
		t.Fatalf("ownership check acquired a second pool connection: %v", err)
	}
}
