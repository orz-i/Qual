package postgres

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// UI, CLI and MCP all use this same ordered action registry. The server-side
// write guard remains authoritative. Do not expose anybody else's scopes.
var projectedTaskActions = []domain.TaskAction{
	domain.TaskClaim, domain.TaskUpdate, domain.TaskComment, domain.TaskStart,
	domain.TaskSubmitReview, domain.TaskCancel, domain.TaskRelease, domain.TaskReassign,
	domain.TaskReportTest, domain.TaskReadTest, domain.TaskReadEvidence, domain.TaskAttachEvidence,
	domain.TaskRecordExecutor, domain.TaskRecordTester, domain.TaskClaimReview,
	domain.TaskRecordReview, domain.TaskReturnForWork, domain.TaskReleaseReview,
	domain.TaskCompleteIndependent, domain.TaskCompleteOwner,
}

func (r Repository) GetTaskCapabilities(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID uuid.UUID) (application.TaskCapabilities, error) {
	out := application.TaskCapabilities{SchemaVersion: 1, WorkItemID: itemID,
		MyCapacity: []string{}, AllowedActions: []domain.TaskAction{}, DeniedReasons: map[domain.TaskAction]string{},
		VerificationRequired: true}
	// A read-only decision snapshot must not race an in-flight Claim, project
	// policy change, credential rotation, permission Deny or review assignment.
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	mode, err := r.lockTaskMode(ctx, tx, project)
	if err != nil {
		return out, err
	}
	out.ProjectMode = mode
	w, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR SHARE OF w`, project.ID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, application.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.WorkVersion = w.Version
	snapshot, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return out, err
	}
	effects := make(map[idomain.Permission]idomain.Effect)
	for _, p := range []idomain.Permission{idomain.WorkRead, idomain.WorkClaim, idomain.WorkWrite,
		idomain.WorkReview, idomain.TestWrite, idomain.ArtifactWrite, idomain.TestRead,
		idomain.ArtifactRead, idomain.AccessManage} {
		effect, err := overrideEffect(ctx, tx, actor, project, p)
		if err != nil {
			return out, err
		}
		effects[p] = effect
	}
	can := func(p idomain.Permission) bool {
		return idomain.CanWithEffect(snapshot.kind, snapshot.role, snapshot.scopes, p, effects[p])
	}
	participant := can(idomain.WorkRead) && can(idomain.WorkClaim) &&
		(snapshot.kind == idomain.Human || snapshot.kind == idomain.Agent)
	manager := can(idomain.AccessManage)
	policy, view, err := resolveWorkReviewPolicy(ctx, tx, w)
	if err != nil {
		return out, err
	}
	out.PolicyVersion = policy.Version
	out.PolicySource = string(view.Source)
	out.ReviewMode = policy.Mode
	reviewer, _, err := activeReviewer(ctx, tx, w)
	if err != nil {
		return out, err
	}
	executor := w.AssigneeID != nil && *w.AssigneeID == actor.PrincipalID
	owner := (w.AccountableOwnerID != nil && *w.AccountableOwnerID == actor.PrincipalID) ||
		domain.IsAssignedOwnerDelegate(w, actor.PrincipalID)
	reviewing := reviewer != nil && *reviewer == actor.PrincipalID
	if participant {
		out.MyCapacity = append(out.MyCapacity, "candidate")
	}
	if executor {
		out.MyCapacity = append(out.MyCapacity, "executor")
	}
	if reviewing {
		out.MyCapacity = append(out.MyCapacity, "reviewer")
	}
	if owner {
		out.MyCapacity = append(out.MyCapacity, "owner")
	}
	// No remote actor/role supplied by the client is trusted. In particular a
	// former Executor cannot claim an independent review after reassign.
	historyIndependent := true
	if participant && mode == domain.TaskScopedAuthorization && w.Status == domain.InReview &&
		policy.Mode == domain.IndependentReview && reviewer == nil {
		former, err := wasTaskExecutor(ctx, tx, w, actor.PrincipalID)
		if err != nil {
			return out, err
		}
		historyIndependent = !former
	}
	for _, action := range projectedTaskActions {
		canonical, registered := application.TaskActionPermission(action)
		if !registered {
			continue
		}
		permitted := false
		reason := "not_current_task_capacity"
		if mode == domain.TaskScopedAuthorization {
			if !participant {
				reason = "participation_required"
			} else if effects[canonical] == idomain.Deny {
				reason = "project_explicit_deny"
			} else {
				facts := domain.TaskAccessFacts{
					ActorID: actor.PrincipalID, WorkItem: w, Action: action,
					ProjectMode: mode, Participant: participant, ExplicitDeny: false,
					ReviewerID: reviewer, ReviewMode: policy.Mode, ForcedIndependent: policy.ForceIndependent,
				}
				permitted = facts.Allowed()
				if action == domain.TaskClaimReview && !historyIndependent &&
					w.Status == domain.InReview && policy.Mode == domain.IndependentReview {
					permitted = false
					reason = "not_independent"
				}
				if (action == domain.TaskRelease || action == domain.TaskReassign) && !permitted && manager {
					// This is the separately authenticated access:manage recovery path,
					// not an extra project grant derived from a claimed task.
					if (w.Status == domain.Backlog || w.Status == domain.Todo || w.Status == domain.InProgress) &&
						(w.AssigneeID != nil || (action == domain.TaskReassign && w.Status == domain.InProgress)) {
						permitted = true
					}
				}
				if !permitted && reason == "not_current_task_capacity" {
					reason = "status_or_relation"
				}
			}
		} else if mode == domain.LegacyAuthorization {
			if can(canonical) {
				permitted = legacyActionReady(action, w, actor.PrincipalID, policy.Mode, manager)
			} else if (action == domain.TaskRelease || action == domain.TaskReassign) && manager {
				permitted = legacyActionReady(action, w, actor.PrincipalID, policy.Mode, manager)
			} else {
				reason = "project_permission_required"
			}
			if !permitted && reason == "not_current_task_capacity" {
				reason = "status_or_relation"
			}
		}
		if permitted {
			out.AllowedActions = append(out.AllowedActions, action)
		} else if action == domain.TaskClaim || action == domain.TaskUpdate || action == domain.TaskComment ||
			action == domain.TaskReassign || action == domain.TaskRelease ||
			action == domain.TaskClaimReview || action == domain.TaskRecordReview ||
			action == domain.TaskCompleteOwner || action == domain.TaskCompleteIndependent {
			out.DeniedReasons[action] = reason
		}
	}
	sort.Strings(out.MyCapacity)
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// For projects that explicitly stay legacy, return permitted UI choices that
// agree with the existing project-wide authorization and actual work status.
// Reviewer Claim is an opt-in task_scoped API, never available in legacy.
func legacyActionReady(a domain.TaskAction, w domain.WorkItem, actor uuid.UUID,
	reviewMode domain.ReviewPolicyMode, manager bool) bool {
	active := w.Status != domain.Done && w.Status != domain.Cancelled
	executor := w.AssigneeID != nil && *w.AssigneeID == actor
	owner := (w.AccountableOwnerID != nil && *w.AccountableOwnerID == actor) ||
		domain.IsAssignedOwnerDelegate(w, actor)
	switch a {
	case domain.TaskClaim:
		return (w.Status == domain.Backlog || w.Status == domain.Todo) && w.AssigneeID == nil
	case domain.TaskClaimReview, domain.TaskReleaseReview:
		return false
	case domain.TaskRelease, domain.TaskReassign:
		return (w.Status == domain.Backlog || w.Status == domain.Todo || w.Status == domain.InProgress) &&
			(executor || manager) && (w.AssigneeID != nil || (a == domain.TaskReassign && w.Status == domain.InProgress))
	case domain.TaskStart:
		return w.Status == domain.Backlog || w.Status == domain.Todo
	case domain.TaskSubmitReview, domain.TaskCancel:
		return w.Status == domain.InProgress
	case domain.TaskReturnForWork:
		return w.Status == domain.InReview
	case domain.TaskCompleteOwner:
		return w.Status == domain.InReview && reviewMode == domain.OwnerAcceptance && owner
	case domain.TaskCompleteIndependent:
		return w.Status == domain.InReview && reviewMode == domain.IndependentReview
	case domain.TaskUpdate, domain.TaskRecordExecutor, domain.TaskComment:
		return active
	case domain.TaskReadTest, domain.TaskReadEvidence:
		return true
	case domain.TaskRecordReview:
		return w.Status == domain.InReview
	case domain.TaskRecordTester, domain.TaskReportTest, domain.TaskAttachEvidence:
		return active
	default:
		return false
	}
}
