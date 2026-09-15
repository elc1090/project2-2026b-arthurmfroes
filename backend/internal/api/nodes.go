package api

import (
	"context"
	"net/http"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/admin"
)

func (a *API) nodeRoutes(mux *http.ServeMux) {
	wrap := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			user, ok := a.user(w, r)
			if !ok {
				return
			}
			if !user.IsAdmin {
				writeError(w, 403, "admin_required")
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("POST /api/admin/nodes", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input admin.JoinInput
		if !decode(w, r, &input) {
			return
		}
		operation, err := a.Admin.Nodes.Join(r.Context(), input)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 202, operation)
	}))
	mux.HandleFunc("POST /api/admin/nodes/{id}/retire", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Key string `json:"idempotency_key"`
		}
		if !decode(w, r, &input) {
			return
		}
		operation, err := a.Admin.Nodes.Retire(r.Context(), r.PathValue("id"), input.Key)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 202, operation)
	}))
	mux.HandleFunc("GET /api/admin/node-operations", wrap(func(w http.ResponseWriter, r *http.Request) {
		operations, err := a.Admin.Nodes.Operations(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"operations": operations})
	}))
	mux.HandleFunc("POST /api/admin/node-operations/{id}/cancel", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decode(w, r, &input) {
			return
		}
		operation, err := a.Admin.Nodes.CancelRetirement(r.Context(), r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, operation)
	}))
	mux.HandleFunc("GET /api/admin/node-operations/{id}", wrap(func(w http.ResponseWriter, r *http.Request) {
		operation, err := a.Admin.Nodes.Operation(r.Context(), r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, operation)
	}))
}
