package hook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/restayway/regbot/pkg/plan"
)

func TestWebhookSignsPayload(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if got := request.Header.Get("X-Regbot-Signature-256"); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization = %q", got)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	err := (Webhook{URL: server.URL, BearerToken: "token", HMACSecret: "secret"}).Deliver(
		context.Background(), plan.Result{Version: "v1", Deleted: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
}
