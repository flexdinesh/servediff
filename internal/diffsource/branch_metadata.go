package diffsource

import "context"

// CurrentBranch returns the same branch label as a snapshot without reading
// checkout diffs or file contents.
func CurrentBranch(ctx context.Context, root string) (string, error) {
	source := &gitSource{root: root}
	head, _, err := source.headAndBase(ctx)
	if err != nil {
		return "", err
	}
	return source.branch(ctx, head)
}
