package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client provides GitHub API operations for coding agents.
type Client struct {
	token  string
	client *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token:  token,
		client: &http.Client{},
	}
}

func (c *Client) do(ctx context.Context, method, url string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github api %s %s: %d %s", method, url, resp.StatusCode, string(data))
	}

	return data, nil
}

// RepoInfo returns basic repository information.
type RepoInfo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
}

func (c *Client) GetRepo(ctx context.Context, repo string) (*RepoInfo, error) {
	data, err := c.do(ctx, "GET", "/repos/"+repo, nil)
	if err != nil {
		return nil, err
	}
	var info RepoInfo
	return &info, json.Unmarshal(data, &info)
}

// TreeEntry represents a file in the repo tree.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

// BrowseRepo returns the full file tree of a repository.
func (c *Client) BrowseRepo(ctx context.Context, repo, branch string) ([]TreeEntry, error) {
	data, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/git/trees/%s?recursive=1", repo, branch), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tree []TreeEntry `json:"tree"`
	}
	return result.Tree, json.Unmarshal(data, &result)
}

// FileContent represents a file read from the repo.
type FileContent struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	Content string `json:"content"`
	Size    int    `json:"size"`
}

// ReadFile reads a file from the repo and decodes its content.
func (c *Client) ReadFile(ctx context.Context, repo, path, ref string) (*FileContent, error) {
	url := fmt.Sprintf("/repos/%s/contents/%s", repo, path)
	if ref != "" {
		url += "?ref=" + ref
	}
	data, err := c.do(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Path    string `json:"path"`
		SHA     string `json:"sha"`
		Content string `json:"content"`
		Size    int    `json:"size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(raw.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	return &FileContent{
		Path:    raw.Path,
		SHA:     raw.SHA,
		Content: string(decoded),
		Size:    raw.Size,
	}, nil
}

// CreateBranch creates a new branch from a base branch.
func (c *Client) CreateBranch(ctx context.Context, repo, baseBranch, newBranch string) error {
	// Get base branch SHA
	refData, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/git/ref/heads/%s", repo, baseBranch), nil)
	if err != nil {
		return fmt.Errorf("get base ref: %w", err)
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(refData, &ref); err != nil {
		return err
	}

	_, err = c.do(ctx, "POST", fmt.Sprintf("/repos/%s/git/refs", repo), map[string]string{
		"ref": "refs/heads/" + newBranch,
		"sha": ref.Object.SHA,
	})
	return err
}

// WriteFile creates or updates a file in the repo.
func (c *Client) WriteFile(ctx context.Context, repo, path, branch, content, message, sha string) error {
	body := map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if sha != "" {
		body["sha"] = sha
	}
	_, err := c.do(ctx, "PUT", fmt.Sprintf("/repos/%s/contents/%s", repo, path), body)
	return err
}

// MultiFileCommit creates a single commit with multiple file changes using Git Data API.
type FileChange struct {
	Path    string  `json:"path"`
	Content *string `json:"content"` // nil = delete
}

func (c *Client) MultiFileCommit(ctx context.Context, repo, branch, message string, files []FileChange) (string, error) {
	// 1. Get current commit SHA
	refData, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/git/ref/heads/%s", repo, branch), nil)
	if err != nil {
		return "", err
	}
	var ref struct {
		Object struct{ SHA string `json:"sha"` } `json:"object"`
	}
	json.Unmarshal(refData, &ref)

	// 2. Get current tree
	commitData, err := c.do(ctx, "GET", fmt.Sprintf("/repos/%s/git/commits/%s", repo, ref.Object.SHA), nil)
	if err != nil {
		return "", err
	}
	var commit struct {
		Tree struct{ SHA string `json:"sha"` } `json:"tree"`
	}
	json.Unmarshal(commitData, &commit)

	// 3. Create blobs and build tree
	type treeItem struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"`
	}
	var items []treeItem

	for _, f := range files {
		if f.Content == nil {
			items = append(items, treeItem{Path: f.Path, Mode: "100644", Type: "blob", SHA: nil})
			continue
		}
		blobData, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/git/blobs", repo), map[string]string{
			"content":  *f.Content,
			"encoding": "utf-8",
		})
		if err != nil {
			return "", fmt.Errorf("create blob %s: %w", f.Path, err)
		}
		var blob struct{ SHA string `json:"sha"` }
		json.Unmarshal(blobData, &blob)
		sha := blob.SHA
		items = append(items, treeItem{Path: f.Path, Mode: "100644", Type: "blob", SHA: &sha})
	}

	// 4. Create tree
	treeData, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/git/trees", repo), map[string]interface{}{
		"base_tree": commit.Tree.SHA,
		"tree":      items,
	})
	if err != nil {
		return "", fmt.Errorf("create tree: %w", err)
	}
	var tree struct{ SHA string `json:"sha"` }
	json.Unmarshal(treeData, &tree)

	// 5. Create commit
	newCommitData, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/git/commits", repo), map[string]interface{}{
		"message": message,
		"tree":    tree.SHA,
		"parents": []string{ref.Object.SHA},
	})
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}
	var newCommit struct{ SHA string `json:"sha"` }
	json.Unmarshal(newCommitData, &newCommit)

	// 6. Update branch ref
	_, err = c.do(ctx, "PATCH", fmt.Sprintf("/repos/%s/git/refs/heads/%s", repo, branch), map[string]string{
		"sha": newCommit.SHA,
	})
	return newCommit.SHA, err
}

// CreatePR creates a pull request.
type PRResult struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
}

func (c *Client) CreatePR(ctx context.Context, repo, title, body, head, base string) (*PRResult, error) {
	data, err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/pulls", repo), map[string]string{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	})
	if err != nil {
		return nil, err
	}
	var pr PRResult
	return &pr, json.Unmarshal(data, &pr)
}

// GetPRDiff returns the diff of a pull request.
func (c *Client) GetPRDiff(ctx context.Context, repo string, prNumber int) (string, error) {
	url := fmt.Sprintf("/repos/%s/pulls/%d", repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com"+url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data), nil
}

// SearchCode searches for code patterns in a repo.
func (c *Client) SearchCode(ctx context.Context, repo, query string) ([]string, error) {
	q := fmt.Sprintf("%s repo:%s", query, repo)
	data, err := c.do(ctx, "GET", fmt.Sprintf("/search/code?q=%s&per_page=10", q), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	json.Unmarshal(data, &result)
	var paths []string
	for _, item := range result.Items {
		paths = append(paths, item.Path)
	}
	return paths, nil
}
