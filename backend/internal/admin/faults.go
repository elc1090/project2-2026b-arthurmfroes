package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrFaultInvalid     = errors.New("invalid fault action")
	ErrFaultUnavailable = errors.New("fault control unavailable")
)

type FaultComponent string
type FaultCommand string
type FaultStatus string

const (
	FaultBackend FaultComponent = "backend"
	FaultSQL     FaultComponent = "sql"
	FaultStorage FaultComponent = "storage"
	FaultNode    FaultComponent = "node"
	FaultStop    FaultCommand   = "stop"
	FaultRestore FaultCommand   = "restore"
)

type FaultRequest struct {
	NodeID    string         `json:"node_id"`
	Component FaultComponent `json:"component"`
	Action    FaultCommand   `json:"action"`
}

type FaultResult struct {
	Component FaultComponent `json:"component"`
	Status    FaultStatus    `json:"status"`
	Error     *string        `json:"error"`
}

type FaultAction struct {
	ID        string         `json:"id"`
	NodeID    string         `json:"node_id"`
	Component FaultComponent `json:"component"`
	Action    FaultCommand   `json:"action"`
	Status    FaultStatus    `json:"status"`
	Error     *string        `json:"error"`
	UpdatedAt string         `json:"updated_at"`
	Results   []FaultResult  `json:"results"`
}

type FaultControl interface {
	Start(context.Context, FaultRequest) (FaultAction, error)
	Actions(context.Context) ([]FaultAction, error)
}

type HTTPFaultControl struct {
	origin string
	token  string
	client *http.Client
}

func NewHTTPFaultControl(origin, token string) (*HTTPFaultControl, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(token) == "" {
		return nil, ErrFaultInvalid
	}
	return &HTTPFaultControl{origin: strings.TrimRight(origin, "/"), token: token, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *HTTPFaultControl) request(ctx context.Context, method string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return ErrFaultInvalid
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+"/v1/actions", reader)
	if err != nil {
		return ErrFaultUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ErrFaultUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
		return ErrFaultInvalid
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrFaultUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(result); err != nil {
		return ErrFaultUnavailable
	}
	return nil
}

func (c *HTTPFaultControl) Start(ctx context.Context, input FaultRequest) (FaultAction, error) {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return FaultAction{}, ErrFaultUnavailable
	}
	wireInput := struct {
		IdempotencyKey string         `json:"idempotency_key"`
		NodeID         string         `json:"node_id"`
		Component      FaultComponent `json:"component"`
		Action         FaultCommand   `json:"action"`
	}{
		IdempotencyKey: "backend-" + hex.EncodeToString(keyBytes),
		NodeID:         input.NodeID,
		Component:      input.Component,
		Action:         input.Action,
	}
	var result FaultAction
	err := c.request(ctx, http.MethodPost, wireInput, &result)
	return result, err
}

func (c *HTTPFaultControl) Actions(ctx context.Context) ([]FaultAction, error) {
	result := []FaultAction{}
	err := c.request(ctx, http.MethodGet, nil, &result)
	return result, err
}

func validFaultRequest(input FaultRequest) bool {
	component := input.Component == FaultBackend || input.Component == FaultSQL || input.Component == FaultStorage || input.Component == FaultNode
	action := input.Action == FaultStop || input.Action == FaultRestore
	return input.NodeID != "" && component && action
}

func (s Service) StartFault(ctx context.Context, input FaultRequest) (FaultAction, error) {
	if !validFaultRequest(input) {
		return FaultAction{}, ErrFaultInvalid
	}
	if s.FaultControl == nil {
		return FaultAction{}, ErrFaultUnavailable
	}
	return s.FaultControl.Start(ctx, input)
}
