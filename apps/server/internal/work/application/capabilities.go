package application

import (
	"context"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	"qual/internal/work/domain"
)

// TaskCapabilities is a short-lived server decision *projection*, never a
// permission token. Work, policy, credential and reviewer facts are read under
// one database transaction. Every actual write re-authorizes independently.
type TaskCapabilities struct {
	SchemaVersion  int                          `json:"schema_version"`
	ProjectMode    domain.TaskAuthorizationMode `json:"project_mode"`
	WorkItemID     uuid.UUID                    `json:"work_item_id"`
	WorkVersion    int64                        `json:"work_version"`
	PolicyVersion  int64                        `json:"policy_version"`
	ReviewMode     domain.ReviewPolicyMode      `json:"review_mode"`
	PolicySource   string                       `json:"policy_source"`
	MyCapacity     []string                     `json:"my_capacity"`
	AllowedActions []domain.TaskAction          `json:"allowed_actions"`
	DeniedReasons  map[domain.TaskAction]string `json:"denied_reasons,omitempty"`
	// A Complete request still verifies a real, selected TestRun or Ready
	// Artifact, a current review decision, and other Done Gate prerequisites.
	VerificationRequired bool `json:"verification_required"`
}

type TaskCapabilitiesRepository interface {
	GetTaskCapabilities(context.Context, identity.Actor, identity.Project, uuid.UUID) (TaskCapabilities, error)
}

func (s Service) Capabilities(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) (TaskCapabilities, error) {
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, "work:read")
	if err != nil {
		return TaskCapabilities{}, err
	}
	repo, ok := s.TaskPolicy.(TaskCapabilitiesRepository)
	if !ok {
		return TaskCapabilities{}, domain.ErrValidation
	}
	return repo.GetTaskCapabilities(ctx, actor, project, itemID)
}
