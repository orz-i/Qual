package application

import (
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// TaskActionPermission names the legacy project's corresponding operation.
// In task_scoped mode only an explicit Project Deny for this permission blocks
// the task-derived capability: the old broad credential scope does NOT grant
// or replace a per-WorkItem relationship.
func TaskActionPermission(action domain.TaskAction) (idomain.Permission, bool) {
	switch action {
	case domain.TaskClaim, domain.TaskRelease, domain.TaskReassign:
		return idomain.WorkClaim, true
	case domain.TaskUpdate, domain.TaskComment, domain.TaskStart, domain.TaskSubmitReview,
		domain.TaskCancel, domain.TaskRecordExecutor:
		return idomain.WorkWrite, true
	case domain.TaskClaimReview, domain.TaskRecordReview, domain.TaskReturnForWork,
		domain.TaskReleaseReview, domain.TaskCompleteIndependent, domain.TaskCompleteOwner:
		return idomain.WorkReview, true
	case domain.TaskReportTest, domain.TaskRecordTester:
		return idomain.TestWrite, true
	case domain.TaskReadTest:
		return idomain.TestRead, true
	case domain.TaskReadEvidence:
		return idomain.ArtifactRead, true
	case domain.TaskAttachEvidence:
		return idomain.ArtifactWrite, true
	default:
		return "", false
	}
}
