// Package oci implements the OCI Distribution provider.
package oci

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/restayway/regbot/pkg/policy"
	"github.com/restayway/regbot/pkg/provider"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"
const indexMediaType = "application/vnd.oci.image.index.v1+json"

type Client struct {
	name         string
	base         *url.URL
	http         *http.Client
	username     string
	password     string
	repositories []string
}

func New(name, endpoint, username, password, caFile string, insecure bool, timeout time.Duration, repositories []string) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid OCI endpoint %q", endpoint)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec // explicit operator option
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{name: name, base: base, username: username, password: password, repositories: repositories, http: &http.Client{Transport: transport, Timeout: timeout}}, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) Preflight(ctx context.Context) (provider.Capabilities, error) {
	response, err := c.do(ctx, http.MethodGet, "/v2/", nil, "")
	if err != nil {
		return provider.Capabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return provider.Capabilities{}, responseError(response)
	}
	capabilities := provider.Capabilities{Delete: true, Referrers: true}
	if len(c.repositories) > 0 {
		return capabilities, nil
	}
	response, err = c.do(ctx, http.MethodGet, "/v2/_catalog?n=1", nil, "")
	if err == nil {
		defer response.Body.Close()
		capabilities.Catalog = response.StatusCode == http.StatusOK
	}
	if !capabilities.Catalog {
		return capabilities, fmt.Errorf("registry catalog is unavailable; configure repositories explicitly")
	}
	return capabilities, nil
}

func (c *Client) List(ctx context.Context, target provider.Target) ([]provider.Artifact, error) {
	repositories := append([]string(nil), c.repositories...)
	if len(repositories) == 0 {
		var err error
		repositories, err = c.catalog(ctx)
		if err != nil {
			return nil, err
		}
	}
	var artifacts []provider.Artifact
	for _, repository := range repositories {
		if !policy.MatchRepository(repository, target.Includes, target.Excludes) {
			continue
		}
		found, err := c.listRepository(ctx, repository)
		if err != nil {
			return nil, fmt.Errorf("list repository %s: %w", repository, err)
		}
		artifacts = append(artifacts, found...)
	}
	return artifacts, nil
}

func (c *Client) Delete(ctx context.Context, request provider.DeleteRequest) error {
	current, err := c.resolve(ctx, request.Repository, firstTag(request.Tags))
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			return provider.ErrNotFound
		}
		return err
	}
	if current.Digest != request.Digest {
		return fmt.Errorf("%w: digest changed from %s to %s", provider.ErrPrecondition, request.Digest, current.Digest)
	}
	response, err := c.do(ctx, http.MethodDelete, "/v2/"+escapeRepository(request.Repository)+"/manifests/"+url.PathEscape(request.Digest), nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusAccepted:
		return nil
	case http.StatusNotFound:
		return provider.ErrNotFound
	case http.StatusMethodNotAllowed, http.StatusBadRequest:
		return fmt.Errorf("manifest deletion is disabled: %w", responseError(response))
	default:
		return responseError(response)
	}
}

func (c *Client) catalog(ctx context.Context) ([]string, error) {
	var response struct {
		Repositories []string `json:"repositories"`
	}
	if err := c.pages(ctx, "/v2/_catalog?n=100", func(body io.Reader) error {
		var page struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return err
		}
		response.Repositories = append(response.Repositories, page.Repositories...)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(response.Repositories)
	return response.Repositories, nil
}

func (c *Client) listRepository(ctx context.Context, repository string) ([]provider.Artifact, error) {
	var tags []string
	path := "/v2/" + escapeRepository(repository) + "/tags/list?n=100"
	if err := c.pages(ctx, path, func(body io.Reader) error {
		var page struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return err
		}
		tags = append(tags, page.Tags...)
		return nil
	}); err != nil {
		return nil, err
	}
	byDigest := map[string]*provider.Artifact{}
	for _, tag := range tags {
		artifact, err := c.resolve(ctx, repository, tag)
		if err != nil {
			return nil, err
		}
		if existing := byDigest[artifact.Digest]; existing != nil {
			existing.Tags = append(existing.Tags, tag)
			continue
		}
		artifact.Tags = []string{tag}
		byDigest[artifact.Digest] = &artifact
	}
	result := make([]provider.Artifact, 0, len(byDigest))
	for _, artifact := range byDigest {
		referrers, err := c.referrers(ctx, repository, artifact.Digest)
		if err != nil {
			return nil, err
		}
		artifact.ReferrerIDs = referrers
		sort.Strings(artifact.Tags)
		result = append(result, *artifact)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Digest < result[j].Digest })
	return result, nil
}

