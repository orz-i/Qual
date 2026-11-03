package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"qual/internal/identity/domain"
)

var (
	ErrAccessValidation  = errors.New("invalid permission change")
	ErrAccessConflict    = errors.New("permission version conflict")
	ErrAccessIdempotency = errors.New("permission idempotency conflict")
	ErrAccessLocked      = errors.New("protected permission target")
)

type PermissionSetting struct {
	Permission       domain.Permission `json:"permission"`
	Effect           domain.Effect     `json:"effect"`
	Version          int64             `json:"version"`
	AllowedByProject bool              `json:"allowed_by_project"`
}
type ProjectMember struct {
	PrincipalID uuid.UUID            `json:"principal_id"`
	DisplayName string               `json:"display_name"`
	Kind        domain.PrincipalType `json:"kind"`
	Role        domain.Role          `json:"role"`
	Permissions []PermissionSetting  `json:"permissions"`
}
type SelfAccess struct {
	ProjectID            uuid.UUID            `json:"project_id"`
	PrincipalID          uuid.UUID            `json:"principal_id"`
	Kind                 domain.PrincipalType `json:"kind"`
	Role                 domain.Role          `json:"role"`
	CredentialScopes     []string             `json:"credential_scopes"`
	EffectivePermissions []domain.Permission  `json:"effective_permissions"`
}

// ProjectPrincipalSummary contains only identities needed to label existing
// project work facts; no role, authorization or credential metadata is exposed.
type ProjectPrincipalSummary struct {
	ID          uuid.UUID            `json:"id"`
	DisplayName string               `json:"display_name"`
	Kind        domain.PrincipalType `json:"kind"`
}

type PermissionChange struct {
	PrincipalID     uuid.UUID         `json:"principal_id"`
	Permission      domain.Permission `json:"permission"`
	Effect          domain.Effect     `json:"effect"`
	ExpectedVersion int64             `json:"expected_version"`
	Reason          string            `json:"reason"`
}

func (c PermissionChange) Validate() error {
	if c.PrincipalID == uuid.Nil || !domain.ValidPermission(c.Permission) || !domain.ValidEffect(c.Effect) ||
		c.ExpectedVersion < 0 || utf8.RuneCountInString(c.Reason) < 3 || utf8.RuneCountInString(c.Reason) > 500 ||
		strings.TrimSpace(c.Reason) != c.Reason {
		return ErrAccessValidation
	}
	return nil
}

type PermissionHistory struct {
	ID          uuid.UUID         `json:"id"`
	ProjectID   uuid.UUID         `json:"project_id"`
	PrincipalID uuid.UUID         `json:"principal_id"`
	Permission  domain.Permission `json:"permission"`
	From        domain.Effect     `json:"before_effect"`
	To          domain.Effect     `json:"after_effect"`
	OldVersion  int64             `json:"old_version"`
	NewVersion  int64             `json:"new_version"`
	ChangedBy   uuid.UUID         `json:"changed_by"`
	RequestID   string            `json:"request_id"`
}
type AccessRepository interface {
	ListMembers(ctx context.Context, projectID uuid.UUID) ([]ProjectMember, error)
	ResolvePrincipals(ctx context.Context, projectID uuid.UUID, ids []uuid.UUID) ([]ProjectPrincipalSummary, error)
	Member(ctx context.Context, actor Actor, projectID uuid.UUID) (ProjectMember, error)
	Change(ctx context.Context, actor Actor, project Project, c PermissionChange, requestID, idem string, hash [32]byte) (PermissionSetting, error)
}
type AccessService struct {
	Identity   Authenticator
	Repository AccessRepository
}

// Principals returns only the requested active member summaries and is
// authorized for work:read, not the permission-management capability.
func (s AccessService) Principals(ctx context.Context, actor Actor, pid uuid.UUID, ids []uuid.UUID) ([]ProjectPrincipalSummary, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, domain.WorkRead); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > 50 {
		return nil, ErrAccessValidation
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, ErrAccessValidation
		}
		if _, exists := seen[id]; !exists {
			unique = append(unique, id)
			seen[id] = struct{}{}
		}
	}
	return s.Repository.ResolvePrincipals(ctx, pid, unique)
}

func (s AccessService) List(ctx context.Context, actor Actor, pid uuid.UUID) ([]ProjectMember, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, domain.AccessManage); err != nil {
		return nil, err
	}
	return s.Repository.ListMembers(ctx, pid)
}
func (s AccessService) Self(ctx context.Context, actor Actor, pid uuid.UUID) (SelfAccess, error) {
	member, err := s.Repository.Member(ctx, actor, pid)
	if err != nil {
		return SelfAccess{}, err
	}
	access := SelfAccess{ProjectID: pid, PrincipalID: actor.PrincipalID, Kind: actor.Type,
		Role: member.Role, CredentialScopes: append([]string(nil), actor.Scopes...),
		EffectivePermissions: make([]domain.Permission, 0)}
	for _, setting := range member.Permissions {
		if domain.CanWithEffect(actor.Type, member.Role, actor.Scopes, setting.Permission, setting.Effect) {
			access.EffectivePermissions = append(access.EffectivePermissions, setting.Permission)
		}
	}
	return access, nil
}
func (s AccessService) Change(ctx context.Context, actor Actor, pid uuid.UUID, c PermissionChange, requestID, idem string) (PermissionSetting, error) {
	if err := c.Validate(); err != nil {
		return PermissionSetting{}, err
	}
	if requestID == "" || len(idem) < 8 || len(idem) > 128 || strings.TrimSpace(idem) != idem {
		return PermissionSetting{}, ErrAccessValidation
	}
	if actor.PrincipalID == c.PrincipalID {
		return PermissionSetting{}, ErrAccessLocked
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, pid, domain.AccessManage)
	if err != nil {
		return PermissionSetting{}, err
	}
	input, _ := json.Marshal(struct {
		ProjectID uuid.UUID        `json:"project_id"`
		Change    PermissionChange `json:"change"`
	}{pid, c})
	h := sha256.Sum256(input)
	return s.Repository.Change(ctx, actor, project, c, requestID, idem, h)
}
