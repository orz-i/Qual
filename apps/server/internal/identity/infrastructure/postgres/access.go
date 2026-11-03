package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"qual/internal/identity/application"
	"qual/internal/identity/domain"
)

// Permission reads always remain Project-scoped; no other tenant membership
// or credential secrets are included in these responses.
type permissionRow struct {
	PrincipalID uuid.UUID
	Permission  domain.Permission
	Effect      domain.Effect
	Version     int64
}

func (r Repository) members(ctx context.Context, pid uuid.UUID, actor *application.Actor) ([]application.ProjectMember, error) {
	var projectWorkspace uuid.UUID
	err := r.Pool.QueryRow(ctx, `SELECT workspace_id FROM qual.projects WHERE id=$1`, pid).Scan(&projectWorkspace)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if actor != nil && actor.WorkspaceID != projectWorkspace {
		return nil, application.ErrNotFound
	}
	var rows pgx.Rows
	if actor == nil {
		rows, err = r.Pool.Query(ctx, `SELECT DISTINCT ON (p.id) p.id,p.display_name,p.kind,m.role
  FROM qual.memberships m JOIN qual.principals p
   ON p.id=m.principal_id AND p.workspace_id=m.workspace_id AND p.disabled_at IS NULL
  WHERE m.workspace_id=$2 AND (m.project_id=$1 OR (m.project_id IS NULL AND m.role='workspace_admin'))
  ORDER BY p.id,(m.project_id IS NULL) ASC LIMIT 101`, pid, projectWorkspace)
	} else {
		rows, err = r.Pool.Query(ctx, `SELECT DISTINCT ON (p.id) p.id,p.display_name,p.kind,m.role
  FROM qual.memberships m JOIN qual.principals p
   ON p.id=m.principal_id AND p.workspace_id=m.workspace_id AND p.disabled_at IS NULL
  WHERE m.workspace_id=$2 AND p.id=$3
   AND (m.project_id=$1 OR (m.project_id IS NULL AND m.role='workspace_admin'))
  ORDER BY p.id,(m.project_id IS NULL) ASC LIMIT 2`, pid, projectWorkspace, actor.PrincipalID)
	}
	if err != nil {
		return nil, err
	}
	members := []application.ProjectMember{}
	for rows.Next() {
		var m application.ProjectMember
		if err := rows.Scan(&m.PrincipalID, &m.DisplayName, &m.Kind, &m.Role); err != nil {
			rows.Close()
			return nil, err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if actor != nil && len(members) != 1 {
		return nil, application.ErrNotFound
	}
	if actor == nil && len(members) > 100 {
		return nil, application.ErrAccessValidation
	}
	if len(members) == 0 {
		return members, nil
	}
	overrides, err := r.Pool.Query(ctx, `SELECT principal_id,permission,effect,version FROM qual.project_permission_overrides
 WHERE project_id=$1 ORDER BY principal_id,permission`, pid)
	if err != nil {
		return nil, err
	}
	byPrincipal := make(map[uuid.UUID]map[domain.Permission]permissionRow)
	for overrides.Next() {
		var item permissionRow
		if err := overrides.Scan(&item.PrincipalID, &item.Permission, &item.Effect, &item.Version); err != nil {
			overrides.Close()
			return nil, err
		}
		if byPrincipal[item.PrincipalID] == nil {
			byPrincipal[item.PrincipalID] = make(map[domain.Permission]permissionRow)
		}
		byPrincipal[item.PrincipalID][item.Permission] = item
	}
	err = overrides.Err()
	overrides.Close()
	if err != nil {
		return nil, err
	}
	for i := range members {
		m := &members[i]
		m.Permissions = make([]application.PermissionSetting, 0, len(domain.AllPermissions))
		for _, permission := range domain.AllPermissions {
			state := application.PermissionSetting{Permission: permission, Effect: domain.Inherit}
			if override, ok := byPrincipal[m.PrincipalID][permission]; ok {
				state.Effect = override.Effect
				state.Version = override.Version
			}
			// Policy-level allowance only. A credential's scopes are an independent,
			// necessary condition and are never exposed for another Principal.
			state.AllowedByProject = domain.CanWithEffect(m.Kind, m.Role, []string{string(permission)}, permission, state.Effect)
			m.Permissions = append(m.Permissions, state)
		}
	}
	return members, nil
}
func (r Repository) ListMembers(ctx context.Context, pid uuid.UUID) ([]application.ProjectMember, error) {
	return r.members(ctx, pid, nil)
}
func (r Repository) Member(ctx context.Context, actor application.Actor, pid uuid.UUID) (application.ProjectMember, error) {
	list, err := r.members(ctx, pid, &actor)
	if err != nil {
		return application.ProjectMember{}, err
	}
	return list[0], nil
}

func (r Repository) Change(ctx context.Context, actor application.Actor, project application.Project,
	c application.PermissionChange, requestID, idem string, requestHash [32]byte) (application.PermissionSetting, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
 VALUES($1,$2,'access.change',$3,$4)
 ON CONFLICT DO NOTHING RETURNING request_hash`,
		project.ID, actor.PrincipalID, idem, requestHash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return application.PermissionSetting{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored, body []byte
		var status int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
    FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2
    AND operation='access.change' AND request_key=$3 FOR UPDATE`,
			project.ID, actor.PrincipalID, idem).Scan(&stored, &body, &status)
		if err != nil {
			return application.PermissionSetting{}, err
		}
		if !bytes.Equal(stored, requestHash[:]) {
			return application.PermissionSetting{}, application.ErrAccessIdempotency
		}
		if status != 200 {
			return application.PermissionSetting{}, application.ErrAccessConflict
		}
		var setting application.PermissionSetting
		if err := json.Unmarshal(body, &setting); err != nil {
			return setting, err
		}
		if err := tx.Commit(ctx); err != nil {
			return setting, err
		}
		return setting, nil
	}

	// Target must have active membership in this exact Project, or be its
	// Workspace Admin. Never grant to arbitrary Principal UUID from elsewhere.
	var targetKind domain.PrincipalType
	var targetRole domain.Role
	err = tx.QueryRow(ctx, `SELECT p.kind,m.role FROM qual.memberships m
  JOIN qual.principals p ON p.id=m.principal_id AND p.workspace_id=m.workspace_id
  WHERE m.workspace_id=$1 AND p.id=$2 AND p.disabled_at IS NULL
  AND (m.project_id=$3 OR (m.project_id IS NULL AND m.role='workspace_admin'))
  ORDER BY (m.project_id IS NULL) ASC LIMIT 1`, project.WorkspaceID, c.PrincipalID, project.ID).
		Scan(&targetKind, &targetRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.PermissionSetting{}, application.ErrNotFound
	}
	if err != nil {
		return application.PermissionSetting{}, err
	}
	if !domain.Grantable(targetKind, c.Permission) {
		return application.PermissionSetting{}, application.ErrAccessValidation
	}
	if targetRole == domain.WorkspaceAdmin {
		return application.PermissionSetting{}, application.ErrAccessLocked
	}
	if c.Permission == domain.AccessManage {
		// Agent manager cannot delegate its own management power to another
		// Agent. Only authenticated Human Workspace Admin can delegate this.
		if actor.Type != domain.Human {
			return application.PermissionSetting{}, application.ErrForbidden
		}
		var isAdmin bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
   SELECT 1 FROM qual.memberships WHERE workspace_id=$1 AND principal_id=$2
     AND project_id IS NULL AND role='workspace_admin')`,
			project.WorkspaceID, actor.PrincipalID).Scan(&isAdmin); err != nil {
			return application.PermissionSetting{}, err
		}
		if !isAdmin || targetRole == domain.WorkspaceAdmin {
			return application.PermissionSetting{}, application.ErrAccessLocked
		}
	}

	var currentEffect domain.Effect = domain.Inherit
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT effect,version FROM qual.project_permission_overrides
 WHERE workspace_id=$1 AND project_id=$2 AND principal_id=$3 AND permission=$4 FOR UPDATE`,
		project.WorkspaceID, project.ID, c.PrincipalID, c.Permission).Scan(&currentEffect, &currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return application.PermissionSetting{}, err
	}
	if currentVersion != c.ExpectedVersion || currentEffect == c.Effect {
		return application.PermissionSetting{}, application.ErrAccessConflict
	}
	version := currentVersion + 1
	if currentVersion == 0 {
		var inserted int64
		err = tx.QueryRow(ctx, `INSERT INTO qual.project_permission_overrides
  (workspace_id,project_id,principal_id,permission,effect,version,changed_by)
  VALUES($1,$2,$3,$4,$5,1,$6)
  ON CONFLICT DO NOTHING RETURNING version`,
			project.WorkspaceID, project.ID, c.PrincipalID, c.Permission, c.Effect, actor.PrincipalID).
			Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.PermissionSetting{}, application.ErrAccessConflict
		}
	} else {
		var updated int64
		err = tx.QueryRow(ctx, `UPDATE qual.project_permission_overrides SET effect=$1,version=version+1,updated_at=now(),changed_by=$2
  WHERE workspace_id=$3 AND project_id=$4 AND principal_id=$5 AND permission=$6 AND version=$7 RETURNING version`,
			c.Effect, actor.PrincipalID, project.WorkspaceID, project.ID, c.PrincipalID, c.Permission, c.ExpectedVersion).Scan(&updated)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.PermissionSetting{}, application.ErrAccessConflict
		}
	}
	if err != nil {
		return application.PermissionSetting{}, err
	}
	detail := map[string]any{
		"principal_id": c.PrincipalID, "permission": c.Permission, "before_effect": currentEffect,
		"after_effect": c.Effect, "old_version": currentVersion, "new_version": version, "reason": c.Reason,
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.project_permission_changes
   (id,workspace_id,project_id,principal_id,permission,before_effect,after_effect,
    old_version,new_version,changed_by,credential_id,request_id,reason)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		uuid.New(), project.WorkspaceID, project.ID, c.PrincipalID, c.Permission, currentEffect, c.Effect,
		currentVersion, version, actor.PrincipalID, actor.CredentialID, requestID, c.Reason)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	auditID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO qual.audit_records
 (id,workspace_id,project_id,actor_id,credential_id,request_id,operation,entity_id,old_version,new_version,diff)
 VALUES($1,$2,$3,$4,$5,$6,'permission.changed',$7,$8,$9,$10)`,
		auditID, project.WorkspaceID, project.ID, actor.PrincipalID, actor.CredentialID, requestID, c.PrincipalID,
		currentVersion, version, raw)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records(id,project_id,audit_id,actor_id,operation,entity_id)
 VALUES($1,$2,$3,$4,'permission.changed',$5)`,
		uuid.New(), project.ID, auditID, actor.PrincipalID, c.PrincipalID)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events(id,project_id,audit_id,event_type,aggregate_id,payload)
 VALUES($1,$2,$3,'permission.changed',$4,$5)`,
		uuid.New(), project.ID, auditID, c.PrincipalID, raw)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	result := application.PermissionSetting{Permission: c.Permission, Effect: c.Effect, Version: version,
		AllowedByProject: domain.CanWithEffect(targetKind, targetRole, []string{string(c.Permission)}, c.Permission, c.Effect)}
	body, _ := json.Marshal(result)
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=200
 WHERE project_id=$2 AND actor_id=$3 AND operation='access.change' AND request_key=$4`,
		body, project.ID, actor.PrincipalID, idem)
	if err != nil {
		return application.PermissionSetting{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return application.PermissionSetting{}, err
	}
	return result, nil
}
