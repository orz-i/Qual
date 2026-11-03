// Package domain defines identity kinds and project permission defaults.
//
// Identity KIND does not determine whether an Agent can perform a privileged
// operation: an explicit per-project allowance + credential scope can grant it.
// Integration reporters deliberately remain limited to test/evidence ingestion.
package domain

import "slices"

type PrincipalType string
type Role string
type Permission string
type Effect string

const (
	Human             PrincipalType = "human"
	Agent             PrincipalType = "agent"
	Integration       PrincipalType = "integration"
	WorkspaceAdmin    Role          = "workspace_admin"
	ProjectMaintainer Role          = "project_maintainer"
	ProjectMember     Role          = "project_member"
	ProjectViewer     Role          = "project_viewer"

	WorkRead      Permission = "work:read"
	WorkWrite     Permission = "work:write"
	WorkClaim     Permission = "work:claim"
	WorkReview    Permission = "work:review"
	AuditRead     Permission = "audit:read"
	TestRead      Permission = "test:read"
	TestWrite     Permission = "test:write"
	ArtifactRead  Permission = "artifact:read"
	ArtifactWrite Permission = "artifact:write"
	RunReport     Permission = "run:report"
	EventWrite    Permission = "event:write"
	ArchiveExport Permission = "archive:export"
	WebhookManage Permission = "webhook:manage"
	AccessManage  Permission = "access:manage"

	Inherit Effect = "inherit"
	Allow   Effect = "allow"
	Deny    Effect = "deny"
)

var AllPermissions = []Permission{
	WorkRead, WorkWrite, WorkClaim, WorkReview, AuditRead, TestRead,
	TestWrite, ArtifactRead, ArtifactWrite, RunReport, EventWrite, ArchiveExport, WebhookManage, AccessManage,
}
var readOnly = []Permission{WorkRead, TestRead, ArtifactRead}
var workAndTest = []Permission{WorkRead, WorkWrite, WorkClaim, TestRead, TestWrite, ArtifactRead, ArtifactWrite}
var integrationWrite = []Permission{TestRead, TestWrite, ArtifactRead, ArtifactWrite}
var integrationGrantable = []Permission{TestRead, TestWrite, ArtifactRead, ArtifactWrite, RunReport, EventWrite}
var integrationRead = []Permission{TestRead, ArtifactRead}

func ValidPermission(p Permission) bool { return slices.Contains(AllPermissions, p) }
func ValidEffect(e Effect) bool         { return e == Inherit || e == Allow || e == Deny }
func ValidKind(k PrincipalType) bool    { return k == Human || k == Agent || k == Integration }
func ValidRole(r Role) bool {
	return r == WorkspaceAdmin || r == ProjectMaintainer || r == ProjectMember || r == ProjectViewer
}

// Grantable limits reporter-type principals even when a configuration row
// or custom credential is forged. Human and Agent can receive any known ability.
func Grantable(kind PrincipalType, p Permission) bool {
	if !ValidPermission(p) {
		return false
	}
	switch kind {
	case Human, Agent:
		return true
	case Integration:
		return slices.Contains(integrationGrantable, p)
	default:
		return false
	}
}

// DefaultCan keeps previously issued Agent tokens least-privileged. No schema
// migration silently gives existing Agents review/audit/export privileges.
func DefaultCan(kind PrincipalType, role Role, p Permission) bool {
	// P1 reporting is opt-in for every kind, including existing Human admins.
	if p == RunReport || p == EventWrite {
		return false
	}
	if !Grantable(kind, p) || !ValidRole(role) {
		return false
	}
	switch kind {
	case Human:
		switch role {
		case WorkspaceAdmin, ProjectMaintainer:
			return true
		case ProjectMember:
			return slices.Contains(workAndTest, p)
		case ProjectViewer:
			return slices.Contains(readOnly, p)
		}
	case Agent:
		if role == ProjectMaintainer || role == ProjectMember {
			return slices.Contains(workAndTest, p)
		}
		return role == ProjectViewer && slices.Contains(readOnly, p)
	case Integration:
		if role == ProjectMember {
			return slices.Contains(integrationWrite, p)
		}
		return role == ProjectViewer && slices.Contains(integrationRead, p)
	}
	return false
}

// CanWithEffect always requires the credential Scope AND project membership.
// Explicit project Allow/Deny overrides role defaults, except for the protected
// Human Workspace Admin's access:manage permission.
func CanWithEffect(kind PrincipalType, role Role, scopes []string, p Permission, effect Effect) bool {
	if !Grantable(kind, p) || !ValidRole(role) ||
		!slices.Contains(scopes, string(p)) || !ValidEffect(effect) {
		return false
	}
	if kind == Human && role == WorkspaceAdmin && p == AccessManage {
		return true
	}
	switch effect {
	case Allow:
		return true
	case Deny:
		return false
	default:
		return DefaultCan(kind, role, p)
	}
}

// Can evaluates default role policy and credential scopes, for compatibility
// with callers that have not supplied an override (such as unit test setup).
func Can(kind PrincipalType, role Role, scopes []string, p Permission) bool {
	return CanWithEffect(kind, role, scopes, p, Inherit)
}
