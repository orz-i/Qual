package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type pendingDelivery struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	EventID        uuid.UUID
	ProjectID      uuid.UUID
	Endpoint       string
	EventType      string
	AggregateID    uuid.UUID
	CreatedAt      time.Time
	Payload        json.RawMessage
	Nonce          []byte
	Ciphertext     []byte
	Attempts       int
}
type OutgoingEvent struct {
	EventID     uuid.UUID       `json:"event_id"`
	DeliveryID  uuid.UUID       `json:"delivery_id"`
	ProjectID   uuid.UUID       `json:"project_id"`
	EventType   string          `json:"event_type"`
	AggregateID uuid.UUID       `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload"`
}

var blockedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func safeIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range blockedNetworks {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
func (c Config) pinnedClient(ctx context.Context, endpoint string) (*http.Client, error) {
	if err := c.ValidateEndpoint(endpoint); err != nil {
		return nil, ErrValidation
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, ErrValidation
	}
	hostname := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	var selected netip.Addr
	if c.AllowLoopbackTest && (hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1") {
		selected = netip.MustParseAddr("127.0.0.1")
	} else {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("DNS_UNAVAILABLE")
		}
		for _, entry := range resolved {
			ip, ok := netip.AddrFromSlice(entry.IP)
			if !ok || !safeIP(ip) {
				return nil, errors.New("DNS_DENIED")
			}
			if !selected.IsValid() {
				selected = ip.Unmap()
			}
		}
	}
	tr := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:     true,
		MaxConnsPerHost:       1,
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(selected.String(), port))
		},
	}
	return &http.Client{
		Transport: tr, Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
func SignedPayload(secret, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(payload)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
func (m Manager) enqueue(ctx context.Context) error {
	_, err := m.Pool.Exec(ctx, `INSERT INTO qual.webhook_deliveries
 (id,project_id,subscription_id,outbox_event_id)
 SELECT gen_random_uuid(),candidate.project_id,candidate.sub_id,candidate.event_id
 FROM (
  SELECT e.project_id AS project_id,s.id AS sub_id,e.id AS event_id
  FROM qual.outbox_events e
  JOIN qual.webhook_subscriptions s ON s.project_id=e.project_id
   AND s.disabled_at IS NULL AND e.created_at>=s.created_at
   AND (s.event_types @> ARRAY['*']::text[] OR e.event_type=ANY(s.event_types))
  WHERE NOT EXISTS(SELECT 1 FROM qual.webhook_deliveries d
   WHERE d.subscription_id=s.id AND d.outbox_event_id=e.id)
  ORDER BY e.created_at,e.id,s.id LIMIT 100
 ) candidate ON CONFLICT DO NOTHING`)
	return err
}
func (m Manager) claim(ctx context.Context) (pendingDelivery, bool, error) {
	var d pendingDelivery
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return d, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE qual.webhook_deliveries SET status='dead',lease_until=NULL,last_error_code='LEASE_EXHAUSTED'
 WHERE status='processing' AND attempt_count>=5 AND lease_until<now()`)
	if err != nil {
		return d, false, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT d.id FROM qual.webhook_deliveries d
 JOIN qual.webhook_subscriptions s ON s.id=d.subscription_id AND s.disabled_at IS NULL
 WHERE ((d.status IN ('queued','retrying') AND d.next_attempt_at<=now())
   OR (d.status='processing' AND d.lease_until<now()))
 AND d.attempt_count<5
 ORDER BY d.next_attempt_at,d.id
 FOR UPDATE OF d SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	err = tx.QueryRow(ctx, `UPDATE qual.webhook_deliveries SET status='processing',
 attempt_count=attempt_count+1,lease_until=now()+interval '30 seconds'
 WHERE id=$1 RETURNING attempt_count`, id).Scan(&d.Attempts)
	if err != nil {
		return d, false, err
	}
	err = tx.QueryRow(ctx, `SELECT d.id,d.subscription_id,d.outbox_event_id,d.project_id,s.endpoint,
 e.event_type,e.aggregate_id,e.created_at,e.payload,s.secret_nonce,s.secret_ciphertext
 FROM qual.webhook_deliveries d
 JOIN qual.webhook_subscriptions s ON s.id=d.subscription_id
 JOIN qual.outbox_events e ON e.id=d.outbox_event_id
 WHERE d.id=$1`, id).Scan(&d.ID, &d.SubscriptionID, &d.EventID, &d.ProjectID, &d.Endpoint,
		&d.EventType, &d.AggregateID, &d.CreatedAt, &d.Payload, &d.Nonce, &d.Ciphertext)
	if err != nil {
		return d, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return d, false, err
	}
	return d, true, nil
}
func (m Manager) send(ctx context.Context, d pendingDelivery) (int, string) {
	client, err := m.Config.pinnedClient(ctx, d.Endpoint)
	if err != nil {
		return 0, "DESTINATION_DENIED"
	}
	secret, err := m.unseal(d.Nonce, d.Ciphertext)
	if err != nil {
		return 0, "SECRET_UNAVAILABLE"
	}
	body, err := json.Marshal(OutgoingEvent{EventID: d.EventID, DeliveryID: d.ID,
		ProjectID: d.ProjectID, EventType: d.EventType, AggregateID: d.AggregateID,
		OccurredAt: d.CreatedAt, Payload: d.Payload})
	if err != nil {
		return 0, "ENCODING_ERROR"
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, "POST", d.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return 0, "INVALID_DESTINATION"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Qual-Timestamp", stamp)
	req.Header.Set("X-Qual-Signature", SignedPayload(secret, stamp, body))
	req.Header.Set("X-Qual-Event-ID", d.EventID.String())
	req.Header.Set("X-Qual-Delivery-ID", d.ID.String())
	resp, err := client.Do(req)
	if err != nil {
		return 0, "NETWORK_ERROR"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode)
}
func (m Manager) finalize(ctx context.Context, d pendingDelivery, status int, code string) error {
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var newState string
	var retry time.Time
	if code == "" {
		newState = "delivered"
	} else if d.Attempts >= 5 {
		newState = "dead"
	} else {
		newState = "retrying"
		delay := time.Duration(15*(1<<min(d.Attempts-1, 6))) * time.Second
		retry = time.Now().Add(delay)
	}
	_, err = tx.Exec(ctx, `UPDATE qual.webhook_deliveries SET
 status=$1,lease_until=NULL,last_http_status=NULLIF($2,0),
 last_error_code=NULLIF($3,''),delivered_at=CASE WHEN $1='delivered' THEN now() ELSE NULL END,
 next_attempt_at=CASE WHEN $1='retrying' THEN $4 ELSE next_attempt_at END
 WHERE id=$5 AND status='processing' AND attempt_count=$6`,
		newState, status, code, retry, d.ID, d.Attempts)
	if err != nil {
		return err
	}
	if code == "" {
		_, err = tx.Exec(ctx, `UPDATE qual.outbox_events SET delivered_at=now()
 WHERE id=$1 AND delivered_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM qual.webhook_deliveries x
   JOIN qual.webhook_subscriptions s ON s.id=x.subscription_id AND s.disabled_at IS NULL
   WHERE x.outbox_event_id=$1 AND x.status<>'delivered')`, d.EventID)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ProcessOne schedules new delivery records and attempts at most one due item.
// Run it from a dedicated explicitly started maintenance process.
func (m Manager) ProcessOne(ctx context.Context) (bool, error) {
	if !m.Config.HasKey {
		return false, ErrUnavailable
	}
	if err := m.enqueue(ctx); err != nil {
		return false, err
	}
	item, found, err := m.claim(ctx)
	if err != nil || !found {
		return false, err
	}
	status, code := m.send(ctx, item)
	return true, m.finalize(ctx, item, status, code)
}
