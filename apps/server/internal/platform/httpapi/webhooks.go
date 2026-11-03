package httpapi

import (
	"net/http"

	webhook "qual/internal/integration/webhooks"
)

type webhookAPI struct {
	work    workAPI
	manager webhook.Manager
}

func webhookMeta(w http.ResponseWriter, r *http.Request) (webhook.Meta, bool) {
	meta, ok := requireIdempotency(w, r)
	return webhook.Meta{RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey}, ok
}
func (h webhookAPI) subscriptions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == "GET" {
		rows, err := h.manager.List(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := webhookMeta(w, r)
	if !ok {
		return
	}
	var in struct {
		Endpoint   string   `json:"endpoint"`
		EventTypes []string `json:"event_types"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	out, err := h.manager.Register(r.Context(), actor, pid, in.Endpoint, in.EventTypes, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, out)
}
func (h webhookAPI) disable(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "subscriptionID")
	if !ok {
		return
	}
	meta, ok := webhookMeta(w, r)
	if !ok {
		return
	}
	if err := h.manager.Disable(r.Context(), actor, pid, id, meta); err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (h webhookAPI) deliveries(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	rows, err := h.manager.Deliveries(r.Context(), actor, pid)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows})
}
