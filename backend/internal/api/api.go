// Package api exposes the private browser API. Eligibility is supplied by the
// node controller and checked before authentication or any user operation.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/accounts"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/admin"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/catalog"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/cluster"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/faults"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/lifecycle"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/storage"
	"github.com/elc1090/project2-2026b-arthurmfroes/backend/internal/uploads"
	"github.com/jackc/pgx/v5/pgconn"
)

const CookieName = "acervo_session"

type API struct {
	Admin         *admin.Service
	Uploads       *uploads.Service
	Accounts      accounts.Service
	Catalog       catalog.Service
	Eligible      func(*http.Request) error
	SecureCookies bool
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	if a.Admin != nil {
		if a.Admin.Nodes != nil {
			a.nodeRoutes(mux)
		}
		mux.HandleFunc("POST /api/admin/nodes/{id}/fault", func(w http.ResponseWriter, r *http.Request) {
			user, ok := a.user(w, r)
			if !ok {
				return
			}
			if !user.IsAdmin {
				writeError(w, 403, "admin_required")
				return
			}
			if a.Admin.Faults == nil || !a.Admin.Faults.Enabled {
				writeError(w, 404, "simulation_disabled")
				return
			}
			var input struct {
				Mode faults.Mode `json:"mode"`
			}
			if !decode(w, r, &input) {
				return
			}
			if err := a.Admin.Faults.Set(r.Context(), r.PathValue("id"), input.Mode); err != nil {
				failure(w, err)
				return
			}
			w.WriteHeader(204)
		})
		mux.HandleFunc("GET /api/admin/cluster", func(w http.ResponseWriter, r *http.Request) {
			user, ok := a.user(w, r)
			if !ok {
				return
			}
			if !user.IsAdmin {
				writeError(w, 403, "admin_required")
				return
			}
			result, err := a.Admin.View(r.Context())
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, result)
		})
	}
	mux.HandleFunc("POST /api/register", a.register)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/me", a.me)
	mux.HandleFunc("GET /api/folders", a.list)
	mux.HandleFunc("POST /api/folders", a.createFolder)
	if a.Uploads != nil {
		mux.HandleFunc("GET /api/files/{id}/download", a.download)
		mux.HandleFunc("POST /api/uploads", a.createUpload)
		mux.HandleFunc("GET /api/uploads", a.listUploads)
		mux.HandleFunc("GET /api/uploads/{id}", a.getUpload)
		mux.HandleFunc("PUT /api/uploads/{id}/parts/{index}", a.putPart)
		mux.HandleFunc("POST /api/uploads/{id}/cancel", a.cancelUpload)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				scheme := "http"
				if a.SecureCookies {
					scheme = "https"
				}
				if err != nil || parsed.Scheme != scheme || parsed.Host != r.Host || parsed.User != nil || parsed.Path != "" {
					writeError(w, http.StatusForbidden, "origin_rejected")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeError(w, http.StatusForbidden, "origin_rejected")
				return
			}
		}
		if a.Eligible == nil || a.Eligible(r) != nil {
			writeError(w, http.StatusServiceUnavailable, "node_unavailable")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	respond(w, status, map[string]string{"error": code})
}
func failure(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, lifecycle.ErrTopology):
		writeError(w, 409, "cockroach_topology_blocked")
	case errors.Is(err, lifecycle.ErrConflict), errors.Is(err, cluster.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, cluster.ErrInvalid):
		writeError(w, 400, "invalid_input")
	case errors.Is(err, cluster.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, accounts.ErrUnauthorized):
		writeError(w, 401, "unauthenticated")
	case errors.Is(err, storage.ErrIntegrity):
		writeError(w, 409, "content_mismatch")
	case errors.Is(err, uploads.ErrConflict), errors.Is(err, accounts.ErrConflict), errors.Is(err, catalog.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, faults.ErrNotFound), errors.Is(err, uploads.ErrNotFound), errors.Is(err, catalog.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, faults.ErrInvalid), errors.Is(err, uploads.ErrInvalid), errors.Is(err, accounts.ErrInvalid), errors.Is(err, catalog.ErrInvalid):
		writeError(w, 400, "invalid_input")
	case errors.As(err, &pg) && pg.Code == "22P02":
		writeError(w, 400, "invalid_input")
	default:
		writeError(w, 503, "temporarily_unavailable")
	}
}
func (a *API) user(w http.ResponseWriter, r *http.Request) (accounts.User, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		failure(w, accounts.ErrUnauthorized)
		return accounts.User{}, false
	}
	user, err := a.Accounts.Authenticate(r.Context(), c.Value)
	if err != nil {
		failure(w, err)
		return accounts.User{}, false
	}
	return user, true
}
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	user, err := a.Accounts.Register(r.Context(), input.Login, input.Password)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, user)
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	session, err := a.Accounts.Login(r.Context(), input.Login, input.Password)
	if err != nil {
		failure(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: session.Token, Path: "/", HttpOnly: true, Secure: a.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt})
	respond(w, 200, session)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if err = a.Accounts.Logout(r.Context(), c.Value); err != nil {
			failure(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	w.WriteHeader(204)
}
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	if user, ok := a.user(w, r); ok {
		respond(w, 200, user)
	}
}
func parent(r *http.Request) *string {
	if value := r.URL.Query().Get("parent_id"); value != "" {
		return &value
	}
	return nil
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	result, err := a.Catalog.List(r.Context(), user.ID, parent(r))
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) createFolder(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(w, r)
	if !ok {
		return
	}
	var input struct {
		ParentID *string `json:"parent_id"`
		Name     string  `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	result, err := a.Catalog.CreateFolder(r.Context(), user.ID, input.ParentID, input.Name)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, result)
}
