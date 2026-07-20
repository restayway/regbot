// Package provider defines the registry-neutral contracts used by Regbot.
//
// Provider implementations discover immutable artifacts and delete them only
// after the engine has verified their preconditions.
package provider

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound indicates that an artifact no longer exists.
var ErrNotFound = errors.New("artifact not found")

// ErrPrecondition indicates that an artifact changed after a plan was created.
var ErrPrecondition = errors.New("artifact precondition failed")

// Artifact is a provider-neutral container artifact. Multiple tags may point
// to the same immutable artifact.
type Artifact struct {
	Provider       string    `json:"provider"`
	Registry       string    `json:"registry"`
	Repository     string    `json:"repository"`
	ID             string    `json:"id"`
	Digest         string    `json:"digest,omitempty"`
	MediaType      string    `json:"mediaType,omitempty"`
	ArtifactType   string    `json:"artifactType,omitempty"`
	Tags           []string  `json:"tags"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
	Size           int64     `json:"size,omitempty"`
	ChildDigests   []string  `json:"childDigests,omitempty"`
	ReferrerIDs    []string  `json:"referrerIds,omitempty"`
	ParsedVersions []Version `json:"parsedVersions,omitempty"`
}

// Version is a typed calendar version parsed from an artifact tag.
type Version struct {
	Tag      string    `json:"tag"`
	Date     time.Time `json:"date"`
	Sequence uint64    `json:"sequence"`
	App      string    `json:"app,omitempty"`
}

// Target identifies a configured provider target.
type Target struct {
	Registry string
	Includes []string
	Excludes []string
}

// DeleteRequest carries immutable preconditions for a deletion.
type DeleteRequest struct {
	Repository string
	ID         string
	Digest     string
	Tags       []string
	UpdatedAt  time.Time
}

// Capabilities describes operations supported by a provider endpoint.
type Capabilities struct {
	Catalog   bool `json:"catalog"`
	Delete    bool `json:"delete"`
	Referrers bool `json:"referrers"`
}

// Provider discovers and mutates one configured registry.
type Provider interface {
	Name() string
	Preflight(context.Context) (Capabilities, error)
	List(context.Context, Target) ([]Artifact, error)
	Delete(context.Context, DeleteRequest) error
}
