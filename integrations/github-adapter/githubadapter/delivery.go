package githubadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSignature = errors.New("invalid GitHub webhook signature")
	ErrInvalid   = errors.New("invalid GitHub delivery")
	ErrIgnore    = errors.New("unsupported GitHub event or action")
	ErrRepo      = errors.New("GitHub repository is not allowlisted")
	ErrConflict  = errors.New("GitHub delivery content conflicts with immutable receipt")
	ErrFull      = errors.New("bounded delivery spool full")
	ErrSpool     = errors.New("untrusted delivery spool")
)

var deliveryIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const MaxWebhookBytes int64 = 256 * 1024
const MaxPending = 1000
const MaxSummary = 240

// Repository is a pinned independent source identity, not only a display name.
type Repository struct {
	ID        int64
	FullName  string
	InstallID int64
}

// Delivery contains only safe structured metadata, never the raw webhook body,
// title, comments, user message, bearer token, or arbitrary GitHub object.
type Delivery struct {
	DeliveryID       string    `json:"delivery_id"`
	RepositoryID     int64     `json:"repository_id"`
	RepositoryName   string    `json:"repository_name"`
	ThreadKind       string    `json:"thread_kind"`
	ExternalThreadID string    `json:"external_thread_id"`
	EventType        string    `json:"event_type"`
	OccurredAt       time.Time `json:"occurred_at"`
	PayloadSHA256    string    `json:"payload_sha256"`
	Summary          string    `json:"summary"`
	ReceivedAt       time.Time `json:"received_at"`
	State            string    `json:"state"`
	Attempts         int       `json:"attempts"`
	LastHTTPStatus   int       `json:"last_http_status,omitempty"`
	LastErrorCode    string    `json:"last_error_code,omitempty"`
	NextAttemptAt    time.Time `json:"next_attempt_at,omitempty"`
	DeliveredEventID string    `json:"delivered_event_id,omitempty"`
	// Bound BEFORE the first network request. A mapping edit after a timeout
	// must never silently send the same GitHub fact to another Qual target.
	PinnedTarget *Target `json:"pinned_target,omitempty"`
	// A bounded, software-append-only state and manual-requeue trail.
	// It is not an independently authenticated Human approval or an OS
	// immutable ledger when Human/PI share the same macOS UID.
	History []Transition `json:"history,omitempty"`
}

// Transition retains previous retry and manual-review facts after the
// delivery's current state changes. Reason codes are controlled, not free text.
type Transition struct {
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
	ReasonCode  string    `json:"reason_code,omitempty"`
	OperatorUID int       `json:"operator_uid,omitempty"`
}

func (d Delivery) stableIdentity() string {
	return d.PayloadSHA256 + "|" + d.EventType + "|" + d.ExternalThreadID + "|" + d.OccurredAt.Format(time.RFC3339Nano)
}

type githubPayload struct {
	Action     string `json:"action"`
	Number     int64  `json:"number"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	PullRequest struct {
		Number    int64     `json:"number"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"pull_request"`
	Issue struct {
		Number    int64     `json:"number"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"issue"`
	Review struct {
		SubmittedAt time.Time `json:"submitted_at"`
	} `json:"review"`
}

var allowedActions = map[string]map[string]bool{
	"pull_request": {
		"opened": true, "reopened": true, "edited": true,
		"synchronize": true, "closed": true, "ready_for_review": true,
	},
	"issues":              {"opened": true, "reopened": true, "edited": true, "closed": true},
	"pull_request_review": {"submitted": true, "dismissed": true, "edited": true},
}

func ParseDelivery(body []byte, deliveryID, event string, repo Repository, now time.Time) (Delivery, error) {
	var zero Delivery
	deliveryID = strings.ToLower(deliveryID)
	if len(body) == 0 || int64(len(body)) > MaxWebhookBytes ||
		!deliveryIDRE.MatchString(deliveryID) || repo.ID <= 0 ||
		repo.FullName == "" || len(repo.FullName) > 128 {
		return zero, ErrInvalid
	}
	var raw githubPayload
	if err := json.Unmarshal(body, &raw); err != nil {
		return zero, ErrInvalid
	}
	if raw.Repository.ID != repo.ID || raw.Repository.FullName != repo.FullName ||
		(repo.InstallID > 0 && raw.Installation.ID != repo.InstallID) {
		return zero, ErrRepo
	}
	if event == "ping" {
		return zero, ErrIgnore
	}
	if !allowedActions[event][raw.Action] {
		return zero, ErrIgnore
	}
	number := raw.Number
	var occurred time.Time
	var kind, segment string
	switch event {
	case "pull_request", "pull_request_review":
		if raw.PullRequest.Number <= 0 || (number != 0 && number != raw.PullRequest.Number) {
			return zero, ErrInvalid
		}
		number = raw.PullRequest.Number
		kind, segment = "pull_request", "pull"
		occurred = raw.PullRequest.UpdatedAt
		if event == "pull_request_review" {
			occurred = raw.Review.SubmittedAt
		}
	case "issues":
		if raw.Issue.Number <= 0 || (number != 0 && number != raw.Issue.Number) {
			return zero, ErrInvalid
		}
		number = raw.Issue.Number
		kind, segment = "issue", "issues"
		occurred = raw.Issue.UpdatedAt
	default:
		return zero, ErrIgnore
	}
	if number < 1 || number > 1000000000 || occurred.IsZero() || occurred.After(now.Add(5*time.Minute)) {
		return zero, ErrInvalid
	}
	parts := strings.Split(repo.FullName, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(repo.FullName, "?#\\ \n\r\t") {
		return zero, ErrInvalid
	}
	kindLabel := "PR"
	if kind == "issue" {
		kindLabel = "Issue"
	}
	summary := fmt.Sprintf("GitHub %s #%d %s", kindLabel, number, raw.Action)
	if len(summary) > MaxSummary {
		return zero, ErrInvalid
	}
	hash := sha256.Sum256(body)
	return Delivery{
		DeliveryID: deliveryID, RepositoryID: repo.ID, RepositoryName: repo.FullName,
		ThreadKind: kind, ExternalThreadID: repo.FullName + "/" + segment + "/" + strconv.FormatInt(number, 10),
		EventType: event + "." + raw.Action, OccurredAt: occurred.UTC().Truncate(time.Microsecond),
		PayloadSHA256: hex.EncodeToString(hash[:]), Summary: summary, ReceivedAt: now.UTC(), State: "pending",
	}, nil
}
