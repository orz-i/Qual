// Package webhooks manages subscriptions and delivers already persisted
// Outbox events. It never executes Agents, tests or CI.
package webhooks

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	identity "qual/internal/identity/application"
	role "qual/internal/identity/domain"
)

var (
	ErrValidation  = errors.New("invalid webhook configuration")
	ErrUnavailable = errors.New("webhook encryption or delivery not configured")
	ErrNotFound    = errors.New("webhook subscription not found")
	ErrConflict    = errors.New("webhook idempotency conflict")
)

type Config struct {
	Key               [32]byte
	HasKey            bool
	AllowedHosts      []string
	AllowLoopbackTest bool // only integration tests, never production configuration
}

func FromEnv() (Config, error) {
	var cfg Config
	value := os.Getenv("QUAL_WEBHOOK_ENCRYPTION_KEY")
	if value != "" {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != 32 {
			return cfg, errors.New("QUAL_WEBHOOK_ENCRYPTION_KEY must be exactly 32 random bytes in hex")
		}
		copy(cfg.Key[:], raw)
		cfg.HasKey = true
	}
	for _, name := range strings.Split(os.Getenv("QUAL_WEBHOOK_ALLOWED_HOSTS"), ",") {
		host := strings.ToLower(strings.TrimSpace(name))
		if host != "" {
			cfg.AllowedHosts = append(cfg.AllowedHosts, host)
		}
	}
	return cfg, nil
}
func (c Config) ValidateEndpoint(raw string) error {
	if len(raw) < 12 || len(raw) > 2048 {
		return ErrValidation
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return ErrValidation
	}
	host := strings.ToLower(u.Hostname())
	if c.AllowLoopbackTest && u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
		return nil
	}
	if u.Scheme != "https" || !slices.Contains(c.AllowedHosts, host) || net.ParseIP(host) != nil || (u.Port() != "" && u.Port() != "443") {
		return ErrValidation
	}
	// TLS hostnames, not arbitrary IP literals or internal single-label names.
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ErrValidation
	}
	return nil
}

