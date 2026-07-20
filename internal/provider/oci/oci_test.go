package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/restayway/regbot/pkg/provider"
)

func TestListAndDeleteByDigest(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var deletedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v2/":
			writer.WriteHeader(http.StatusOK)
		case request.Method == http.MethodGet && request.URL.Path == "/v2/_catalog":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"repositories":["team/api"]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v2/team/api/tags/list":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"name":"team/api","tags":["v2026.01.01.1-api","stable"]}`))
		case request.Method == http.MethodHead && request.URL.Path == "/v2/team/api/manifests/v2026.01.01.1-api":
			writer.Header().Set("Docker-Content-Digest", "sha256:abc")
			writer.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		case request.Method == http.MethodHead && request.URL.Path == "/v2/team/api/manifests/stable":
			writer.Header().Set("Docker-Content-Digest", "sha256:abc")
			writer.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		case request.Method == http.MethodGet && request.URL.Path == "/v2/team/api/referrers/sha256:abc":
			writer.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = writer.Write([]byte(`{"schemaVersion":2,"manifests":[]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v2/team/api/manifests/sha256:abc":
			mu.Lock()
			deletedPath = request.URL.Path
			mu.Unlock()
			writer.WriteHeader(http.StatusAccepted)
		default:
			http.Error(writer, request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New("local", server.URL, "", "", "", false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := client.Preflight(context.Background())
	if err != nil || !capabilities.Catalog {
		t.Fatalf("preflight = %+v, %v", capabilities, err)
	}
	artifacts, err := client.List(context.Background(), provider.Target{Includes: []string{"team/*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || !slices.Equal(artifacts[0].Tags, []string{"stable", "v2026.01.01.1-api"}) {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	if err := client.Delete(context.Background(), provider.DeleteRequest{
		Repository: "team/api", Digest: "sha256:abc", Tags: artifacts[0].Tags,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if deletedPath == "" {
		t.Fatal("expected digest deletion")
	}
}

func TestDeleteDetectsChangedDigest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Docker-Content-Digest", "sha256:new")
	}))
	defer server.Close()
	client, err := New("local", server.URL, "", "", "", false, 0, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Delete(context.Background(), provider.DeleteRequest{
		Repository: "api", Digest: "sha256:old", Tags: []string{"v2026.01.01.1"},
	})
	if err == nil {
		t.Fatal("expected precondition error")
	}
}
