package link

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOrigin is the hosted control plane. Override with `wasa login --url`
// (and change this once the hosted instance ships).
const DefaultOrigin = "https://wasa.build"

const apiPrefix = "/api/v1"

// Link poll outcomes, mirroring the api's status strings.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// StartResponse is the api's answer to opening a link request.
type StartResponse struct {
	UserCode     string `json:"user_code"`
	VerifyURL    string `json:"verify_url"`
	DeviceSecret string `json:"device_secret"`
	Interval     int    `json:"interval"`
	ExpiresIn    int    `json:"expires_in"`
}

// PollResponse is one answer to polling a link request. Token is set exactly
// once, on the poll that observes approval.
type PollResponse struct {
	Status string `json:"status"`
	Token  string `json:"token"`
}

// StartLink opens a device-link request for a runner with the given name.
func StartLink(
	ctx context.Context,
	origin, name string,
) (StartResponse, error) {
	var out StartResponse
	err := postJSON(
		ctx, origin, "/runners/link/start",
		map[string]string{"name": name}, "", &out,
	)
	if err != nil {
		return StartResponse{}, err
	}
	if out.DeviceSecret == "" || out.UserCode == "" {
		return StartResponse{}, fmt.Errorf("api returned an empty link grant")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	return out, nil
}

// PollLink asks the api whether the link request was decided.
func PollLink(
	ctx context.Context,
	origin, deviceSecret string,
) (PollResponse, error) {
	var out PollResponse
	err := postJSON(
		ctx, origin, "/runners/link/poll",
		map[string]string{"device_secret": deviceSecret}, "", &out,
	)
	if err != nil {
		return PollResponse{}, err
	}
	return out, nil
}

// Revoke deletes the runner server-side — `wasa logout`'s best-effort
// cleanup.
func Revoke(ctx context.Context, origin, token string) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		strings.TrimSuffix(origin, "/")+apiPrefix+"/runners/self",
		nil,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("api answered %s", resp.Status)
	}
	return nil
}

func postJSON(
	ctx context.Context,
	origin, path string,
	body any,
	token string,
	out any,
) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimSuffix(origin, "/")+apiPrefix+path,
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("api answered %s: %s",
			resp.Status, strings.TrimSpace(readSome(resp.Body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode api response: %w", err)
	}
	return nil
}

func readSome(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 512))
	return string(data)
}
