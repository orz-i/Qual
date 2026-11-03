package domain

import "testing"

func TestPrincipalSpecificPermissionOverrides(t *testing.T) {
	cases := []struct {
		name    string
		kind    PrincipalType
		role    Role
		scope   Permission
		effect  Effect
		granted bool
	}{
		{"Agent A may autonomously review after explicit grant", Agent, ProjectMember, WorkReview, Allow, true},
		{"Agent B may audit after explicit grant", Agent, ProjectViewer, AuditRead, Allow, true},
		{"Agent B cannot write on audit token", Agent, ProjectViewer, WorkWrite, Inherit, false},
		{"Run reporting is not inherited by Agent", Agent, ProjectMember, RunReport, Inherit, false},
		{"Run reporting requires explicit Agent allowance", Agent, ProjectMember, RunReport, Allow, true},
		{"Event writing not inherited by any Agent", Agent, ProjectMember, EventWrite, Inherit, false},
		{"Event writing needs explicit Agent allowance", Agent, ProjectMember, EventWrite, Allow, true},
		{"Event writing denied by default for Human Admin", Human, WorkspaceAdmin, EventWrite, Inherit, false},
		{"Event writing requires explicit Integration grant", Integration, ProjectMember, EventWrite, Allow, true},
		{"Run reporting is not inherited by Human admin", Human, WorkspaceAdmin, RunReport, Inherit, false},
		{"Run reporting requires explicit Human allowance", Human, WorkspaceAdmin, RunReport, Allow, true},
		{"Integration cannot submit Run without grant", Integration, ProjectMember, RunReport, Inherit, false},
		{"Integration can report with explicit grant", Integration, ProjectMember, RunReport, Allow, true},
		{"Human member may review after explicit grant", Human, ProjectMember, WorkReview, Allow, true},
		{"Human maintainer can explicitly deny own work write", Human, ProjectMaintainer, WorkWrite, Deny, false},
		{"Agent's inherited review remains forbidden", Agent, ProjectMaintainer, WorkReview, Inherit, false},
		{"Agent manager can manage grants only if explicit", Agent, ProjectMember, AccessManage, Allow, true},
		{"Workspace Admin manager cannot lose management rights", Human, WorkspaceAdmin, AccessManage, Deny, true},
		{"Integration cannot elevate review even via malicious DB grant", Integration, ProjectMember, WorkReview, Allow, false},
		{"Integration cannot manage grants", Integration, ProjectMember, AccessManage, Allow, false},
		{"Integration cannot manage webhook", Integration, ProjectMember, WebhookManage, Allow, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanWithEffect(tc.kind, tc.role, []string{string(tc.scope)}, tc.scope, tc.effect)
			if got != tc.granted {
				t.Fatalf("got %v want %v", got, tc.granted)
			}
			if CanWithEffect(tc.kind, tc.role, nil, tc.scope, Allow) {
				t.Fatal("missing credential scope must deny even with project allow")
			}
			if CanWithEffect(tc.kind, tc.role, []string{"work:read"}, tc.scope, Effect("invalid")) {
				t.Fatal("unknown effect must deny")
			}
		})
	}
	if ValidPermission("made:up") || Grantable(Agent, "made:up") {
		t.Fatal("arbitrary scopes not allowed")
	}
	if !ValidPermission(AccessManage) || !ValidEffect(Inherit) {
		t.Fatal("access policy not registered")
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	tests := []struct {
		name    string
		kind    PrincipalType
		role    Role
		scope   Permission
		allowed bool
	}{
		{"admin can review", Human, WorkspaceAdmin, WorkReview, true},
		{"human maintainer can review", Human, ProjectMaintainer, WorkReview, true},
		{"human member cannot review", Human, ProjectMember, WorkReview, false},
		{"agent cannot review", Agent, ProjectMaintainer, WorkReview, false},
		{"agent can claim", Agent, ProjectMember, WorkClaim, true},
		{"agent cannot be admin", Agent, WorkspaceAdmin, AuditRead, false},
		{"viewer read", Human, ProjectViewer, WorkRead, true},
		{"viewer cannot write", Human, ProjectViewer, WorkWrite, false},
		{"integration cannot write tasks", Integration, ProjectMaintainer, WorkWrite, false},
		{"integration can upload results", Integration, ProjectMember, TestWrite, true},
		{"integration cannot edit cases without a role", Integration, ProjectViewer, TestWrite, false},
		{"agent can write tests", Agent, ProjectMember, TestWrite, true},
		{"viewer reads tests", Human, ProjectViewer, TestRead, true},
		{"viewer cannot write tests", Human, ProjectViewer, TestWrite, false},
		{"Human maintainer exports", Human, ProjectMaintainer, ArchiveExport, true},
		{"Human maintainer manages webhook", Human, ProjectMaintainer, WebhookManage, true},
		{"Human member cannot export", Human, ProjectMember, ArchiveExport, false},
		{"Agent cannot export", Agent, ProjectMaintainer, ArchiveExport, false},
		{"Agent can upload evidence", Agent, ProjectMember, ArtifactWrite, true},
		{"Agent cannot report external run by default", Agent, ProjectMember, RunReport, false},
		{"Agent cannot ingest events by default", Agent, ProjectMember, EventWrite, false},
		{"Viewer can read artifact", Human, ProjectViewer, ArtifactRead, true},
		{"Viewer cannot upload artifact", Human, ProjectViewer, ArtifactWrite, false},
		{"Integration can upload test evidence", Integration, ProjectMember, ArtifactWrite, true},
		{"Integration cannot manage webhook", Integration, ProjectMember, WebhookManage, false},
		{"invalid principal kind", "unknown", WorkspaceAdmin, WorkRead, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Can(tc.kind, tc.role, []string{string(tc.scope)}, tc.scope); got != tc.allowed {
				t.Fatalf("Can(%q,%q,%q) = %v, want %v", tc.kind, tc.role, tc.scope, got, tc.allowed)
			}
			if got := Can(tc.kind, tc.role, nil, tc.scope); got {
				t.Fatal("credential missing scope must always deny")
			}
		})
	}
}
