// Package hook delivers post-apply notifications.
package hook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/restayway/regbot/pkg/plan"
)

type Webhook struct {
	URL, BearerToken, HMACSecret string
	Timeout                      time.Duration
}

func (w Webhook) Deliver(ctx context.Context, result plan.Result) error {
	payload, err := json.Marshal(struct {
		Version string `json:"version"`
		Deleted int    `json:"deleted"`
		Failed  int    `json:"failed"`
	}{Version: result.Version, Deleted: result.Deleted, Failed: result.Failed})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if w.BearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+w.BearerToken)
	}
	if w.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(w.HMACSecret))
		mac.Write(payload)
		request.Header.Set("X-Regbot-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	timeout := w.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", response.Status)
	}
	return nil
}
