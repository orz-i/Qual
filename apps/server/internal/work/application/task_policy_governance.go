package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// TaskAuthorizationPolicyView is project governance (not a grant). The row is
// absent by default, so legacy remains the universal safe installation mode.
type TaskAuthorizationPolicyView struct {
	ProjectID   uuid.UUID                    `json:"project_id"`
	Mode        domain.TaskAuthorizationMode `json:"mode"`
	Version     int64                        `json:"version"`
	Source      string                       `json:"source"`
	SchemaReady bool                         `json:"schema_ready"`
}
type TaskAuthorizationPolicyPreview struct {
	Current                   TaskAuthorizationPolicyView  `json:"current"`
	TargetMode                domain.TaskAuthorizationMode `json:"target_mode"`
	WouldChange               bool                         `json:"would_change"`
	LegacyScopeWidening       bool                         `json:"legacy_scope_widening"`
	RequiresLegacyWideningAck bool                         `json:"requires_legacy_widening_ack"`
	WorkItemsTotal            int64                        `json:"work_items_total"`
	UnassignedOpenItems       int64                        `json:"unassigned_open_items"`
}
type TaskAuthorizationPolicyChange struct {
	Mode                      domain.TaskAuthorizationMode `json:"mode"`
	ExpectedVersion           int64                        `json:"expected_version"`
	Reason                    string                       `json:"reason"`
	AcknowledgeLegacyWidening bool                         `json:"acknowledge_legacy_widening"`
}

func (c TaskAuthorizationPolicyChange) Validate() error {
	if c.Mode != domain.LegacyAuthorization && c.Mode != domain.TaskScopedAuthorization ||
		c.ExpectedVersion < 0 || strings.TrimSpace(c.Reason) != c.Reason ||
		utf8.RuneCountInString(c.Reason) < 16 || utf8.RuneCountInString(c.Reason) > 2000 {
		return domain.ErrValidation
	}
	// A true acknowledgement is mandatory for potentially widening rollback,
	// independently of what the caller guesses about the current mode.
	if c.Mode == domain.LegacyAuthorization && !c.AcknowledgeLegacyWidening {
		return domain.ErrValidation
	}
	return nil
}

type TaskAuthorizationPolicyRepository interface {
	GetTaskAuthorizationPolicy(context.Context, identity.Actor, identity.Project) (TaskAuthorizationPolicyView, error)
	PreviewTaskAuthorizationPolicy(context.Context, identity.Actor, identity.Project, domain.TaskAuthorizationMode) (TaskAuthorizationPolicyPreview, error)
	ChangeTaskAuthorizationPolicy(context.Context, identity.Actor, identity.Project, TaskAuthorizationPolicyChange, Meta, [32]byte) (TaskAuthorizationPolicyView, error)
}

func (s Service) taskModeGovernance(ctx context.Context, actor identity.Actor, pid uuid.UUID) (identity.Project, TaskAuthorizationPolicyRepository, error) {
	project, err := s.Identity.AuthorizeProject(ctx, actor, pid, idomain.AccessManage)
	if err != nil {
		return identity.Project{}, nil, err
	}
	r, ok := s.TaskPolicy.(TaskAuthorizationPolicyRepository)
	if !ok {
		return identity.Project{}, nil, domain.ErrValidation
	}
	return project, r, nil
}
func (s Service) GetTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor, pid uuid.UUID) (TaskAuthorizationPolicyView, error) {
	project, repo, err := s.taskModeGovernance(ctx, actor, pid)
	if err != nil {
		return TaskAuthorizationPolicyView{}, err
	}
	return repo.GetTaskAuthorizationPolicy(ctx, actor, project)
}
func (s Service) PreviewTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor, pid uuid.UUID,
	target domain.TaskAuthorizationMode) (TaskAuthorizationPolicyPreview, error) {
	if target != domain.LegacyAuthorization && target != domain.TaskScopedAuthorization {
		return TaskAuthorizationPolicyPreview{}, domain.ErrValidation
	}
	project, repo, err := s.taskModeGovernance(ctx, actor, pid)
	if err != nil {
		return TaskAuthorizationPolicyPreview{}, err
	}
	return repo.PreviewTaskAuthorizationPolicy(ctx, actor, project, target)
}
func (s Service) ChangeTaskAuthorizationPolicy(ctx context.Context, actor identity.Actor, pid uuid.UUID,
	change TaskAuthorizationPolicyChange, meta Meta) (TaskAuthorizationPolicyView, error) {
	if err := validMeta(meta); err != nil {
		return TaskAuthorizationPolicyView{}, err
	}
	if err := change.Validate(); err != nil {
		return TaskAuthorizationPolicyView{}, err
	}
	project, repo, err := s.taskModeGovernance(ctx, actor, pid)
	if err != nil {
		return TaskAuthorizationPolicyView{}, err
	}
	hash := hashCommand("task_authorization_policy.change", struct {
		ProjectID uuid.UUID
		Change    TaskAuthorizationPolicyChange
	}{pid, change})
	return repo.ChangeTaskAuthorizationPolicy(ctx, actor, project, change, meta, hash)
}
