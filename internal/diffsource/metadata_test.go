package diffsource

import "testing"

func TestSanitizeRemoteCredentials(t *testing.T) {
	for _, test := range []struct{ input, expected string }{
		{"https://user:password@example.com/org/repo.git?token=secret#fragment", "https://example.com/org/repo.git"},
		{"ssh://git@example.com/org/repo.git", "ssh://example.com/org/repo.git"},
		{"git@example.com:org/repo.git", "example.com:org/repo.git"},
		{"file:///tmp/repo.git?token=secret", "file:///tmp/repo.git"},
		{"/tmp/repo.git", "/tmp/repo.git"},
	} {
		if result := sanitizeRemote(test.input); result != test.expected {
			t.Errorf("sanitize %q: got %q want %q", test.input, result, test.expected)
		}
	}
}
