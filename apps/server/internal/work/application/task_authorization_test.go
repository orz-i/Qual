package application

import (
	"testing"

	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

func TestTaskActionPermission(t *testing.T) {
	m := map[domain.TaskAction]idomain.Permission{
		domain.TaskClaim:               idomain.WorkClaim,
		domain.TaskUpdate:              idomain.WorkWrite,
		domain.TaskComment:             idomain.WorkWrite,
		domain.TaskStart:               idomain.WorkWrite,
		domain.TaskSubmitReview:        idomain.WorkWrite,
		domain.TaskCancel:              idomain.WorkWrite,
		domain.TaskRelease:             idomain.WorkClaim,
		domain.TaskReassign:            idomain.WorkClaim,
		domain.TaskReportTest:          idomain.TestWrite,
		domain.TaskAttachEvidence:      idomain.ArtifactWrite,
		domain.TaskRecordExecutor:      idomain.WorkWrite,
		domain.TaskRecordTester:        idomain.TestWrite,
		domain.TaskClaimReview:         idomain.WorkReview,
		domain.TaskRecordReview:        idomain.WorkReview,
		domain.TaskReturnForWork:       idomain.WorkReview,
		domain.TaskReleaseReview:       idomain.WorkReview,
		domain.TaskCompleteIndependent: idomain.WorkReview,
		domain.TaskCompleteOwner:       idomain.WorkReview,
	}
	for action, expected := range m {
		if actual, ok := TaskActionPermission(action); !ok || actual != expected {
			t.Errorf("%s -> %s, %v (want %s)", action, actual, ok, expected)
		}
	}
	for _, action := range []domain.TaskAction{"", "task.record_auditor", "task.manage_project", "task.some_future_action"} {
		if p, ok := TaskActionPermission(action); ok || p != "" {
			t.Errorf("unknown action %q was mapped to project permission %s", action, p)
		}
	}
}
