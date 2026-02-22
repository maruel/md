// Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"os"
	"path/filepath"
	"strings"
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
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
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

func TestAppendCacheLayers(t *testing.T) {
	t.Run("existing_cache_appended", func(t *testing.T) {
		// Create a fake Dockerfile and a fake host cache dir.
		tmpDir := t.TempDir()
		dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cacheDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(cacheDir, "file.txt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}

		caches := []CacheMount{{
			Name:          "mycache",
			HostPath:      cacheDir,
			ContainerPath: "/home/user/.cache/myapp",
		}}
		extraArgs, activeKey, err := appendCacheLayers(dockerfilePath, caches, "/home/user", nil)
		if err != nil {
			t.Fatal(err)
		}

		// Should return --build-context args.
		if len(extraArgs) != 2 || extraArgs[0] != "--build-context" || extraArgs[1] != "mycache="+cacheDir {
			t.Errorf("extraArgs = %v, want [--build-context mycache=%s]", extraArgs, cacheDir)
		}
		// activeKey must be non-empty (cache dir exists).
		if activeKey == "" {
			t.Error("activeKey should be non-empty when cache dir exists")
		}

		// The Dockerfile should have the COPY instruction appended.
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatal(err)
		}
		got := string(content)
		if !strings.Contains(got, "COPY --chown=user:user --from=mycache / /home/user/.cache/myapp/") {
			t.Errorf("Dockerfile missing COPY instruction:\n%s", got)
		}
	})

	t.Run("missing_cache_skipped", func(t *testing.T) {
		tmpDir := t.TempDir()
		dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		caches := []CacheMount{{
			Name:          "missing",
			HostPath:      "/nonexistent/path/that/does/not/exist",
			ContainerPath: "/home/user/.cache/missing",
		}}
		extraArgs, activeKey, err := appendCacheLayers(dockerfilePath, caches, "/home/user", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Missing cache produces no extra args and empty active key.
		if len(extraArgs) != 0 {
			t.Errorf("extraArgs = %v, want empty", extraArgs)
		}
		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\" for missing cache", activeKey)
		}
		// Dockerfile should not have a COPY instruction.
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "COPY --from=missing") {
			t.Error("Dockerfile should not contain COPY for missing cache")
		}
	})

	t.Run("mount_paths_mkdir", func(t *testing.T) {
		tmpDir := t.TempDir()
		dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		mountPaths := []string{"/home/user/.amp", "/home/user/.claude"}
		extraArgs, activeKey, err := appendCacheLayers(dockerfilePath, nil, "/home/user", mountPaths)
		if err != nil {
			t.Fatal(err)
		}
		if len(extraArgs) != 0 {
			t.Errorf("extraArgs = %v, want empty", extraArgs)
		}
		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\" when no caches", activeKey)
		}
		content, err := os.ReadFile(dockerfilePath)
		if err != nil {
			t.Fatal(err)
		}
		got := string(content)
		if !strings.Contains(got, "mkdir -p") {
			t.Error("Dockerfile should contain mkdir -p for mount paths")
		}
		if !strings.Contains(got, "/home/user/.amp") {
			t.Error("Dockerfile should contain /home/user/.amp")
		}
		if !strings.Contains(got, "/home/user/.claude") {
			t.Error("Dockerfile should contain /home/user/.claude")
		}
	})

	t.Run("no_caches_no_mount_paths_returns_nil", func(t *testing.T) {
		tmpDir := t.TempDir()
		dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		extraArgs, activeKey, err := appendCacheLayers(dockerfilePath, nil, "/home/user", nil)
		if err != nil {
			t.Fatal(err)
		}
		if extraArgs != nil {
			t.Errorf("extraArgs = %v, want nil", extraArgs)
		}
		if activeKey != "" {
			t.Errorf("activeKey = %q, want \"\"", activeKey)
		}
	})

	t.Run("activeKey_differs_from_requested_when_dir_missing", func(t *testing.T) {
		// This is the core correctness invariant: if a requested cache dir does
		// not exist on the host, activeKey must differ from cacheSpecKey of the
		// requested set. imageBuildNeeded will then trigger a rebuild the next
		// time the dir exists, rather than falsely reporting "up to date".
		tmpDir := t.TempDir()
		dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		requested := []CacheMount{{
			Name:          "missing",
			HostPath:      "/nonexistent/path",
			ContainerPath: "/home/user/.cache/missing",
		}}
		_, activeKey, err := appendCacheLayers(dockerfilePath, requested, "/home/user", nil)
		if err != nil {
			t.Fatal(err)
		}
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