func (c *Client) referrers(ctx context.Context, repository, digest string) ([]string, error) {
	path := "/v2/" + escapeRepository(repository) + "/referrers/" + url.PathEscape(digest)
	response, err := c.do(ctx, http.MethodGet, path, nil, indexMediaType)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return c.referrersByTag(ctx, repository, digest)
	}
	if response.StatusCode != http.StatusOK {
		err := responseError(response)
		response.Body.Close()
		return nil, err
	}
	defer response.Body.Close()
	return decodeReferrersIndex(response, repository, digest, "referrers API")
}

func (c *Client) referrersByTag(ctx context.Context, repository, digest string) ([]string, error) {
	tag, err := referrersTag(digest)
	if err != nil {
		return nil, fmt.Errorf("derive referrers tag for %s@%s: %w", repository, digest, err)
	}
	path := "/v2/" + escapeRepository(repository) + "/manifests/" + url.PathEscape(tag)
	response, err := c.do(ctx, http.MethodGet, path, nil, indexMediaType)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		err := responseError(response)
		response.Body.Close()
		return nil, fmt.Errorf("fetch referrers tag for %s@%s: %w", repository, digest, err)
	}
	defer response.Body.Close()
	return decodeReferrersIndex(response, repository, digest, "referrers tag")
}

func decodeReferrersIndex(response *http.Response, repository, subject, source string) ([]string, error) {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != indexMediaType {
		return nil, fmt.Errorf("%s for %s@%s returned invalid content type %q", source, repository, subject, response.Header.Get("Content-Type"))
	}
	var index struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Manifests     []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.NewDecoder(response.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("decode %s for %s@%s: %w", source, repository, subject, err)
	}
	if index.SchemaVersion != 2 {
		return nil, fmt.Errorf("%s for %s@%s has invalid schemaVersion %d", source, repository, subject, index.SchemaVersion)
	}
	if index.MediaType != "" && index.MediaType != indexMediaType {
		return nil, fmt.Errorf("%s for %s@%s has invalid mediaType %q", source, repository, subject, index.MediaType)
	}
	seen := make(map[string]struct{}, len(index.Manifests))
	result := make([]string, 0, len(index.Manifests))
	for _, descriptor := range index.Manifests {
		if !validDigest(descriptor.Digest) {
			return nil, fmt.Errorf("%s for %s@%s contains invalid descriptor digest %q", source, repository, subject, descriptor.Digest)
		}
		if _, ok := seen[descriptor.Digest]; !ok {
			seen[descriptor.Digest] = struct{}{}
			result = append(result, descriptor.Digest)
		}
	}
	sort.Strings(result)
	return result, nil
}

func referrersTag(digest string) (string, error) {
	algorithm, encoded, ok := strings.Cut(digest, ":")
	if !ok || !validDigest(digest) {
		return "", fmt.Errorf("invalid digest %q", digest)
	}
	algorithm = truncate(algorithm, 32)
	encoded = truncate(encoded, 64)
	return sanitizeTagPart(algorithm) + "-" + sanitizeTagPart(encoded), nil
}

