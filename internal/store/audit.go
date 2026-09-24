package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/transport"
)

// Audit writes audit_log rows, each in its own statement on the pool, outside any task
// transaction, so a crash never loses the record of a command that left (FR-023).
type Audit struct {
	DB    *pgxpool.Pool
	Actor string // collector:<id>
}

func (a *Audit) Sent(ctx context.Context, action string, target netip.Addr, command string) (transport.Ref, error) {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	ref := transport.Ref{
		ID:      fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]),
		Action:  action,
		Target:  target,
		Command: command,
	}
	return ref, a.write(ctx, ref, "sent")
}

func (a *Audit) Result(ctx context.Context, ref transport.Ref, result string) error {
	return a.write(ctx, ref, result)
}

func (a *Audit) write(ctx context.Context, ref transport.Ref, result string) error {
	_, err := a.DB.Exec(ctx, `INSERT INTO audit_log (ref, actor, action, target, command, result) VALUES ($1, $2, $3, $4, $5, $6)`,
		ref.ID, a.Actor, ref.Action, ref.Target, null(ref.Command), result)
	return err
}
