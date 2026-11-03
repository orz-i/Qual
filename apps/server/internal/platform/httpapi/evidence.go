package httpapi

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strconv"

	evidenceapp "qual/internal/evidence/application"
	"qual/internal/evidence/domain"
)

type evidenceAPI struct {
	work    workAPI
	service evidenceapp.Service
}

func evidenceMeta(w http.ResponseWriter, r *http.Request) (evidenceapp.Meta, bool) {
	meta, ok := requireIdempotency(w, r)
	return evidenceapp.Meta{RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey}, ok
}
func (e evidenceAPI) artifacts(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		kind := domain.TargetKind(r.URL.Query().Get("target_kind"))
		target, err := uuid.Parse(r.URL.Query().Get("target_id"))
		if err != nil {
			writeError(w, r, 400, "VALIDATION_ERROR", "target_id UUID required")
			return
		}
		items, err := e.service.List(r.Context(), actor, pid, kind, target)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	var in domain.BeginInput
	if !readJSON(w, r, &in) {
		return
	}
	item, err := e.service.Begin(r.Context(), actor, pid, in, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, item)
}
func (e evidenceAPI) artifact(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "artifactID")
	if !ok {
		return
	}
	item, err := e.service.Get(r.Context(), actor, pid, id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}
func (e evidenceAPI) upload(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "artifactID")
	if !ok {
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	defer r.Body.Close()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, domain.MaxArtifactSize))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			writeError(w, r, 413, "PAYLOAD_TOO_LARGE", "Artifact exceeds 10 MiB")
		} else {
			writeError(w, r, 400, "VALIDATION_ERROR", "Invalid artifact bytes")
		}
		return
	}
	item, err := e.service.Upload(r.Context(), actor, pid, id, data, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}
func (e evidenceAPI) complete(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "artifactID")
	if !ok {
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	item, err := e.service.Complete(r.Context(), actor, pid, id, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}
func attachment(w http.ResponseWriter, filename string, body []byte, sha string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-SHA256", sha)
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func (e evidenceAPI) download(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "artifactID")
	if !ok {
		return
	}
	item, body, err := e.service.Download(r.Context(), actor, pid, id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	attachment(w, item.Filename, body, item.SHA256)
}
func (e evidenceAPI) references(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		kind := domain.TargetKind(r.URL.Query().Get("target_kind"))
		target, err := uuid.Parse(r.URL.Query().Get("target_id"))
		if err != nil {
			writeError(w, r, 400, "VALIDATION_ERROR", "target_id UUID required")
			return
		}
		items, err := e.service.References(r.Context(), actor, pid, kind, target)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	var in domain.ReferenceInput
	if !readJSON(w, r, &in) {
		return
	}
	ref, err := e.service.AddReference(r.Context(), actor, pid, in, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, ref)
}
func (e evidenceAPI) exports(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := e.service.Exports(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	result, err := e.service.Export(r.Context(), actor, pid, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, result)
}
func (e evidenceAPI) export(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "exportID")
	if !ok {
		return
	}
	result, body, err := e.service.ExportDownload(r.Context(), actor, pid, id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	attachment(w, fmt.Sprintf("qual-%s-%s.zip", pid.String()[:8], id.String()[:8]), body, result.SHA256)
}
