package api

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/uploads"
)

func (a *API) createUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	var input uploads.CreateInput
	if !decode(w, r, &input) {
		return
	}
	result, err := a.Uploads.Create(r.Context(), user.ID, input)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, result)
}
func (a *API) listUploads(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	result, err := a.Uploads.List(r.Context(), user.ID)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": result})
}
func (a *API) getUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	result, err := a.Uploads.Get(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) putPart(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
	if err != nil || index < 0 {
		failure(w, uploads.ErrInvalid)
		return
	}
	if r.ContentLength > uploads.MaxPartSize {
		writeError(w, 413, "part_too_large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxPartSize)
	if err = a.Uploads.PutPart(r.Context(), user.ID, r.PathValue("id"), index, r.Body); err != nil {
		failure(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) cancelUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	result, err := a.Uploads.Cancel(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	reader, name, size, err := a.Uploads.Download(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == "HEAD" {
		return
	}
	if _, err = io.Copy(w, reader); err != nil {
		panic(http.ErrAbortHandler)
	}
}
