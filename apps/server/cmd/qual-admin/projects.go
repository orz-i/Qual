package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qual/internal/identity/domain"
)

func createProject(ctx context.Context, pool *pgxpool.Pool, workspace, key, name string) error {
	if strings.TrimSpace(name) == "" {
		name = key
	}
	var wsID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM qual.workspaces WHERE slug=$1`, workspace).Scan(&wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("workspace not found")
		}
		return err
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO qual.projects(id,workspace_id,key,name) VALUES($1,$2,$3,$4)`, id, wsID, key, name); err != nil {
		return err
	}
	fmt.Println("Project ID:", id)
	return nil
}

func grantMembership(ctx context.Context, pool *pgxpool.Pool, workspace, project string, principalID uuid.UUID, role domain.Role) error {
	var wsID, projectID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT w.id,p.id FROM qual.workspaces w
		JOIN qual.projects p ON p.workspace_id=w.id
		WHERE w.slug=$1 AND p.key=$2`, workspace, project).Scan(&wsID, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("workspace or project not found")
	}
	if err != nil {
		return err
	}
	var kind domain.PrincipalType
	err = pool.QueryRow(ctx, `SELECT kind FROM qual.principals
		WHERE id=$1 AND workspace_id=$2 AND disabled_at IS NULL`, principalID, wsID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("principal not in workspace or disabled")
	}
	if err != nil {
		return err
	}
	if kind == domain.Integration && role != domain.ProjectViewer && role != domain.ProjectMember {
		return errors.New("integration principals may only be project members/viewers")
	}
	_, err = pool.Exec(ctx, `INSERT INTO qual.memberships(id,workspace_id,project_id,principal_id,role)
		VALUES($1,$2,$3,$4,$5)`, uuid.New(), wsID, projectID, principalID, role)
	if err != nil {
		return err
	}
	fmt.Println("Project membership granted:", projectID, principalID)
	return nil
}

func disablePrincipal(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE qual.principals SET disabled_at=now()
		WHERE id=$1 AND disabled_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("principal not found or disabled")
	}
	if _, err := tx.Exec(ctx, `UPDATE qual.credentials SET revoked_at=now()
		WHERE principal_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("Principal disabled and credentials revoked:", id)
	return nil
}