func validDigest(digest string) bool {
	algorithm, encoded, ok := strings.Cut(digest, ":")
	if !ok || algorithm == "" || encoded == "" {
		return false
	}
	for i, r := range algorithm {
		if i == 0 {
			if !isASCIIAlpha(r) {
				return false
			}
			continue
		}
		if !isASCIIAlphaNumeric(r) && !strings.ContainsRune("+._-", r) {
			return false
		}
	}
	for _, r := range encoded {
		if !isASCIIAlphaNumeric(r) && !strings.ContainsRune("=_-", r) {
			return false
		}
	}
	switch algorithm {
	case "sha256":
		return len(encoded) == 64 && isLowerHex(encoded)
	case "sha512":
		return len(encoded) == 128 && isLowerHex(encoded)
	}
	return true
}

func sanitizeTagPart(value string) string {
	return strings.Map(func(r rune) rune {
		if isASCIIAlphaNumeric(r) || strings.ContainsRune("_.-", r) {
			return r
		}
		return '-'
	}, value)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func isASCIIAlpha(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func isASCIIAlphaNumeric(r rune) bool {
	return isASCIIAlpha(r) || r >= '0' && r <= '9'
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Client) resolve(ctx context.Context, repository, reference string) (provider.Artifact, error) {
	if reference == "" {
		return provider.Artifact{}, provider.ErrNotFound
	}
	path := "/v2/" + escapeRepository(repository) + "/manifests/" + url.PathEscape(reference)
	response, err := c.do(ctx, http.MethodHead, path, nil, manifestAccept)
	if err != nil {
		return provider.Artifact{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return provider.Artifact{}, provider.ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return provider.Artifact{}, responseError(response)
	}
	digest := response.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return provider.Artifact{}, fmt.Errorf("registry omitted Docker-Content-Digest for %s:%s", repository, reference)
	}
	return provider.Artifact{
		Provider: "oci", Registry: c.name, Repository: repository, ID: digest, Digest: digest,
		MediaType: response.Header.Get("Content-Type"), Size: response.ContentLength,
	}, nil
}

func (c *Client) pages(ctx context.Context, path string, decode func(io.Reader) error) error {
	for path != "" {
		response, err := c.do(ctx, http.MethodGet, path, nil, "")
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			err := responseError(response)
			response.Body.Close()
			return err
		}
		if err := decode(response.Body); err != nil {
			response.Body.Close()
			return err
		}
		response.Body.Close()
		path = nextLink(response.Header.Get("Link"))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, accept string) (*http.Response, error) {
	requestURL := c.base.ResolveReference(&url.URL{Path: strings.Split(path, "?")[0], RawQuery: rawQuery(path)})
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if c.username != "" {
		request.SetBasicAuth(c.username, c.password)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(strings.ToLower(response.Header.Get("WWW-Authenticate")), "bearer ") {
		return response, nil
	}
	challenge := response.Header.Get("WWW-Authenticate")
	response.Body.Close()
	token, err := c.bearerToken(ctx, challenge)
	if err != nil {
		return nil, err
	}
	request, err = http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	return c.http.Do(request)
}

func (c *Client) bearerToken(ctx context.Context, challenge string) (string, error) {
	params := parseChallenge(strings.TrimSpace(challenge[len("Bearer "):]))
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("bearer challenge has no realm")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	query := tokenURL.Query()
	for _, key := range []string{"service", "scope"} {
		if params[key] != "" {
			query.Set(key, params[key])
		}
	}
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	if c.username != "" {
		request.SetBasicAuth(c.username, c.password)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", responseError(response)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		payload.Token = payload.AccessToken
	}
	if payload.Token == "" {
		return "", fmt.Errorf("token service returned an empty token")
	}
	return payload.Token, nil
}

func responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return fmt.Errorf("registry returned %s: %s", response.Status, strings.TrimSpace(string(body)))
}

func escapeRepository(repository string) string {
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func firstTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func rawQuery(path string) string {
	_, query, _ := strings.Cut(path, "?")
	return query
}

func nextLink(value string) string {
	start, end := strings.Index(value, "<"), strings.Index(value, ">")
	if start < 0 || end <= start {
		return ""
	}
	return value[start+1 : end]
}

func parseChallenge(value string) map[string]string {
	result := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			result[strings.ToLower(key)] = strings.Trim(val, `"`)
		}
	}
	return result
}
