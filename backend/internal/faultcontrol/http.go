package faultcontrol

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type Handler struct {
	service *Service
	token   string
}

func NewHandler(service *Service, token string) (http.Handler, error) {
	if service == nil || strings.TrimSpace(token) == "" {
		return nil, errors.New("fault actuator service and internal token are required")
	}
	return &Handler{service: service, token: token}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorized(r) {
		h.writeError(w, http.StatusUnauthorized, "invalid internal credential")
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/actions":
		h.submit(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/actions":
		if id := r.URL.Query().Get("id"); id != "" {
			h.get(w, id)
		} else {
			h.writeJSON(w, http.StatusOK, h.service.List())
		}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/actions/"):
		h.get(w, strings.TrimPrefix(r.URL.Path, "/v1/actions/"))
	default:
		h.writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimPrefix(header, prefix)
	return len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action, _, err := h.service.Submit(r.Context(), request)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, ErrIdempotencyConflict):
			status = http.StatusConflict
		case errors.Is(err, ErrTargetNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrDriverUnavailable):
			status = http.StatusServiceUnavailable
		}
		h.writeError(w, status, clientError(err))
		return
	}
	h.writeJSON(w, http.StatusAccepted, action)
}

func clientError(err error) string {
	switch {
	case errors.Is(err, ErrTargetNotFound):
		return "fault target is not configured"
	case errors.Is(err, ErrIdempotencyConflict):
		return "idempotency key conflicts with an existing action"
	case errors.Is(err, ErrDriverUnavailable):
		return "infrastructure driver is unavailable"
	case errors.Is(err, ErrInvalidRequest):
		return err.Error()
	default:
		return "invalid fault action request"
	}
}

func (h *Handler) get(w http.ResponseWriter, id string) {
	if id == "" || strings.Contains(id, "/") {
		h.writeError(w, http.StatusNotFound, "action not found")
		return
	}
	action, ok := h.service.Get(id)
	if !ok {
		h.writeError(w, http.StatusNotFound, "action not found")
		return
	}
	h.writeJSON(w, http.StatusOK, action)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
