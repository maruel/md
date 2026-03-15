// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package md

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type githubCreateTokenReq struct {
	Name           string            `json:"name"`
	ExpirationDays int               `json:"expiration_days"`
	Permissions    map[string]string `json:"permissions"`
}

type githubAPIError struct {
	Message string `json:"message"`
}

type githubCreateTokenResp struct {
	Token string `json:"token"`
}

// CreateReadOnlyGithubToken creates a fine-grained GitHub personal access
// token with read-only permissions via the REST API. The returned token
// expires in one day (the minimum GitHub allows).
//
// Requires the parent token to have the manage:write:personal_access_tokens
// scope (classic PAT) or personal_access_tokens:write permission (fine-grained
// PAT). Returns an error if the API call fails or the parent token lacks the
// required permission.
func CreateReadOnlyGithubToken(ctx context.Context, parentToken string) (string, error) {
	body, err := json.Marshal(githubCreateTokenReq{
		Name:           fmt.Sprintf("md-ro-%d", time.Now().Unix()),
		ExpirationDays: 1,
		Permissions: map[string]string{
			// metadata is required for all fine-grained tokens.
			"metadata":      "read",
			"contents":      "read",
			"issues":        "read",
			"pull_requests": "read",
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.github.com/user/personal-access-tokens",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+parentToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading github API response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		var apiErr githubAPIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return "", fmt.Errorf("github API returned %d: %s", resp.StatusCode, apiErr.Message)
		}
		return "", fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var result githubCreateTokenResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing github API response: %w", err)
	}
	if result.Token == "" {
		return "", errors.New("github API returned empty token")
	}
	return result.Token, nil
}
