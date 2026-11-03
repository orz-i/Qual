package postgres

import (
	"context"

	"github.com/google/uuid"
	"qual/internal/identity/application"
)

// ResolvePrincipals intentionally cannot enumerate the project: it receives a
// bounded list of already-known ids and returns only active same-project
// members or workspace admins. Revoked/foreign identities are omitted.
func (r Repository) ResolvePrincipals(ctx context.Context, projectID uuid.UUID, ids []uuid.UUID) ([]application.ProjectPrincipalSummary, error) {
	rows, err := r.Pool.Query(ctx, `SELECT DISTINCT p.id,p.display_name,p.kind
 FROM qual.projects project
 JOIN qual.principals p ON p.workspace_id=project.workspace_id AND p.disabled_at IS NULL
 JOIN qual.memberships m ON m.workspace_id=project.workspace_id AND m.principal_id=p.id
 WHERE project.id=$1 AND p.id=ANY($2::uuid[])
 AND (m.project_id=project.id OR (m.project_id IS NULL AND m.role='workspace_admin'))
 ORDER BY p.id LIMIT 50`, projectID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]application.ProjectPrincipalSummary, 0)
	for rows.Next() {
		var info application.ProjectPrincipalSummary
		if err := rows.Scan(&info.ID, &info.DisplayName, &info.Kind); err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, rows.Err()
}
