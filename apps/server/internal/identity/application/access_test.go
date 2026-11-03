package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"qual/internal/identity/domain"
)

type fakeProjectAuthorizer struct {
	permission domain.Permission
	project    Project
	err        error
}

func (f *fakeProjectAuthorizer) Authenticate(context.Context, string) (Actor, error) {
	return Actor{}, nil
}
func (f *fakeProjectAuthorizer) AuthorizeProject(_ context.Context, _ Actor, _ uuid.UUID, p domain.Permission) (Project, error) {
	f.permission = p
	return f.project, f.err
}

type fakeAccessRepo struct {
	member  ProjectMember
	changed bool
}

func (r *fakeAccessRepo) Member(_ context.Context, _ Actor, _ uuid.UUID) (ProjectMember, error) {
	return r.member, nil
}
func (r *fakeAccessRepo) ListMembers(_ context.Context, _ uuid.UUID) ([]ProjectMember, error) {
	return []ProjectMember{r.member}, nil
}
func (r *fakeAccessRepo) ResolvePrincipals(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]ProjectPrincipalSummary, error) {
	if len(ids) == 0 {
		return nil, ErrAccessValidation
	}
	return []ProjectPrincipalSummary{{ID: ids[0], DisplayName: "Authorized Agent", Kind: domain.Agent}}, nil
}
func (r *fakeAccessRepo) Change(_ context.Context, _ Actor, _ Project, _ PermissionChange, _, _ string, _ [32]byte) (PermissionSetting, error) {
	r.changed = true
	return PermissionSetting{Permission: domain.WorkReview, Effect: domain.Allow, Version: 1}, nil
}
func TestSelfAccessScopeAndProjectOverride(t *testing.T) {
	id := uuid.New()
	repo := &fakeAccessRepo{member: ProjectMember{PrincipalID: id, Kind: domain.Agent, Role: domain.ProjectMember, Permissions: []PermissionSetting{
		{Permission: domain.WorkRead, Effect: domain.Inherit},
		{Permission: domain.WorkReview, Effect: domain.Allow},
		{Permission: domain.AuditRead, Effect: domain.Deny},
	}}}
	s := AccessService{Repository: repo}
	actor := Actor{PrincipalID: id, Type: domain.Agent, Scopes: []string{"work:read", "audit:read"}}
	access, err := s.Self(context.Background(), actor, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(access.EffectivePermissions) != 1 || access.EffectivePermissions[0] != domain.WorkRead {
		t.Fatalf("unexpected access: %+v", access.EffectivePermissions)
	}
	actor.Scopes = append(actor.Scopes, string(domain.WorkReview))
	access, err = s.Self(context.Background(), actor, uuid.New())
	if err != nil || len(access.EffectivePermissions) != 2 {
		t.Fatalf("explicit allow plus credential not applied: %+v %v", access, err)
	}
}
func TestPrincipalSummariesRequireWorkReadAndBoundedIDs(t *testing.T) {
	ctx := context.Background()
	authorizer := &fakeProjectAuthorizer{project: Project{ID: uuid.New()}}
	service := AccessService{Identity: authorizer, Repository: &fakeAccessRepo{}}
	actor := Actor{PrincipalID: uuid.New(), Type: domain.Human}
	principal := uuid.New()
	result, err := service.Principals(ctx, actor, authorizer.project.ID, []uuid.UUID{principal, principal})
	if err != nil || len(result) != 1 || result[0].Kind != domain.Agent {
		t.Fatalf("unexpected summary %v %v", result, err)
	}
	if authorizer.permission != domain.WorkRead {
		t.Fatalf("must require work:read, checked %s", authorizer.permission)
	}
	for _, ids := range [][]uuid.UUID{nil, {uuid.Nil}, make([]uuid.UUID, 51)} {
		if _, err := service.Principals(ctx, actor, authorizer.project.ID, ids); !errors.Is(err, ErrAccessValidation) {
			t.Fatalf("accepted bad IDs: %v", err)
		}
	}
	authorizer.err = ErrForbidden
	if _, err := service.Principals(ctx, actor, authorizer.project.ID, []uuid.UUID{principal}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorization failure bypassed: %v", err)
	}
}

func TestChangeRequiresPermissionManagerAndForbidsSelf(t *testing.T) {
	id := uuid.New()
	auth := &fakeProjectAuthorizer{project: Project{ID: uuid.New()}}
	repo := &fakeAccessRepo{}
	s := AccessService{Identity: auth, Repository: repo}
	actor := Actor{PrincipalID: id, Type: domain.Agent}
	c := PermissionChange{PrincipalID: id, Permission: domain.WorkReview, Effect: domain.Allow, ExpectedVersion: 0, Reason: "explicit delegation"}
	if _, err := s.Change(context.Background(), actor, auth.project.ID, c, "request-123", "key-long-enough"); !errors.Is(err, ErrAccessLocked) {
		t.Fatalf("self-modification allowed: %v", err)
	}
	c.PrincipalID = uuid.New()
	if _, err := s.Change(context.Background(), actor, auth.project.ID, c, "request-123", "key-long-enough"); err != nil {
		t.Fatal(err)
	}
	if auth.permission != domain.AccessManage || !repo.changed {
		t.Fatal("access:manage was not checked")
	}
	c.Reason = " "
	if _, err := s.Change(context.Background(), actor, auth.project.ID, c, "request-123", "key-long-enough"); !errors.Is(err, ErrAccessValidation) {
		t.Fatal("invalid reason was accepted")
	}
}
