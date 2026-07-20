package oci

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/restayway/regbot/pkg/provider"
)

const (
	testSubjectDigest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testReferrerDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
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

func TestReferrersFallsBackToTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		tagStatus   int
		tagBody     string
		contentType string
		want        []string
	}{
		{name: "missing fallback means no referrers", tagStatus: http.StatusNotFound},
		{
			name: "valid fallback index", tagStatus: http.StatusOK,
			contentType: indexMediaType,
			tagBody: fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[{"digest":%q},{"digest":%q}]}`,
				indexMediaType, testReferrerDigest, testReferrerDigest),
			want: []string{testReferrerDigest},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v2/repo/referrers/" + testSubjectDigest:
					http.NotFound(writer, request)
				case "/v2/repo/manifests/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":
					if test.contentType != "" {
						writer.Header().Set("Content-Type", test.contentType)
					}
					writer.WriteHeader(test.tagStatus)
					_, _ = writer.Write([]byte(test.tagBody))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			client, err := New("local", server.URL, "", "", "", false, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.referrers(context.Background(), "repo", testSubjectDigest)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("referrers() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReferrersFallbackFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{name: "unexpected status", status: http.StatusInternalServerError, body: "error"},
		{name: "invalid content type", status: http.StatusOK, contentType: "text/plain", body: `{"schemaVersion":2,"manifests":[]}`},
		{name: "invalid schema", status: http.StatusOK, contentType: indexMediaType, body: `{"schemaVersion":1,"manifests":[]}`},
		{name: "invalid media type", status: http.StatusOK, contentType: indexMediaType, body: `{"schemaVersion":2,"mediaType":"application/json","manifests":[]}`},
		{name: "invalid descriptor", status: http.StatusOK, contentType: indexMediaType, body: `{"schemaVersion":2,"manifests":[{"digest":"not-a-digest"}]}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.Contains(request.URL.Path, "/referrers/") {
					http.NotFound(writer, request)
					return
				}
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New("local", server.URL, "", "", "", false, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.referrers(context.Background(), "repo", testSubjectDigest); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestNativeReferrersProtectsSubject(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", indexMediaType)
		_, _ = writer.Write([]byte(fmt.Sprintf(
			`{"schemaVersion":2,"mediaType":%q,"manifests":[{"digest":%q}]}`,
			indexMediaType, testReferrerDigest,
		)))
	}))
	defer server.Close()
	client, err := New("local", server.URL, "", "", "", false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.referrers(context.Background(), "repo", testSubjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{testReferrerDigest}) {
		t.Fatalf("referrers() = %v", got)
	}
}

func TestReferrersTag(t *testing.T) {
	t.Parallel()
	got, err := referrersTag(testSubjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	want := "sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got != want {
		t.Fatalf("referrersTag() = %q, want %q", got, want)
	}
	for _, invalid := range []string{"", "sha256", "sha256:abc", ":abc", "sha256:ABCDEF"} {
		if _, err := referrersTag(invalid); err == nil {
			t.Errorf("referrersTag(%q) succeeded", invalid)
		}
	}
}
