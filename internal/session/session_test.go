package session

import (
	"context"
	"reflect"
	"testing"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
)

type sourceStub struct {
	kind    string
	root    string
	support diffsource.Support
}

func (source sourceStub) Root() string { return source.root }
func (source sourceStub) Kind() string { return source.kind }
func (source sourceStub) Support() diffsource.Support {
	return source.support
}
func (source sourceStub) Snapshot(context.Context, review.DiffMode) (review.RepositoryDiff, error) {
	return review.RepositoryDiff{}, nil
}
func (source sourceStub) Patch(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FilePatch, error) {
	return review.FilePatch{}, nil
}
func (source sourceStub) Contents(context.Context, review.DiffMode, review.ChangedFile, *string) (review.FileContents, error) {
	return review.FileContents{}, nil
}

func TestResolveCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		support  diffsource.Support
		policies Policies
		want     Capabilities
	}{
		{
			name: "local",
			support: diffsource.Support{
				Scopes:          []review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged},
				Refresh:         true,
				StagingMetadata: true,
				FileContents:    true,
			},
			want: expectedCapabilities(
				[]review.DiffMode{review.DiffAll, review.DiffStaged, review.DiffUnstaged},
				Enabled, Enabled, Enabled, Enabled,
			),
		},
		{
			name:     "patch",
			support:  diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}},
			policies: Policies{Comments: Auto},
			want: expectedCapabilities(
				[]review.DiffMode{review.DiffAll},
				Unavailable, Unavailable, Unavailable, Enabled,
			),
		},
		{
			name:     "comments disabled",
			support:  diffsource.Support{Scopes: []review.DiffMode{review.DiffAll}},
			policies: Policies{Comments: DisablePolicy},
			want: expectedCapabilities(
				[]review.DiffMode{review.DiffAll},
				Unavailable, Unavailable, Unavailable, Disabled,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved := Resolve(sourceStub{kind: test.name, root: "/repo", support: test.support}, test.policies)
			if !reflect.DeepEqual(resolved.Capabilities, test.want) {
				t.Fatalf("capabilities = %#v, want %#v", resolved.Capabilities, test.want)
			}
		})
	}
}

func expectedCapabilities(scopes []review.DiffMode, refresh, staging, contents, comments State) Capabilities {
	capabilities := Capabilities{}
	capabilities.Diff.Scopes = ScopesCapability{State: Enabled, Values: scopes}
	capabilities.Diff.Refresh = Capability{State: refresh}
	capabilities.Diff.StagingMetadata = Capability{State: staging}
	capabilities.Files.Contents = Capability{State: contents}
	capabilities.Review.Comments = Capability{State: comments}
	return capabilities
}
