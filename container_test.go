// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"simple", "simple", "simple"},
		{"spaces", "hello world", "'hello world'"},
		{"single_quote", "it's", `'it'\''s'`},
		{"multiple_quotes", "a'b'c", `'a'\''b'\''c'`},
		{"safe_path", "safe-path/to_file.txt", "safe-path/to_file.txt"},
		{"with_spaces", "with spaces", "'with spaces'"},
		{"semicolon", "with;semi", "'with;semi'"},
		{"dollar_cmd", "$(cmd)", "'$(cmd)'"},
		{"backslash", `back\slash`, `'back\slash'`},
		{"newline", "hello\nworld", "'hello\nworld'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnmarshalContainer(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15 10:30:00 +0000 UTC"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if ct.Name != "md-repo-main" {
			t.Errorf("ContainerName = %q, want %q", ct.Name, "md-repo-main")
		}
		if ct.State != "running" {
			t.Errorf("State = %q, want %q", ct.State, "running")
		}
		wantTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		if !ct.CreatedAt.Equal(wantTime) {
			t.Errorf("CreatedAt = %v, want %v", ct.CreatedAt, wantTime)
		}
		if time.Since(ct.CreatedAt) <= 0 {
			t.Error("CreatedAt is in the future")
		}
	})
	t.Run("with_labels", func(t *testing.T) {
		reposData, _ := json.Marshal([]Repo{{GitRoot: "/home/user/repo", Branch: "main"}})
		reposB64 := base64.StdEncoding.EncodeToString(reposData)
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15 10:30:00 +0000 UTC","Labels":"md.repos=` + reposB64 + `,other=ignored"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(ct.Repos) != 1 {
			t.Fatalf("len(Repos) = %d, want 1", len(ct.Repos))
		}
		if ct.Repos[0].GitRoot != "/home/user/repo" {
			t.Errorf("Repos[0].GitRoot = %q, want %q", ct.Repos[0].GitRoot, "/home/user/repo")
		}
		if ct.Repos[0].Branch != "main" {
			t.Errorf("Repos[0].Branch = %q, want %q", ct.Repos[0].Branch, "main")
		}
	})
	t.Run("no_labels", func(t *testing.T) {
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15 10:30:00 +0000 UTC","Labels":""}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if ct.Repos != nil {
			t.Errorf("expected nil Repos, got %v", ct.Repos)
		}
	})
	t.Run("empty_repos", func(t *testing.T) {
		// No-repo containers encode md.repos as an empty JSON array.
		reposData, _ := json.Marshal([]Repo{})
		reposB64 := base64.StdEncoding.EncodeToString(reposData)
		raw := `{"Names":"md-agent","State":"running","CreatedAt":"2025-06-15 10:30:00 +0000 UTC","Labels":"md.repos=` + reposB64 + `"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(ct.Repos) != 0 {
			t.Errorf("expected empty Repos, got %v", ct.Repos)
		}
	})
	t.Run("podman_rfc3339", func(t *testing.T) {
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15T10:30:00.123456789Z"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		wantTime := time.Date(2025, 6, 15, 10, 30, 0, 123456789, time.UTC)
		if !ct.CreatedAt.Equal(wantTime) {
			t.Errorf("CreatedAt = %v, want %v", ct.CreatedAt, wantTime)
		}
	})
	t.Run("podman_rfc3339_no_frac", func(t *testing.T) {
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15T10:30:00Z"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		wantTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		if !ct.CreatedAt.Equal(wantTime) {
			t.Errorf("CreatedAt = %v, want %v", ct.CreatedAt, wantTime)
		}
	})
	t.Run("podman_rfc3339_offset", func(t *testing.T) {
		raw := `{"Names":"md-repo-main","State":"running","CreatedAt":"2025-06-15T10:30:00+02:00"}`
		ct, err := unmarshalContainer([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		wantTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.FixedZone("", 2*60*60))
		if !ct.CreatedAt.Equal(wantTime) {
			t.Errorf("CreatedAt = %v, want %v", ct.CreatedAt, wantTime)
		}
	})
	t.Run("bad_created_at", func(t *testing.T) {
		raw := `{"Names":"x","State":"running","CreatedAt":"not-a-date"}`
		_, err := unmarshalContainer([]byte(raw))
		if err == nil {
			t.Fatal("expected error for bad CreatedAt")
		}
	})
}

func TestParseStatsLine(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		line := `{"Name":"md-repo-main","CPUPerc":"1.23%","MemUsage":"150MiB / 7.5GiB","MemPerc":"1.95%","PIDs":"12","NetIO":"1.5kB / 500B","BlockIO":"10MB / 2MB"}`
		s, name, err := parseStatsLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if name != "md-repo-main" {
			t.Errorf("name = %q, want %q", name, "md-repo-main")
		}
		if s.CPUPerc != 1.23 {
			t.Errorf("CPUPerc = %v, want 1.23", s.CPUPerc)
		}
		if s.MemUsed != 150<<20 {
			t.Errorf("MemUsed = %d, want %d", s.MemUsed, 150<<20)
		}
		if s.PIDs != 12 {
			t.Errorf("PIDs = %d, want 12", s.PIDs)
		}
	})
	t.Run("na_values", func(t *testing.T) {
		// docker stats returns N/A when cgroup metrics are unavailable (e.g. DinD).
		line := `{"Name":"md-repo-main","CPUPerc":"N/A","MemUsage":"N/A / N/A","MemPerc":"N/A","PIDs":"N/A","NetIO":"N/A / N/A","BlockIO":"N/A / N/A"}`
		s, name, err := parseStatsLine(line)
		if err != nil {
			t.Fatalf("N/A values should not cause an error, got: %v", err)
		}
		if name != "md-repo-main" {
			t.Errorf("name = %q, want %q", name, "md-repo-main")
		}
		if s.CPUPerc != 0 || s.MemUsed != 0 || s.MemLimit != 0 || s.NetRx != 0 || s.NetTx != 0 {
			t.Errorf("expected all-zero stats for N/A, got %+v", s)
		}
	})
}

func TestParseCreatedAt(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"docker", "2025-06-15 10:30:00 +0000 UTC", false},
		{"docker_with_tz", "2025-06-15 10:30:00 -0700 MST", false},
		{"podman_rfc3339nano", "2025-06-15T10:30:00.123456789Z", false},
		{"podman_rfc3339", "2025-06-15T10:30:00Z", false},
		{"podman_rfc3339_offset", "2025-06-15T10:30:00+02:00", false},
		{"invalid", "not-a-date", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCreatedAt(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCreatedAt(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
		})
	}
}
