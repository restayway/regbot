// Package plan defines the stable, serializable Regbot plan and result formats.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/restayway/regbot/pkg/provider"
)

const (
	// FormatVersion is the current plan wire-format version.
	FormatVersion = "v1"
)

// Action is one proposed deletion with immutable preconditions.
type Action struct {
	Policy      string            `json:"policy"`
	ReasonCodes []string          `json:"reasonCodes"`
	Artifact    provider.Artifact `json:"artifact"`
}

// Plan is an immutable deletion proposal.
type Plan struct {
	Version           string    `json:"version"`
	CreatedAt         time.Time `json:"createdAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
	ConfigFingerprint string    `json:"configFingerprint"`
	Actions           []Action  `json:"actions"`
	ProtectedCount    int       `json:"protectedCount"`
	DiscoveredCount   int       `json:"discoveredCount"`
}

// Result records the outcome of applying a plan.
type Result struct {
	Version    string          `json:"version"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt time.Time       `json:"finishedAt"`
	Planned    int             `json:"planned"`
	Deleted    int             `json:"deleted"`
	Skipped    int             `json:"skipped"`
	Failed     int             `json:"failed"`
	Outcomes   []ActionOutcome `json:"outcomes"`
}

// ActionOutcome records one apply outcome.
type ActionOutcome struct {
	Action Action `json:"action"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Fingerprint returns the SHA-256 fingerprint of bytes.
func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ID returns a deterministic fingerprint for a plan.
func (p Plan) ID() string {
	data, _ := json.Marshal(p)
	return Fingerprint(data)
}
