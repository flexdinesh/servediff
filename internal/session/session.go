package session

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
)

type State string

const (
	Enabled     State = "enabled"
	Disabled    State = "disabled"
	Unavailable State = "unavailable"
)

const (
	DiffScopes          = "diff.scopes"
	DiffRefresh         = "diff.refresh"
	DiffStagingMetadata = "diff.stagingMetadata"
	FilesContents       = "files.contents"
	ReviewComments      = "review.comments"
)

type Capability struct {
	State State `json:"state"`
}

func (capability Capability) Enabled() bool { return capability.State == Enabled }

type ScopesCapability struct {
	State  State             `json:"state"`
	Values []review.DiffMode `json:"values"`
}

func (capability ScopesCapability) Allows(mode review.DiffMode) bool {
	if capability.State != Enabled {
		return false
	}
	for _, value := range capability.Values {
		if value == mode {
			return true
		}
	}
	return false
}

type DiffCapabilities struct {
	Scopes          ScopesCapability `json:"scopes"`
	Refresh         Capability       `json:"refresh"`
	StagingMetadata Capability       `json:"stagingMetadata"`
}

type FileCapabilities struct {
	Contents Capability `json:"contents"`
}

type ReviewCapabilities struct {
	Comments Capability `json:"comments"`
}

type Capabilities struct {
	Diff   DiffCapabilities   `json:"diff"`
	Files  FileCapabilities   `json:"files"`
	Review ReviewCapabilities `json:"review"`
}

type Policy string

const (
	Auto          Policy = "auto"
	DisablePolicy Policy = "disabled"
)

type Policies struct {
	Comments Policy
}

type Session struct {
	ID           string
	Source       diffsource.Source
	Capabilities Capabilities
}

type CapabilityError struct {
	Capability string
}

func (err *CapabilityError) Error() string {
	return "Capability " + err.Capability + " is not enabled for this session"
}

func Resolve(source diffsource.Source, policies Policies) Session {
	support := source.Support()
	capabilities := Capabilities{}
	capabilities.Diff.Scopes = ScopesCapability{
		State:  supported(len(support.Scopes) > 0),
		Values: append([]review.DiffMode(nil), support.Scopes...),
	}
	capabilities.Diff.Refresh = Capability{State: supported(support.Refresh)}
	capabilities.Diff.StagingMetadata = Capability{State: supported(support.StagingMetadata)}
	capabilities.Files.Contents = Capability{State: supported(support.FileContents)}
	capabilities.Review.Comments = Capability{State: policyState(true, policies.Comments)}
	hash := sha256.Sum256([]byte(source.Kind() + "\x00" + source.Root()))
	return Session{ID: hex.EncodeToString(hash[:]), Source: source, Capabilities: capabilities}
}

func supported(value bool) State {
	if value {
		return Enabled
	}
	return Unavailable
}

func policyState(supported bool, policy Policy) State {
	if !supported {
		return Unavailable
	}
	if policy == DisablePolicy {
		return Disabled
	}
	return Enabled
}

func NotEnabled(capability string) error {
	return &CapabilityError{Capability: capability}
}
