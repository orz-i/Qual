// Package httpapi exposes liveness and authorized HTTP transport adapters.
// Protected business routes are mounted only when a configured store exists.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
)

// NewHandler builds a router. Phase 1 callers may omit dependencies and get
// only liveness; Phase 2 routes are never exposed without an Authenticator.
func NewHandler(deps ...*Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	var ready func(context.Context) error
	var token string
	var logger *slog.Logger
	if len(deps) > 0 && deps[0] != nil {
		ready = deps[0].Ready
		token = deps[0].MetricsToken
		logger = deps[0].Logger
	}
	stats := newMetrics()
	mux.HandleFunc("GET /readyz", readinessHandler(ready))
	if len(token) >= 32 {
		mux.HandleFunc("GET /internal/metrics", metricsHandler(stats, token))
	}
	if len(deps) > 0 && deps[0] != nil && deps[0].Identity != nil && deps[0].Work.Work != nil {
		api := workAPI{auth: deps[0].Identity, service: deps[0].Work}
		if deps[0].Browser != nil {
			sessions, err := newSessionAPI(deps[0].Browser, deps[0].WebOrigin)
			if err != nil {
				panic(err)
			}
			api.session = sessions
			mux.HandleFunc("POST /api/v1/auth/login", sessions.login)
			mux.HandleFunc("GET /api/v1/auth/session", sessions.current)
			mux.HandleFunc("POST /api/v1/auth/logout", sessions.logout)
			mux.HandleFunc("GET /api/v1/projects", api.projects)
		}
		if deps[0].Access.Repository != nil {
			access := accessAPI{work: api, access: deps[0].Access}
			mux.HandleFunc("GET /api/v1/projects/{projectID}/access/me", access.self)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/principals", access.principals)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/access", access.members)
			mux.HandleFunc("PUT /api/v1/projects/{projectID}/access/{principalID}/permissions/{permission}", access.change)
		}
		mux.HandleFunc("GET /api/v1/projects/{projectID}/review-policy", api.reviewPolicy)
		mux.HandleFunc("PUT /api/v1/projects/{projectID}/review-policy", api.reviewPolicy)
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items", api.create)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items", api.list)
		if deps[0].Work.Roles != nil {
			mux.HandleFunc("GET /api/v1/projects/{projectID}/review-queue", api.reviewQueue)
		}
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}", api.get)
		if deps[0].Context != nil {
			mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/context", contextAPI{work: api, read: deps[0].Context}.get)
		}
		mux.HandleFunc("PATCH /api/v1/projects/{projectID}/work-items/{itemID}", api.update)
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/claim", api.claim)
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/transitions", api.transition)
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/complete", api.complete)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/acceptances", api.acceptances)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/review-policy", api.workReviewPolicy)
		mux.HandleFunc("PUT /api/v1/projects/{projectID}/work-items/{itemID}/review-policy", api.workReviewPolicy)
		if deps[0].Work.Roles != nil {
			mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/role-entries", api.roleEntries)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/role-entries", api.roleEntries)
		}
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/comments", api.addComment)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/comments", api.comments)
		mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/dependencies", api.linkDependency)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/dependencies", api.dependencies)
		if deps[0].Runs.Runs != nil {
			runs := agentRunAPI{work: api, service: deps[0].Runs}
			mux.HandleFunc("POST /api/v1/projects/{projectID}/agent-runs", runs.register)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/agent-runs/{runID}", runs.get)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/agent-runs/{runID}/reports", runs.report)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/agent-runs/{runID}/reports", runs.reports)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/agent-runs", runs.byWorkItem)
		}
		if deps[0].Events != nil && deps[0].Events.Events != nil {
			events := eventAPI{work: api, service: deps[0].Events}
			mux.HandleFunc("POST /api/v1/projects/{projectID}/external-events", events.register)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/external-events", events.workTimeline)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/threads/{threadID}/external-events", events.threadTimeline)
		}
		mux.HandleFunc("GET /api/v1/projects/{projectID}/activity", api.activity)
		mux.HandleFunc("GET /api/v1/projects/{projectID}/audit", api.audit)
		if deps[0].Evidence.Records != nil {
			ev := evidenceAPI{work: api, service: deps[0].Evidence}
			mux.HandleFunc("GET /api/v1/projects/{projectID}/artifacts", ev.artifacts)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/artifacts", ev.artifacts)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/artifacts/{artifactID}", ev.artifact)
			mux.HandleFunc("PUT /api/v1/projects/{projectID}/artifacts/{artifactID}/content", ev.upload)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/artifacts/{artifactID}/complete", ev.complete)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/artifacts/{artifactID}/content", ev.download)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/external-references", ev.references)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/external-references", ev.references)
			if deps[0].Evidence.Threads != nil {
				mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/threads", ev.itemThreads)
				mux.HandleFunc("POST /api/v1/projects/{projectID}/work-items/{itemID}/threads", ev.itemThreads)
				mux.HandleFunc("DELETE /api/v1/projects/{projectID}/work-items/{itemID}/threads/{threadID}", ev.unlinkThread)
				mux.HandleFunc("GET /api/v1/projects/{projectID}/threads/{threadID}/work-items", ev.threadItems)
			}
			mux.HandleFunc("GET /api/v1/projects/{projectID}/exports", ev.exports)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/exports", ev.exports)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/exports/{exportID}/content", ev.export)
		}
		if deps[0].Webhooks != nil {
			wh := webhookAPI{work: api, manager: *deps[0].Webhooks}
			mux.HandleFunc("GET /api/v1/projects/{projectID}/webhooks", wh.subscriptions)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/webhooks", wh.subscriptions)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/webhooks/{subscriptionID}/disable", wh.disable)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/webhook-deliveries", wh.deliveries)
		}
		if deps[0].Tests.Tests != nil {
			tests := testingAPI{work: api, tests: deps[0].Tests}
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-suites", tests.suites)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-suites", tests.suites)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-cases", tests.cases)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-cases", tests.cases)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-cases/{caseID}", tests.caseDetail)
			mux.HandleFunc("PATCH /api/v1/projects/{projectID}/test-cases/{caseID}", tests.caseDetail)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-cases/{caseID}/versions", tests.versions)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-cases/{caseID}/versions", tests.versions)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-cases/{caseID}/coverage", tests.coverage)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-cases/{caseID}/coverage", tests.coverage)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/work-items/{itemID}/test-coverage", tests.workCoverage)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-runs", tests.runs)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-runs", tests.runs)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-runs/{runID}", tests.runDetail)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-runs/{runID}/junit-imports", tests.importJUnit)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-run-items/{itemID}/attempts", tests.attempts)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-run-items/{itemID}/attempts", tests.attempts)
			mux.HandleFunc("POST /api/v1/projects/{projectID}/test-attempts/{attemptID}/defects", tests.defect)
			mux.HandleFunc("GET /api/v1/projects/{projectID}/test-attempts/{attemptID}/defects", tests.defect)
		}
	}
	return wrapWithRequestID(observeRequests(mux, stats, logger))
}