type Subscription struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Endpoint   string     `json:"endpoint"`
	EventTypes []string   `json:"event_types"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at"`
}
type Registration struct {
	Subscription
	Secret string `json:"secret,omitempty"` // shown only once, NEVER stored in response_json
}
type Delivery struct {
	ID             uuid.UUID  `json:"id"`
	EventID        uuid.UUID  `json:"event_id"`
	SubscriptionID uuid.UUID  `json:"subscription_id"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	LastHTTPStatus *int       `json:"last_http_status"`
	LastErrorCode  *string    `json:"last_error_code"`
	DeliveredAt    *time.Time `json:"delivered_at"`
}
type Meta struct {
	RequestID      string
	IdempotencyKey string
}

func (m Meta) valid() error {
	if m.RequestID == "" || len(m.IdempotencyKey) < 8 || len(m.IdempotencyKey) > 128 {
		return ErrValidation
	}
	return nil
}

type Manager struct {
	Pool     *pgxpool.Pool
	Identity identity.Authenticator
	Config   Config
}

func (m Manager) authorize(ctx context.Context, a identity.Actor, pid uuid.UUID) (identity.Project, error) {
	return m.Identity.AuthorizeProject(ctx, a, pid, role.WebhookManage)
}
func (m Manager) sealedSecret() (string, []byte, []byte, error) {
	if !m.Config.HasKey {
		return "", nil, nil, ErrUnavailable
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", nil, nil, err
	}
	plaintext := "whsec_" + hex.EncodeToString(secret[:])
	block, err := aes.NewCipher(m.Config.Key[:])
	if err != nil {
		return "", nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return plaintext, nonce, ciphertext, nil
}
func (m Manager) unseal(nonce, ciphertext []byte) (string, error) {
	if !m.Config.HasKey {
		return "", ErrUnavailable
	}
	block, err := aes.NewCipher(m.Config.Key[:])
	if err != nil {
		return "", ErrUnavailable
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrUnavailable
	}
	raw, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrUnavailable
	}
	return string(raw), nil
}

var eventName = regexp.MustCompile(`^([a-z][a-z0-9_.]{0,79}|\*)$`)

func validEvents(events []string) bool {
	if len(events) < 1 || len(events) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range events {
		if !eventName.MatchString(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func hashCommand(op string, payload any) [32]byte {
	raw, _ := json.Marshal(struct {
		Op   string
		Data any
	}{op, payload})
	return sha256.Sum256(raw)
}
func (m Manager) Register(ctx context.Context, a identity.Actor, pid uuid.UUID, endpoint string, events []string, meta Meta) (Registration, error) {
	if err := meta.valid(); err != nil {
		return Registration{}, err
	}
	if !validEvents(events) || m.Config.ValidateEndpoint(endpoint) != nil {
		return Registration{}, ErrValidation
	}
	if !m.Config.HasKey {
		return Registration{}, ErrUnavailable
	}
	p, err := m.authorize(ctx, a, pid)
	if err != nil {
		return Registration{}, err
	}
	_ = p
	requestHash := hashCommand("webhook.register", struct {
		Endpoint string
		Events   []string
	}{endpoint, events})
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return Registration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		pid, a.PrincipalID, "webhook.register", meta.IdempotencyKey, requestHash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored, raw []byte
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json FROM qual.idempotency_records
 WHERE project_id=$1 AND actor_id=$2 AND operation='webhook.register' AND request_key=$3 FOR UPDATE`,
			pid, a.PrincipalID, meta.IdempotencyKey).Scan(&stored, &raw)
		if err != nil {
			return Registration{}, err
		}
		if !bytes.Equal(stored, requestHash[:]) {
			return Registration{}, ErrConflict
		}
		var sub Subscription
		if err := json.Unmarshal(raw, &sub); err != nil {
			return Registration{}, err
		}
		return Registration{Subscription: sub}, tx.Commit(ctx)
	}
	secret, nonce, ciphertext, err := m.sealedSecret()
	if err != nil {
		return Registration{}, err
	}
	sub := Subscription{ID: uuid.New(), ProjectID: pid, Endpoint: endpoint, EventTypes: events}
	err = tx.QueryRow(ctx, `INSERT INTO qual.webhook_subscriptions
 (id,project_id,endpoint,event_types,secret_nonce,secret_ciphertext,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
		sub.ID, pid, endpoint, events, nonce, ciphertext, a.PrincipalID).Scan(&sub.CreatedAt)
	if err != nil {
		return Registration{}, err
	}
	raw, _ := json.Marshal(sub)
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=200
 WHERE project_id=$2 AND actor_id=$3 AND operation='webhook.register' AND request_key=$4`,
		raw, pid, a.PrincipalID, meta.IdempotencyKey)
	if err != nil {
		return Registration{}, err
	}
	if err := emit(ctx, tx, a, p, meta, "webhook.registered", sub.ID, map[string]any{"event_types": events}); err != nil {
		return Registration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Registration{}, err
	}
	return Registration{Subscription: sub, Secret: secret}, nil
}
func (m Manager) Disable(ctx context.Context, a identity.Actor, pid, id uuid.UUID, meta Meta) error {
	if err := meta.valid(); err != nil {
		return err
	}
	p, err := m.authorize(ctx, a, pid)
	if err != nil {
		return err
	}
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	hash := hashCommand("webhook.disable", id)
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
 VALUES($1,$2,'webhook.disable',$3,$4) ON CONFLICT DO NOTHING RETURNING request_hash`,
		pid, a.PrincipalID, meta.IdempotencyKey, hash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored []byte
		if err := tx.QueryRow(ctx, `SELECT request_hash FROM qual.idempotency_records
    WHERE project_id=$1 AND actor_id=$2 AND operation='webhook.disable' AND request_key=$3 FOR UPDATE`,
			pid, a.PrincipalID, meta.IdempotencyKey).Scan(&stored); err != nil {
			return err
		}
		if !bytes.Equal(stored, hash[:]) {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `UPDATE qual.webhook_subscriptions SET disabled_at=now()
 WHERE project_id=$1 AND id=$2 AND disabled_at IS NULL`, pid, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE qual.webhook_deliveries SET status='cancelled',lease_until=NULL
 WHERE project_id=$1 AND subscription_id=$2 AND status IN ('queued','retrying')`, pid, id)
	if err != nil {
		return err
	}
	if err := emit(ctx, tx, a, p, meta, "webhook.disabled", id, map[string]any{"disabled": true}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=200
 WHERE project_id=$2 AND actor_id=$3 AND operation='webhook.disable' AND request_key=$4`,
		map[string]any{"id": id, "disabled": true}, pid, a.PrincipalID, meta.IdempotencyKey)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (m Manager) List(ctx context.Context, a identity.Actor, pid uuid.UUID) ([]Subscription, error) {
	if _, err := m.authorize(ctx, a, pid); err != nil {
		return nil, err
	}
	rows, err := m.Pool.Query(ctx, `SELECT id,project_id,endpoint,event_types,created_at,disabled_at
 FROM qual.webhook_subscriptions WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Endpoint, &s.EventTypes, &s.CreatedAt, &s.DisabledAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (m Manager) Deliveries(ctx context.Context, a identity.Actor, pid uuid.UUID) ([]Delivery, error) {
	if _, err := m.authorize(ctx, a, pid); err != nil {
		return nil, err
	}
	rows, err := m.Pool.Query(ctx, `SELECT id,outbox_event_id,subscription_id,status,attempt_count,
 next_attempt_at,last_http_status,last_error_code,delivered_at
 FROM qual.webhook_deliveries WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.NextAttemptAt,
			&d.LastHTTPStatus, &d.LastErrorCode, &d.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func emit(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project, meta Meta, op string, id uuid.UUID, info map[string]any) error {
	raw, _ := json.Marshal(info)
	audit := uuid.New()
	_, err := tx.Exec(ctx, `INSERT INTO qual.audit_records
 (id,workspace_id,project_id,actor_id,credential_id,request_id,operation,entity_id,new_version,diff)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,1,$9)`, audit, p.WorkspaceID, p.ID, a.PrincipalID, a.CredentialID, meta.RequestID, op, id, raw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records(id,project_id,audit_id,actor_id,operation,entity_id)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, audit, a.PrincipalID, op, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events(id,project_id,audit_id,event_type,aggregate_id,payload)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, audit, op, id, raw)
	return err
}
