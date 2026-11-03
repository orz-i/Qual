// Package application defines the identity port consumed by work use cases.
package application

import (
	"context"
	"qual/internal/identity/domain"

	"github.com/google/uuid"
)

type Actor struct {
	PrincipalID  uuid.UUID
	CredentialID uuid.UUID
	WorkspaceID  uuid.UUID
	Type         domain.PrincipalType
	Scopes       []string
}

type Project struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Key         string    `json:"key"`
}

type Authenticator interface {
	Authenticate(ctx context.Context, bearer string) (Actor, error)
	AuthorizeProject(ctx context.Context, actor Actor, projectID uuid.UUID, permission domain.Permission) (Project, error)
}
