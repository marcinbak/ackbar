package daemon

import (
	"testing"
)

func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "git@github.com:mode/app.git",
			expected: "github.com/mode/app",
		},
		{
			input:    "https://github.com/Mode/app",
			expected: "github.com/mode/app",
		},
		{
			input:    "http://gitlab.com/user/project.git/",
			expected: "gitlab.com/user/project",
		},
		{
			input:    "ssh://git@github.com/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			input:    "",
			expected: "",
		},
		{
			input:    "   https://github.com/mode/app.git   ",
			expected: "github.com/mode/app",
		},
	}

	for _, tc := range tests {
		got := NormalizeGitURL(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeGitURL(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
