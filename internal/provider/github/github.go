// Package github implements GitHub Container Registry package discovery.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/restayway/regbot/pkg/policy"
	"github.com/restayway/regbot/pkg/provider"
)

type Client struct {
	name      string
	base      *url.URL
	owner     string
	ownerType string
	token     string
	http      *http.Client
}

func New(name, endpoint, owner, ownerType, token string, timeout time.Duration) (*Client, error) {
	if endpoint == "" {
		endpoint = "https://api.github.com"
	}
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid GitHub API endpoint")
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{name: name, base: base, owner: owner, ownerType: ownerType, token: token, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) Preflight(ctx context.Context) (provider.Capabilities, error) {
	path := "/user"
	if c.ownerType == "organization" {
		path = "/orgs/" + url.PathEscape(c.owner)
	}
	response, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return provider.Capabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return provider.Capabilities{}, responseError(response)
	}
	return provider.Capabilities{Catalog: true, Delete: true}, nil
}

func (c *Client) List(ctx context.Context, target provider.Target) ([]provider.Artifact, error) {
	packages, err := c.packages(ctx)
	if err != nil {
		return nil, err
	}
	var artifacts []provider.Artifact
	for _, packageName := range packages {
		if !policy.MatchRepository(packageName, target.Includes, target.Excludes) {
			continue
		}
		versions, err := c.versions(ctx, packageName)
		if err != nil {
			return nil, fmt.Errorf("list package %s: %w", packageName, err)
		}
		artifacts = append(artifacts, versions...)
	}
	return artifacts, nil
}

func (c *Client) Delete(ctx context.Context, request provider.DeleteRequest) error {
	id := url.PathEscape(request.ID)
	path := c.ownerPrefix() + "/packages/container/" + url.PathEscape(request.Repository) + "/versions/" + id
	current, err := c.version(ctx, path, request.Repository)
	if err != nil {
		return err
	}
	if request.Digest != "" && current.Digest != request.Digest ||
		!request.UpdatedAt.IsZero() && !current.UpdatedAt.Equal(request.UpdatedAt) ||
		!sameTags(current.Tags, request.Tags) {
		return fmt.Errorf("%w: GitHub package version changed", provider.ErrPrecondition)
	}
	response, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return provider.ErrNotFound
	case http.StatusConflict:
		return provider.ErrPrecondition
	default:
		return responseError(response)
	}
}

func (c *Client) version(ctx context.Context, path, packageName string) (provider.Artifact, error) {
	response, err := c.do(ctx, http.MethodGet, path, nil)
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
	var version struct {
		ID        int64     `json:"id"`
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Metadata  struct {
			Container struct {
				Tags []string `json:"tags"`
			} `json:"container"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		return provider.Artifact{}, err
	}
	return provider.Artifact{
		Provider: "github", Registry: c.name, Repository: packageName,
		ID: strconv.FormatInt(version.ID, 10), Digest: version.Name,
		Tags: version.Metadata.Container.Tags, CreatedAt: version.CreatedAt, UpdatedAt: version.UpdatedAt,
	}, nil
}

func (c *Client) packages(ctx context.Context) ([]string, error) {
	path := c.ownerPrefix() + "/packages?package_type=container&per_page=100"
	var names []string
	err := c.pages(ctx, path, func(body io.Reader) error {
		var packages []struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(body).Decode(&packages); err != nil {
			return err
		}
		for _, item := range packages {
			names = append(names, item.Name)
		}
		return nil
	})
	return names, err
}

func (c *Client) versions(ctx context.Context, packageName string) ([]provider.Artifact, error) {
	path := c.ownerPrefix() + "/packages/container/" + url.PathEscape(packageName) + "/versions?per_page=100"
	var result []provider.Artifact
	err := c.pages(ctx, path, func(body io.Reader) error {
		var versions []struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Metadata  struct {
				Container struct {
					Tags []string `json:"tags"`
				} `json:"container"`
			} `json:"metadata"`
		}
		if err := json.NewDecoder(body).Decode(&versions); err != nil {
			return err
		}
		for _, version := range versions {
			result = append(result, provider.Artifact{
				Provider: "github", Registry: c.name, Repository: packageName,
				ID: strconv.FormatInt(version.ID, 10), Digest: version.Name,
				Tags: version.Metadata.Container.Tags, CreatedAt: version.CreatedAt, UpdatedAt: version.UpdatedAt,
			})
		}
		return nil
	})
	return result, err
}

func (c *Client) ownerPrefix() string {
	if c.ownerType == "organization" {
		return "/orgs/" + url.PathEscape(c.owner)
	}
	return "/users/" + url.PathEscape(c.owner)
}

func (c *Client) pages(ctx context.Context, path string, decode func(io.Reader) error) error {
	for path != "" {
		response, err := c.do(ctx, http.MethodGet, path, nil)
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
		next := githubNext(response.Header.Get("Link"))
		response.Body.Close()
		path = next
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	requestURL := path
	if strings.HasPrefix(path, "/") {
		requestURL = c.base.ResolveReference(&url.URL{Path: strings.Split(path, "?")[0], RawQuery: rawQuery(path)}).String()
	}
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		request.Header.Set("User-Agent", "restayway-regbot")
		response, err := c.http.Do(request)
		if err != nil {
			last = err
		} else if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 && !(response.StatusCode == http.StatusForbidden && response.Header.Get("Retry-After") != "") {
			return response, nil
		} else {
			last = responseError(response)
			response.Body.Close()
		}
		delay := retryDelay(response, attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("GitHub request retries exhausted: %w", last)
}

func retryDelay(response *http.Response, attempt int) time.Duration {
	if response != nil {
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	base := math.Pow(2, float64(attempt))
	return time.Duration(base*500)*time.Millisecond + time.Duration(rand.IntN(250))*time.Millisecond
}

func responseError(response *http.Response) error {
	if response == nil {
		return errors.New("empty GitHub response")
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return fmt.Errorf("GitHub returned %s: %s", response.Status, strings.TrimSpace(string(body)))
}

func githubNext(value string) string {
	for _, link := range strings.Split(value, ",") {
		if !strings.Contains(link, `rel="next"`) {
			continue
		}
		start, end := strings.Index(link, "<"), strings.Index(link, ">")
		if start >= 0 && end > start {
			return link[start+1 : end]
		}
	}
	return ""
}

func rawQuery(path string) string {
	_, query, _ := strings.Cut(path, "?")
	return query
}

func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, tag := range a {
		counts[tag]++
	}
	for _, tag := range b {
		counts[tag]--
		if counts[tag] < 0 {
			return false
		}
	}
	return true
}
