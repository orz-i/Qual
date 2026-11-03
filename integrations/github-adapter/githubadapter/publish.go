package githubadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Target struct {
	ProjectID  string `json:"project_id"`
	WorkItemID string `json:"work_item_id"`
	ThreadID   string `json:"thread_id"`
}

// Mapping never authorizes creation of WorkItems or Thread Links. It records
// an explicit, pre-existing association approved by a legitimate operator.
// One delivery maps to at most ONE WorkItem (Goose17 Event natural identity).
type Mapping struct {
	RepositoryID   int64             `json:"repository_id"`
	RepositoryName string            `json:"repository_name"`
	Links          map[string]Target `json:"links"`
}

func ReadMapping(path string, repo Repository) (Mapping, error) {
	if path == "" || !filepath.IsAbs(path) {
		return Mapping{}, ErrInvalid
	}
	data, err := trustedFile(path)
	if err != nil {
		return Mapping{}, err
	}
	var m Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		return Mapping{}, ErrInvalid
	}
	if m.RepositoryID != repo.ID || m.RepositoryName != repo.FullName {
		return Mapping{}, ErrRepo
	}
	if len(m.Links) > 500 {
		return Mapping{}, ErrInvalid
	}
	for key, target := range m.Links {
		if !strings.HasPrefix(key, repo.FullName+"/pull/") &&
			!strings.HasPrefix(key, repo.FullName+"/issues/") {
			return Mapping{}, ErrInvalid
		}
		number := key[strings.LastIndexByte(key, '/')+1:]
		n, err := strconv.ParseUint(number, 10, 32)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != number {
			return Mapping{}, ErrInvalid
		}
		for _, id := range []string{target.ProjectID, target.WorkItemID, target.ThreadID} {
			if !deliveryIDRE.MatchString(id) {
				return Mapping{}, ErrInvalid
			}
		}
	}
	return m, nil
}

func QualEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		u.Path != "" && u.Path != "/" {
		return nil, ErrInvalid
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return nil, ErrInvalid
	}
	port := u.Port()
	if port == "" {
		return nil, ErrInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 {
		return nil, ErrInvalid
	}
	return u, nil
}

// Publisher never uses gh CLI or the Human's GitHub PAT. It requires one
// independent Qual Integration Credential with explicit event:write only.
type Publisher struct {
	Spool      *Spool
	Repository Repository
	MapPath    string
	QualURL    string
	Token      string
	Client     *http.Client
	Now        func() time.Time
}

type PublishStats struct {
	Attempted      int `json:"attempted"`
	Delivered      int `json:"delivered"`
	BlockedMapping int `json:"blocked_mapping"`
	BlockedAuth    int `json:"blocked_auth"`
	Conflict       int `json:"conflict"`
	Retryable      int `json:"retryable"`
	Skipped        int `json:"skipped"`
}

func (p Publisher) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p Publisher) validate() (Mapping, *url.URL, error) {
	if p.Spool == nil || p.Token == "" || strings.ContainsAny(p.Token, "\r\n \t") || len(p.Token) > 512 {
		return Mapping{}, nil, ErrInvalid
	}
	u, err := QualEndpoint(p.QualURL)
	if err != nil {
		return Mapping{}, nil, err
	}
	m, err := ReadMapping(p.MapPath, p.Repository)
	return m, u, err
}

func (p Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy: nil, MaxConnsPerHost: 1, DisableKeepAlives: true,
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (p Publisher) deliver(ctx context.Context, base *url.URL, d Delivery, t Target) (int, string, error) {
	body := map[string]any{
		"work_item_id": t.WorkItemID, "thread_id": t.ThreadID,
		"source": "github", "external_event_id": d.DeliveryID,
		"event_type": d.EventType, "occurred_at": d.OccurredAt.Format(time.RFC3339Nano),
		"payload_sha256": d.PayloadSHA256, "summary": d.Summary,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	addr := strings.TrimRight(base.String(), "/") + "/api/v1/projects/" + t.ProjectID + "/external-events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, strings.NewReader(string(raw)))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Idempotency-Key", "github-"+d.DeliveryID)
	response, err := p.client().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return response.StatusCode, "", nil
	}
	// JSON response must be an actual persisted Event, not a fake 201.
	var persisted struct {
		ID              string `json:"id"`
		ProjectID       string `json:"project_id"`
		WorkItemID      string `json:"work_item_id"`
		ThreadID        string `json:"thread_id"`
		Source          string `json:"source"`
		ExternalEventID string `json:"external_event_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8193)).Decode(&persisted); err != nil {
		return 502, "", errors.New("invalid response from Qual")
	}
	if !deliveryIDRE.MatchString(persisted.ID) || persisted.ProjectID != t.ProjectID ||
		persisted.WorkItemID != t.WorkItemID || persisted.ThreadID != t.ThreadID ||
		persisted.Source != "github" || persisted.ExternalEventID != d.DeliveryID {
		return 502, "", errors.New("mismatched persisted Qual Event")
	}
	return response.StatusCode, persisted.ID, nil
}

func (p Publisher) PublishOnce(ctx context.Context) (PublishStats, error) {
	var stats PublishStats
	m, base, err := p.validate()
	if err != nil {
		return stats, err
	}
	err = p.Spool.withLock("worker", func() error {
		jobs, err := p.Spool.Pending()
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if job.State == "conflict" || job.State == "manual_review" ||
				(!job.NextAttemptAt.IsZero() && job.NextAttemptAt.After(p.now())) {
				stats.Skipped++
				continue
			}
			target, found := m.Links[job.ExternalThreadID]
			if !found {
				job.State = "blocked_mapping"
				job.LastErrorCode = "NO_APPROVED_THREAD_WORKITEM_LINK"
				if err := p.Spool.Update(job, false); err != nil {
					return err
				}
				stats.BlockedMapping++
				continue
			}
			job.Attempts++
			status, id, err := p.deliver(ctx, base, job, target)
			job.LastHTTPStatus = status
			stats.Attempted++
			switch {
			case err == nil && status == 201:
				job.DeliveredEventID = id
				job.LastErrorCode = ""
				if err := p.Spool.Update(job, true); err != nil {
					return err
				}
				stats.Delivered++
				continue
			case status == 401 || status == 403:
				job.NextAttemptAt = time.Time{}
				job.State = "blocked_auth"
				job.LastErrorCode = "QUAL_CREDENTIAL_OR_PROJECT_SCOPE_DENIED"
				stats.BlockedAuth++
			case status == 404:
				job.NextAttemptAt = time.Time{}
				job.State = "blocked_mapping"
				job.LastErrorCode = "QUAL_WORK_ITEM_OR_THREAD_LINK_NOT_FOUND"
				stats.BlockedMapping++
			case status == 409:
				job.NextAttemptAt = time.Time{}
				job.State = "conflict"
				job.LastErrorCode = "QUAL_EVENT_IDENTITY_CONFLICT"
				stats.Conflict++
			default:
				job.State = "retry"
				job.LastErrorCode = "QUAL_TRANSIENT_DELIVERY_FAILURE"
				stats.Retryable++
				if job.Attempts >= 6 {
					job.State = "manual_review"
					job.NextAttemptAt = time.Time{}
				} else {
					delay := time.Second * time.Duration(1<<min(job.Attempts, 6))
					job.NextAttemptAt = p.now().Add(delay)
				}
			}
			if err := p.Spool.Update(job, false); err != nil {
				return err
			}
		}
		return nil
	})
	return stats, err
}
