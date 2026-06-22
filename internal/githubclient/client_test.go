package githubclient

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// --- isFullSHA ---

func TestIsFullSHA(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"1234567890abcdef1234567890abcdef12345678", true},
		{"abcdefabcdefabcdefabcdefabcdefabcdefabcd", true},
		{"ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"1234567890abcdef1234567890abcdef1234567", false},
		{"1234567890abcdef1234567890abcdef123456789", false},
		{"v4", false},
		{"main", false},
		{"", false},
		{"1234567890abcdef1234567890abcdef1234567g", false},
		{"1234567890abcdef1234567890abcdef1234567!", false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := isFullSHA(tt.ref)
			if got != tt.want {
				t.Errorf("isFullSHA(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

// --- parseTagVersion ---

func TestParseTagVersion(t *testing.T) {
	tests := []struct {
		name        string
		wantMajor   int
		wantMinor   int
		wantPatch   int
	}{
		{"v1.2.3", 1, 2, 3},
		{"v4.0.0", 4, 0, 0},
		{"v0.1.0", 0, 1, 0},
		{"v10.20.30", 10, 20, 30},
		{"1.2.3", 1, 2, 3},
		{"v1.2", 0, 0, 0},
		{"v1", 0, 0, 0},
		{"latest", 0, 0, 0},
		{"", 0, 0, 0},
		{"v1.2.3.4", 1, 2, 0},
		{"v1.2.3-beta", 1, 2, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTagVersion(tt.name)
			if got.major != tt.wantMajor {
				t.Errorf("parseTagVersion(%q).major = %d, want %d", tt.name, got.major, tt.wantMajor)
			}
			if got.minor != tt.wantMinor {
				t.Errorf("parseTagVersion(%q).minor = %d, want %d", tt.name, got.minor, tt.wantMinor)
			}
			if got.patch != tt.wantPatch {
				t.Errorf("parseTagVersion(%q).patch = %d, want %d", tt.name, got.patch, tt.wantPatch)
			}
		})
	}
}

// --- sortTags ---

func TestSortTags(t *testing.T) {
	tests := []struct {
		name string
		tags []Tag
		want []string
	}{
		{
			name: "sorts by semver descending",
			tags: []Tag{
				{Name: "v1.0.0", CommitSHA: "aaa"},
				{Name: "v2.0.0", CommitSHA: "bbb"},
				{Name: "v1.5.0", CommitSHA: "ccc"},
			},
			want: []string{"v2.0.0", "v1.5.0", "v1.0.0"},
		},
		{
			name: "handles major version differences",
			tags: []Tag{
				{Name: "v3.0.0", CommitSHA: "aaa"},
				{Name: "v1.0.0", CommitSHA: "bbb"},
				{Name: "v2.0.0", CommitSHA: "ccc"},
			},
			want: []string{"v3.0.0", "v2.0.0", "v1.0.0"},
		},
		{
			name: "handles patch version differences",
			tags: []Tag{
				{Name: "v1.0.0", CommitSHA: "aaa"},
				{Name: "v1.0.2", CommitSHA: "bbb"},
				{Name: "v1.0.1", CommitSHA: "ccc"},
			},
			want: []string{"v1.0.2", "v1.0.1", "v1.0.0"},
		},
		{
			name: "handles empty slice",
			tags: []Tag{},
			want: []string{},
		},
		{
			name: "handles single element",
			tags: []Tag{{Name: "v1.0.0", CommitSHA: "aaa"}},
			want: []string{"v1.0.0"},
		},
		{
			name: "handles non-semver tags",
			tags: []Tag{
				{Name: "latest", CommitSHA: "aaa"},
				{Name: "v1.0.0", CommitSHA: "bbb"},
			},
			want: []string{"v1.0.0", "latest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortTags(tt.tags)
			got := make([]string, len(tt.tags))
			for i, tag := range tt.tags {
				got[i] = tag.Name
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sortTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Mock client for testing ---

type mockClient struct {
	tags       []Tag
	commitDate map[string]map[string]CommitDateResult
	resolveRef map[string]string
	errors     map[string]error
}

type CommitDateResult struct {
	Time string
	OK   bool
	Err  error
}

func (m *mockClient) ListTags(ctx context.Context, owner, repo string) ([]Tag, error) {
	key := owner + "/" + repo
	if err, ok := m.errors["ListTags:"+key]; ok {
		return nil, err
	}
	return m.tags, nil
}

func (m *mockClient) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	key := owner + "/" + repo
	if repoMap, ok := m.commitDate[key]; ok {
		if result, ok := repoMap[sha]; ok {
			t, _ := time.Parse(time.RFC3339, result.Time)
			return t, result.OK, result.Err
		}
	}
	return time.Time{}, false, nil
}

func (m *mockClient) ResolveRef(ctx context.Context, owner, repo, ref string) (string, error) {
	key := owner + "/" + repo + "@" + ref
	if err, ok := m.errors["ResolveRef:"+key]; ok {
		return "", err
	}
	if sha, ok := m.resolveRef[key]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("ref %q not found", ref)
}

var _ Client = (*mockClient)(nil)
