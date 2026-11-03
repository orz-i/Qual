package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	identity "qual/internal/identity/application"
)

// issueBrowserCode creates a single-use, 5-minute secret for a pre-existing
// active Human. This is a privileged offline operation, not an HTTP endpoint.
func issueBrowserCode(ctx context.Context, pool *pgxpool.Pool, principalID uuid.UUID) error {
	var active bool
	err := pool.QueryRow(ctx, `SELECT true FROM qual.principals
        WHERE id=$1 AND kind='human' AND disabled_at IS NULL`, principalID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("active Human Principal not found")
	}
	if err != nil {
		return err
	}
	code, hash, err := identity.GenerateSecret(identity.LoginCodePrefix)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO qual.browser_login_codes(id,principal_id,code_hash,expires_at)
        VALUES($1,$2,$3,$4)`, uuid.New(), principalID, hash[:], time.Now().Add(5*time.Minute))
	if err != nil {
		return err
	}
	fmt.Println("One-time browser login code (5 min; shown once):", code)
	return nil
}
