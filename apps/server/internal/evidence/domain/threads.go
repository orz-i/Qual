package domain

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"
)

// Thread is an immutable source identity, never an Agent session or executable job.
type Thread struct {
	ID               uuid.UUID `json:"id"`
	ProjectID        uuid.UUID `json:"project_id"`
	Provider         string    `json:"provider"`
	ThreadKind       string    `json:"thread_kind"`
	ExternalThreadID string    `json:"external_thread_id"`
	URL              string    `json:"url"`
	CreatedBy        uuid.UUID `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
}

type ThreadLinkInput struct {
	Provider          string     `json:"provider"`
	ThreadKind        string     `json:"thread_kind"`
	ExternalThreadID  string     `json:"external_thread_id"`
	URL               string     `json:"url,omitempty"`
	SourceReferenceID *uuid.UUID `json:"source_reference_id,omitempty"`
	ExpectedVersion   int64      `json:"expected_version"`
}

var (
	threadSlug   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,59}$`)
	githubNumber = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// A source identity must include its tenant, repository or channel context.
// Bare numeric PR/Issue IDs are ambiguous across GitHub repositories.
func validThreadExternalID(provider, kind, externalID string) bool {
	if len(externalID) < 3 || len(externalID) > 240 || strings.TrimSpace(externalID) != externalID {
		return false
	}
	parts := strings.Split(externalID, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#\\") {
			return false
		}
		for _, r := range part {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return false
			}
		}
	}
	if provider == "github" && (kind == "pull_request" || kind == "issue") {
		segment := "pull"
		if kind == "issue" {
			segment = "issues"
		}
		return len(parts) == 4 && parts[2] == segment && githubNumber.MatchString(parts[3])
	}
	return true
}

func ValidThreadURL(raw string) bool {
	if raw == "" {
		return true // Some external systems have a stable ID but no public link.
	}
	if len(raw) > 2048 || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, " \r\n\t") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Hostname() == "" {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	// Validate using local ICANN Public Suffix List data, not DNS or HTTP.
	// In particular, .corp, .lan, .onion and .example are not public TLDs.
	// Numeric host shorthands (e.g. 127.1) may resolve as loopback in browsers.
	labels := strings.Split(host, ".")
	tld := labels[len(labels)-1]
	if len(tld) < 2 || !strings.ContainsAny(tld, "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	// Private PSL entries (such as github.io) may have a public underlying
	// ICANN TLD; their registrable subdomains remain valid.
	// Some special-use names (notably .onion) occur in the PSL. They
	// remain unsuitable for a publicly routable link even when listed.
	switch tld {
	case "onion", "alt", "i2p", "localhost", "local", "internal",
		"test", "invalid", "example", "corp", "lan", "arpa":
		return false
	}
	if suffix, icann := publicsuffix.PublicSuffix(tld); !icann || suffix != tld {
		return false
	}
	_, err = publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil
}

func (in ThreadLinkInput) Validate() error {
	if !threadSlug.MatchString(in.Provider) || !threadSlug.MatchString(in.ThreadKind) ||
		!validThreadExternalID(in.Provider, in.ThreadKind, in.ExternalThreadID) ||
		in.ExpectedVersion < 1 || !ValidThreadURL(in.URL) {
		return ErrValidation
	}
	if in.SourceReferenceID != nil && *in.SourceReferenceID == uuid.Nil {
		return ErrValidation
	}
	return nil
}

type WorkThreadLink struct {
	ID                uuid.UUID  `json:"id"`
	ProjectID         uuid.UUID  `json:"project_id"`
	ThreadID          uuid.UUID  `json:"thread_id"`
	WorkItemID        uuid.UUID  `json:"work_item_id"`
	SourceReferenceID *uuid.UUID `json:"source_reference_id"`
	CreatedBy         uuid.UUID  `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	RemovedBy         *uuid.UUID `json:"removed_by"`
	RemovedAt         *time.Time `json:"removed_at"`
}

type WorkThreadEntry struct {
	Thread Thread         `json:"thread"`
	Link   WorkThreadLink `json:"link"`
}
type ThreadMutation struct {
	WorkThreadEntry
	WorkItemVersion int64 `json:"work_item_version"`
}
type WorkThreadPage struct {
	Items      []WorkThreadEntry `json:"items"`
	NextCursor *uuid.UUID        `json:"next_cursor"`
}
type LinkedWorkItem struct {
	ID      uuid.UUID `json:"id"`
	Key     string    `json:"key"`
	Status  string    `json:"status"`
	Version int64     `json:"version"`
}
type ThreadWorkItemEntry struct {
	Link     WorkThreadLink `json:"link"`
	WorkItem LinkedWorkItem `json:"work_item"`
}
type ThreadWorkItemPage struct {
	Items      []ThreadWorkItemEntry `json:"items"`
	NextCursor *uuid.UUID            `json:"next_cursor"`
}
