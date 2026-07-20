package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/restayway/regbot/pkg/provider"
)

func TestListAndDeleteVersion(t *testing.T) {
	t.Parallel()
	var deleted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(writer, "missing token", http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/orgs/example-org":
			_, _ = writer.Write([]byte(`{"login":"example-org"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/orgs/example-org/packages":
			_, _ = writer.Write([]byte(`[{"name":"api"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/orgs/example-org/packages/container/api/versions":
			_, _ = writer.Write([]byte(`[{"id":42,"name":"sha256:abc","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","metadata":{"container":{"tags":["v2026.01.01.1-api"]}}}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/orgs/example-org/packages/container/api/versions/42":
			_, _ = writer.Write([]byte(`{"id":42,"name":"sha256:abc","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","metadata":{"container":{"tags":["v2026.01.01.1-api"]}}}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/orgs/example-org/packages/container/api/versions/42":
			deleted.Store(true)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.Error(writer, request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New("github", server.URL, "example-org", "organization", "test-token", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	artifacts, err := client.List(context.Background(), provider.Target{Includes: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != "42" {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	if err := client.Delete(context.Background(), provider.DeleteRequest{
		Repository: "api", ID: "42", Digest: "sha256:abc",
		Tags: []string{"v2026.01.01.1-api"},
	}); err != nil {
		t.Fatal(err)
	}
	if !deleted.Load() {
		t.Fatal("expected deletion")
	}
}
