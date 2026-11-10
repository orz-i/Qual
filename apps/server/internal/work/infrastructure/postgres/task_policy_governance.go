package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

func (r Repository) taskPolicyGovernanceSnapshot(ctx context.Context, tx pgx.Tx,
	actor identity.Actor, project identity.Project, write bool) (application.TaskAuthorizationPolicyView, error) {
	view := application.TaskAuthorizationPolicyView{ProjectID: project.ID,
		Mode: domain.LegacyAuthorization, Source: "default", Version: 0}
	lock := "FOR SHARE"
	if write {
		lock = "FOR UPDATE"
	}
	var projectID uuid.UUID
	// This locks the same Project row as lockTaskMode, serializing a policy
	// switch with EVERY scoped WorkItem mutation, including absent policy rows.
	if err := tx.QueryRow(ctx, `SELECT id FROM qual.projects
   WHERE id=$1 AND workspace_id=$2 `+lock, project.ID, project.WorkspaceID).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return view, identity.ErrNotFound
		}
		return view, err
	}
	current, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return view, err
	}
	effect, err := overrideEffect(ctx, tx, actor, project, idomain.AccessManage)
	if err != nil {
		return view, err
	}
	if !idomain.CanWithEffect(current.kind, current.role, current.scopes, idomain.AccessManage, effect) {
		return view, identity.ErrForbidden
	}
	var version int
	var policyTable, historyTable, reviewTable, boundColumn, boundIndex, boundTrigger bool
	err = tx.QueryRow(ctx, `SELECT
   COALESCE((SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied),0),
   to_regclass('qual.project_task_authorization_policies') IS NOT NULL,
   to_regclass('qual.task_authorization_policy_changes') IS NOT NULL,
   to_regclass('qual.work_review_assignments') IS NOT NULL,
   EXISTS(SELECT 1 FROM information_schema.columns
     WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id'),
   to_regclass('qual.test_runs_bound_work_idx') IS NOT NULL,
   EXISTS(SELECT 1 FROM pg_trigger tg WHERE tg.tgrelid=to_regclass('qual.test_runs')
     AND tg.tgname='test_runs_append_only' AND tg.tgenabled <> 'D'
     AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()'))`).Scan(
		&version, &policyTable, &historyTable, &reviewTable, &boundColumn, &boundIndex, &boundTrigger)
	if err != nil {
		return view, err
	}
	view.SchemaReady = version >= 20 && policyTable && historyTable && reviewTable &&
		boundColumn && boundIndex && boundTrigger
	if !policyTable {
		return view, nil
	}
	var mode domain.TaskAuthorizationMode
	err = tx.QueryRow(ctx, `SELECT mode,version FROM qual.project_task_authorization_policies
  WHERE project_id=$1`, project.ID).Scan(&mode, &view.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	if mode != domain.LegacyAuthorization && mode != domain.TaskScopedAuthorization {
		return view, domain.ErrValidation
	}
	view.Mode = mode
	view.Source = "project"
	return view, nil
}
func (r Repository) GetTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor,
	project identity.Project) (application.TaskAuthorizationPolicyView, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return application.TaskAuthorizationPolicyView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	view, err := r.taskPolicyGovernanceSnapshot(ctx, tx, actor, project, false)
	if err != nil {
		return view, err
	}
	return view, tx.Commit(ctx)
}
func (r Repository) PreviewTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor,
	project identity.Project, target domain.TaskAuthorizationMode) (application.TaskAuthorizationPolicyPreview, error) {
	out := application.TaskAuthorizationPolicyPreview{TargetMode: target}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out.Current, err = r.taskPolicyGovernanceSnapshot(ctx, tx, actor, project, false)
	if err != nil {
		return out, err
	}
	out.WouldChange = out.Current.Mode != target
	out.LegacyScopeWidening = out.Current.Mode == domain.TaskScopedAuthorization && target == domain.LegacyAuthorization
	out.RequiresLegacyWideningAck = out.LegacyScopeWidening
	err = tx.QueryRow(ctx, `SELECT COUNT(*),
  COUNT(*) FILTER(WHERE status IN ('backlog','todo','in_progress','in_review') AND assignee_id IS NULL)
  FROM qual.work_items WHERE project_id=$1`, project.ID).Scan(&out.WorkItemsTotal, &out.UnassignedOpenItems)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// ChangeTaskAuthorizationPolicy is an authenticated, explicit project-level
// governance transition. Unlike a feature flag in a Web component this is
// versioned, idempotent and shares the Project lock with all WorkItem writes.
func (r Repository) ChangeTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor,
	project identity.Project, input application.TaskAuthorizationPolicyChange,
	meta application.Meta, hash [32]byte) (application.TaskAuthorizationPolicyView, error) {
	result := application.TaskAuthorizationPolicyView{}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records
  (project_id,actor_id,operation,request_key,request_hash)
  VALUES($1,$2,'task_authorization_policy.change',$3,$4)
  ON CONFLICT DO NOTHING RETURNING request_hash`,
		project.ID, actor.PrincipalID, meta.IdempotencyKey, hash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var oldHash, oldBody []byte
		var oldStatus int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
   FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2
   AND operation='task_authorization_policy.change' AND request_key=$3 FOR UPDATE`,
			project.ID, actor.PrincipalID, meta.IdempotencyKey).Scan(&oldHash, &oldBody, &oldStatus)
		if err != nil {
			return result, err
		}
		if !bytes.Equal(oldHash, hash[:]) || oldStatus != 200 {
			return result, application.ErrIdempotency
		}
		if err = json.Unmarshal(oldBody, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	old, err := r.taskPolicyGovernanceSnapshot(ctx, tx, actor, project, true)
	if err != nil {
		return result, err
	}
	if old.Version != input.ExpectedVersion {
		return result, domain.ErrConflict
	}
	if old.Mode == input.Mode {
		return result, domain.ErrValidation
	}
	if !old.SchemaReady {
		// Never turn an incomplete Goose18-19 or inconsistent Goose20 install
		// into a live dynamic-mode policy. No silent legacy fallback.
		return result, domain.ErrDoneGate
	}
	if old.Mode == domain.TaskScopedAuthorization && input.Mode == domain.LegacyAuthorization &&
		!input.AcknowledgeLegacyWidening {
		return result, domain.ErrValidation
	}
	result = application.TaskAuthorizationPolicyView{ProjectID: project.ID, Mode: input.Mode,
		Version: old.Version + 1, Source: "project", SchemaReady: true}
	if old.Version == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies
   (workspace_id,project_id,mode,version,changed_by)
   VALUES($1,$2,$3,$4,$5)`, project.WorkspaceID, project.ID, result.Mode,
			result.Version, actor.PrincipalID)
	} else {
		tag, e := tx.Exec(ctx, `UPDATE qual.project_task_authorization_policies
   SET mode=$1,version=$2,changed_by=$3,updated_at=$4
   WHERE project_id=$5 AND version=$6`,
			result.Mode, result.Version, actor.PrincipalID, time.Now().UTC(), project.ID, old.Version)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			err = domain.ErrConflict
		}
	}
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.task_authorization_policy_changes
   (id,workspace_id,project_id,actor_id,credential_id,request_id,
    old_mode,new_mode,old_version,new_version,reason)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		uuid.New(), project.WorkspaceID, project.ID, actor.PrincipalID, actor.CredentialID,
		meta.RequestID, old.Mode, result.Mode, old.Version, result.Version, input.Reason)
	if err != nil {
		return result, err
	}
	prev := old.Version
	err = r.emit(ctx, tx, actor, project, meta.RequestID,
		"project.task_authorization_policy_changed", project.ID, &prev, result.Version,
		map[string]any{"from": old.Mode, "to": result.Mode, "legacy_scope_widening": old.Mode == domain.TaskScopedAuthorization && result.Mode == domain.LegacyAuthorization,
			"reason": input.Reason})
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=200
  WHERE project_id=$2 AND actor_id=$3 AND operation='task_authorization_policy.change'
    AND request_key=$4`, body, project.ID, actor.PrincipalID, meta.IdempotencyKey)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

var _ application.TaskAuthorizationPolicyRepository = Repository{}
