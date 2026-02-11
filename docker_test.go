// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import "testing"

func TestConvertGitURLToHTTPS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https", "https://github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"git_at", "git@github.com:user/repo.git", "https://github.com/user/repo.git"},
		{"ssh_git", "ssh://git@github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"git_proto", "git://github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"unknown", "unknown://foo", "unknown://foo"},
		{"whitespace", "  git@github.com:user/repo.git  ", "https://github.com/user/repo.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convertGitURLToHTTPS(tt.in); got != tt.want {
				t.Errorf("convertGitURLToHTTPS(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
