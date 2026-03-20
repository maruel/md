// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}
	for _, tt := range tests {
		if got := FormatBytes(tt.in); got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{1234567, "1,234,567"},
		{1000000000, "1,000,000,000"},
	}
	for _, tt := range tests {
		if got := formatCount(tt.in); got != tt.want {
			t.Errorf("formatCount(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveHostPath(t *testing.T) {
	tests := []struct {
		path, home, want string
	}{
		{"~/go/pkg/mod", "/home/alice", "/home/alice/go/pkg/mod"},
		{"~/.cargo/registry", "/home/alice", "/home/alice/.cargo/registry"},
		{"/absolute/path", "/home/alice", "/absolute/path"},
		{"/no/tilde", "/home/alice", "/no/tilde"},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct{ path, home, want string }{`~\go\pkg\mod`, `C:\Users\alice`, `C:/Users/alice/go/pkg/mod`})
	}
	for _, tt := range tests {
		if got := resolveHostPath(tt.path, tt.home); got != tt.want {
			t.Errorf("resolveHostPath(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
		}
	}
}

func TestDirStats(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name, content string
	}{
		{"a.txt", "hello"},
		{"b.txt", "world!"},
		{"sub/c.txt", "foo"},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gotFiles, gotBytes := dirStats(dir)
	if gotFiles != 3 {
		t.Errorf("dirStats files = %d, want 3", gotFiles)
	}
	// "hello"=5, "world!"=6, "foo"=3 → 14
	if gotBytes != 14 {
		t.Errorf("dirStats bytes = %d, want 14", gotBytes)
	}
	// Non-existent dir returns zeros.
	f, b := dirStats(filepath.Join(dir, "nonexistent"))
	if f != 0 || b != 0 {
		t.Errorf("dirStats(nonexistent) = (%d, %d), want (0, 0)", f, b)
	}
}

func TestKeysSHA(t *testing.T) {
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

	t.Run("deterministic", func(t *testing.T) {
		got1, err := keysSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		got2, err := keysSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if got1 != got2 {
			t.Fatalf("expected deterministic hash, got %q then %q", got1, got2)
		}
	})

	t.Run("changes_with_keys", func(t *testing.T) {
		sha1, err := keysSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keysDir, "authorized_keys"), []byte("different"), 0o644); err != nil {
			t.Fatal(err)
		}
		sha2, err := keysSHA(keysDir)
		if err != nil {
			t.Fatal(err)
		}
		if sha1 == sha2 {
			t.Error("keysSHA should change when key content changes")
		}
	})
}

func TestCacheSpecKey(t *testing.T) {
	t.Run("nil_returns_empty", func(t *testing.T) {
		if got := cacheSpecKey(nil); got != "" {
			t.Errorf("cacheSpecKey(nil) = %q, want \"\"", got)
		}
	})
	t.Run("empty_returns_empty", func(t *testing.T) {
		if got := cacheSpecKey([]CacheMount{}); got != "" {
			t.Errorf("cacheSpecKey([]) = %q, want \"\"", got)
		}
	})
	t.Run("non_empty_returns_hex", func(t *testing.T) {
		cm := []CacheMount{{Name: "go-mod", HostPath: "~/go/pkg/mod", ContainerPath: "/home/user/go/pkg/mod"}}
		got := cacheSpecKey(cm)
		if len(got) != 16 {
			t.Errorf("cacheSpecKey len = %d, want 16", len(got))
		}
	})
	t.Run("order_independent", func(t *testing.T) {
		a := []CacheMount{
			{Name: "go-mod", ContainerPath: "/home/user/go/pkg/mod"},
			{Name: "go-build", ContainerPath: "/home/user/.cache/go-build"},
		}
		b := []CacheMount{
			{Name: "go-build", ContainerPath: "/home/user/.cache/go-build"},
			{Name: "go-mod", ContainerPath: "/home/user/go/pkg/mod"},
		}
		if cacheSpecKey(a) != cacheSpecKey(b) {
			t.Error("cacheSpecKey should be order-independent")
		}
	})
	t.Run("different_specs_differ", func(t *testing.T) {
		a := cacheSpecKey([]CacheMount{{Name: "go-mod", ContainerPath: "/home/user/go/pkg/mod"}})
		b := cacheSpecKey([]CacheMount{{Name: "cargo", ContainerPath: "/home/user/.cargo/registry"}})
		if a == b {
			t.Error("different caches should produce different keys")
		}
	})
}

func TestResolveCaches(t *testing.T) {
	t.Run("existing_cache_resolved", func(t *testing.T) {
		cacheDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(cacheDir, "file.txt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}

		caches := []CacheMount{{
			Name:          "mycache",
			HostPath:      cacheDir,
			ContainerPath: "/home/user/.cache/myapp",
		}}
		active, dirs, activeKey := resolveCaches(caches, "/home/user", nil)

		if len(active) != 1 || active[0].cm.Name != "mycache" {
			t.Errorf("active = %v, want 1 entry for mycache", active)
		}
		if activeKey == "" {
			t.Error("activeKey should be non-empty when cache dir exists")
		}
		// Should include the cache container path and its intermediary.
		if !slices.Contains(dirs, "/home/user/.cache/myapp") {
			t.Errorf("dirs = %v, want to contain /home/user/.cache/myapp", dirs)
		}
	})

	t.Run("missing_cache_skipped", func(t *testing.T) {
		caches := []CacheMount{{
			Name:          "missing",
			HostPath:      "/nonexistent/path/that/does/not/exist",
			ContainerPath: "/home/user/.cache/missing",
		}}
		active, _, activeKey := resolveCaches(caches, "/home/user", nil)

		if len(active) != 0 {
			t.Errorf("active = %v, want empty", active)
		}
		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\" for missing cache", activeKey)
		}
	})

	t.Run("mount_paths_included", func(t *testing.T) {
		mountPaths := []string{"/home/user/.amp", "/home/user/.claude"}
		_, dirs, activeKey := resolveCaches(nil, "/home/user", mountPaths)

		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\" when no caches", activeKey)
		}
		for _, want := range mountPaths {
			if !slices.Contains(dirs, want) {
				t.Errorf("dirs = %v, want to contain %s", dirs, want)
			}
		}
	})

	t.Run("no_caches_no_mount_paths", func(t *testing.T) {
		active, dirs, activeKey := resolveCaches(nil, "/home/user", nil)
		if len(active) != 0 {
			t.Errorf("active = %v, want empty", active)
		}
		if len(dirs) != 0 {
			t.Errorf("dirs = %v, want empty", dirs)
		}
		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\"", activeKey)
		}
	})

	t.Run("activeKey_differs_from_requested_when_dir_missing", func(t *testing.T) {
		requested := []CacheMount{{
			Name:          "missing",
			HostPath:      "/nonexistent/path",
			ContainerPath: "/home/user/.cache/missing",
		}}
		_, _, activeKey := resolveCaches(requested, "/home/user", nil)
		requestedKey := cacheSpecKey(requested)
		if activeKey == requestedKey {
			t.Errorf("activeKey %q should differ from requestedKey %q when host dir is missing", activeKey, requestedKey)
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
