package githubclient

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

// --- resolveGitObjectToCommit ---

func TestResolveGitObjectToCommit(t *testing.T) {
	t.Run("returns commit as-is", func(t *testing.T) {
		sha, err := resolveGitObjectToCommit("abc123", "commit", func(string) (string, string, error) {
			t.Fatal("resolveTag should not be called for commit objects")
			return "", "", nil
		})
		if err != nil {
			t.Fatalf("resolveGitObjectToCommit() error = %v", err)
		}
		if sha != "abc123" {
			t.Fatalf("resolveGitObjectToCommit() = %q, want %q", sha, "abc123")
		}
	})

	t.Run("treats empty type as commit", func(t *testing.T) {
		sha, err := resolveGitObjectToCommit("def456", "", func(string) (string, string, error) {
			t.Fatal("resolveTag should not be called when type is empty")
			return "", "", nil
		})
		if err != nil {
			t.Fatalf("resolveGitObjectToCommit() error = %v", err)
		}
		if sha != "def456" {
			t.Fatalf("resolveGitObjectToCommit() = %q, want %q", sha, "def456")
		}
	})

	t.Run("dereferences annotated tag to commit", func(t *testing.T) {
		sha, err := resolveGitObjectToCommit("tag-sha", "tag", func(tagSHA string) (string, string, error) {
			if tagSHA != "tag-sha" {
				t.Fatalf("resolveTag called with %q, want %q", tagSHA, "tag-sha")
			}
			return "commit-sha", "commit", nil
		})
		if err != nil {
			t.Fatalf("resolveGitObjectToCommit() error = %v", err)
		}
		if sha != "commit-sha" {
			t.Fatalf("resolveGitObjectToCommit() = %q, want %q", sha, "commit-sha")
		}
	})

	t.Run("supports nested tags", func(t *testing.T) {
		calls := 0
		sha, err := resolveGitObjectToCommit("tag-1", "tag", func(tagSHA string) (string, string, error) {
			calls++
			switch tagSHA {
			case "tag-1":
				return "tag-2", "tag", nil
			case "tag-2":
				return "commit-1", "commit", nil
			default:
				return "", "", fmt.Errorf("unexpected tag sha %q", tagSHA)
			}
		})
		if err != nil {
			t.Fatalf("resolveGitObjectToCommit() error = %v", err)
		}
		if sha != "commit-1" {
			t.Fatalf("resolveGitObjectToCommit() = %q, want %q", sha, "commit-1")
		}
		if calls != 2 {
			t.Fatalf("resolveTag call count = %d, want %d", calls, 2)
		}
	})

	t.Run("errors on unsupported object type", func(t *testing.T) {
		_, err := resolveGitObjectToCommit("blob-sha", "blob", func(string) (string, string, error) {
			return "", "", nil
		})
		if err == nil || !strings.Contains(err.Error(), "unsupported git object type") {
			t.Fatalf("expected unsupported type error, got %v", err)
		}
	})

	t.Run("propagates tag resolution error", func(t *testing.T) {
		wantErr := errors.New("network failed")
		_, err := resolveGitObjectToCommit("tag-sha", "tag", func(string) (string, string, error) {
			return "", "", wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected wrapped error %v, got %v", wantErr, err)
		}
	})

	t.Run("errors when dereference depth exceeded", func(t *testing.T) {
		_, err := resolveGitObjectToCommit("tag-0", "tag", func(tagSHA string) (string, string, error) {
			return tagSHA + "-next", "tag", nil
		})
		if err == nil || !strings.Contains(err.Error(), "exceeded maximum tag dereference depth") {
			t.Fatalf("expected depth error, got %v", err)
		}
	})
}

func TestExtractGraphQLCommit(t *testing.T) {
	t.Run("commit target", func(t *testing.T) {
		obj := graphQLGitObject{
			TypeName:      "Commit",
			OID:           "abc123",
			CommittedDate: "2026-01-02T03:04:05Z",
		}

		sha, when, ok := extractGraphQLCommit(obj)
		if !ok {
			t.Fatal("expected ok")
		}
		if sha != "abc123" {
			t.Fatalf("sha = %q, want %q", sha, "abc123")
		}
		if when.IsZero() {
			t.Fatal("expected parsed commit date")
		}
	})

	t.Run("annotated tag points to commit", func(t *testing.T) {
		obj := graphQLGitObject{
			TypeName: "Tag",
			Target: &graphQLGitObject{
				TypeName:      "Commit",
				OID:           "def456",
				CommittedDate: "2026-02-03T04:05:06Z",
			},
		}

		sha, _, ok := extractGraphQLCommit(obj)
		if !ok {
			t.Fatal("expected ok")
		}
		if sha != "def456" {
			t.Fatalf("sha = %q, want %q", sha, "def456")
		}
	})

	t.Run("missing commit in chain", func(t *testing.T) {
		obj := graphQLGitObject{TypeName: "Tree"}
		_, _, ok := extractGraphQLCommit(obj)
		if ok {
			t.Fatal("expected not ok")
		}
	})
}

func TestCachingClient_ListTagsPrimesCommitDateCache(t *testing.T) {
	mock := &mockClient{
		tags: []Tag{
			{Name: "v1.2.3", CommitSHA: "ABCDEF", CommitDate: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), CommitDateKnown: true},
		},
		commitDate: map[string]map[string]CommitDateResult{},
		resolveRef: map[string]string{},
		errors:     map[string]error{},
	}

	client := NewCachingClient(mock)
	if _, err := client.ListTags(context.Background(), "owner", "repo"); err != nil {
		t.Fatalf("ListTags() error = %v", err)
	}

	when, ok, err := client.CommitDate(context.Background(), "owner", "repo", "abcdef")
	if err != nil {
		t.Fatalf("CommitDate() error = %v", err)
	}
	if !ok {
		t.Fatal("expected cached commit date")
	}
	if when.IsZero() {
		t.Fatal("expected non-zero commit date")
	}
}

func TestFilterTagsByPrefix(t *testing.T) {
	tags := []Tag{
		{Name: "github-v1.2.0", CommitSHA: "a"},
		{Name: "v1.16.2", CommitSHA: "b"},
		{Name: "github-v1.1.9", CommitSHA: "c"},
	}

	filtered := filterTagsByPrefix(tags, "github-v")
	if len(filtered) != 2 {
		t.Fatalf("len(filtered) = %d, want 2", len(filtered))
	}
	if filtered[0].Name != "github-v1.2.0" || filtered[1].Name != "github-v1.1.9" {
		t.Fatalf("unexpected filtered tags: %+v", filtered)
	}
}
