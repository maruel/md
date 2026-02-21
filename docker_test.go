// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"os"
	"path/filepath"
	"testing"
)

func setupContextSHAHash(t *testing.T) (buildCtx, keysDir string) {
	buildCtx = t.TempDir()
	keysDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(buildCtx, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, content string }{
		{"a.txt", "aaa"},
		{"b.txt", "bbb"},
		{"sub/c.txt", "ccc"},
	} {
		if err := os.WriteFile(filepath.Join(buildCtx, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []struct{ name, content string }{
		{"ssh_host_ed25519_key", "hostkey"},
		{"ssh_host_ed25519_key.pub", "hostkey.pub"},
		{"authorized_keys", "authkeys"},
	} {
		if err := os.WriteFile(filepath.Join(keysDir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return buildCtx, keysDir
}

func TestContextSHAHash(t *testing.T) {
	t.Run("valid_hex", func(t *testing.T) {
		buildCtx, keysDir := setupContextSHAHash(t)
		got, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 64 {
			t.Fatalf("expected 64-char hex string, got %q", got)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		buildCtx, keysDir := setupContextSHAHash(t)
		got1, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		got2, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if got1 != got2 {
			t.Fatalf("expected deterministic hash, got %q then %q", got1, got2)
		}
	})

	t.Run("sensitive_to_change", func(t *testing.T) {
		buildCtx, keysDir := setupContextSHAHash(t)
		before, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(buildCtx, "a.txt"), []byte("modified"), 0o644); err != nil {
			t.Fatal(err)
		}
		after, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Fatal("hash should differ after modifying a file")
		}
	})

	t.Run("missing_key_file", func(t *testing.T) {
		buildCtx := t.TempDir()
		keysDir := t.TempDir()
		_, err := contextSHAHash(buildCtx, keysDir)
		if err == nil {
			t.Fatal("expected error for missing key files")
		}
	})
}

func TestEmbeddedContextSHA(t *testing.T) {
	keysDir := t.TempDir()
	for _, f := range []struct{ name, content string }{
		{"ssh_host_ed25519_key", "hostkey"},
		{"ssh_host_ed25519_key.pub", "hostkey.pub"},
		{"authorized_keys", "authkeys"},
	} {
		if err := os.WriteFile(filepath.Join(keysDir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("matches_contextSHAHash", func(t *testing.T) {
		buildCtx, err := prepareBuildContext()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.RemoveAll(buildCtx) }()
		diskSHA, err := contextSHAHash(buildCtx, keysDir)
		if err != nil {
			t.Fatal(err)
		}
		embeddedSHA, err := embeddedContextSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if diskSHA != embeddedSHA {
			t.Fatalf("embeddedContextSHA=%q != contextSHAHash=%q", embeddedSHA, diskSHA)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		got1, err := embeddedContextSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		got2, err := embeddedContextSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if got1 != got2 {
			t.Fatalf("expected deterministic hash, got %q then %q", got1, got2)
		}
	})
}

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
