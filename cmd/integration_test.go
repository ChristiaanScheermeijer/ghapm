package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
)

// --- Mock GitHub Client ---

type mockGitHubClient struct {
	tags       map[string][]githubclient.Tag
	commits    map[string]commitInfo
	resolved   map[string]string
	failOn     map[string]error
}

type commitInfo struct {
	date time.Time
	ok   bool
}

func newMockClient() *mockGitHubClient {
	return &mockGitHubClient{
		tags:     make(map[string][]githubclient.Tag),
		commits:  make(map[string]commitInfo),
		resolved: make(map[string]string),
		failOn:   make(map[string]error),
	}
}

func (m *mockGitHubClient) AddTags(owner, repo string, tags []githubclient.Tag) {
	m.tags[owner+"/"+repo] = tags
}

func (m *mockGitHubClient) AddCommit(owner, repo, sha string, date time.Time, ok bool) {
	m.commits[owner+"/"+repo+"/"+sha] = commitInfo{date: date, ok: ok}
}

func (m *mockGitHubClient) AddResolve(owner, repo, ref, sha string) {
	m.resolved[owner+"/"+repo+"/"+ref] = sha
}

func (m *mockGitHubClient) AddError(key string, err error) {
	m.failOn[key] = err
}

func (m *mockGitHubClient) ListTags(ctx context.Context, owner, repo string) ([]githubclient.Tag, error) {
	key := "ListTags:" + owner + "/" + repo
	if err, ok := m.failOn[key]; ok {
		return nil, err
	}
	return m.tags[owner+"/"+repo], nil
}

func (m *mockGitHubClient) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	key := owner + "/" + repo + "/" + sha
	if err, ok := m.failOn["CommitDate:"+key]; ok {
		return time.Time{}, false, err
	}
	info, ok := m.commits[key]
	if !ok {
		return time.Time{}, false, nil
	}
	return info.date, info.ok, nil
}

func (m *mockGitHubClient) ResolveRef(ctx context.Context, owner, repo, ref string) (string, error) {
	key := "ResolveRef:" + owner + "/" + repo + "/" + ref
	if err, ok := m.failOn[key]; ok {
		return "", err
	}
	sha, ok := m.resolved[owner+"/"+repo+"/"+ref]
	if !ok {
		return "", &resolveError{ref: ref}
	}
	return sha, nil
}

type resolveError struct {
	ref string
}

func (e *resolveError) Error() string {
	return "ref \"" + e.ref + "\" not found"
}

var _ githubclient.Client = (*mockGitHubClient)(nil)

// --- Integration Tests for init command ---

func TestTransformInitLine_AlreadyPinned(t *testing.T) {
	client := newMockClient()
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@1234567890abcdef1234567890abcdef12345678 # ghapm:v4"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for already pinned action")
	}
	if newLine != line {
		t.Errorf("line changed unexpectedly:\ngot:  %q\nwant: %q", newLine, line)
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "already-pinned" {
		t.Errorf("status = %q, want %q", change.Status, "already-pinned")
	}
	if change.TrackingMajor == nil || *change.TrackingMajor != 4 {
		t.Errorf("tracking major = %v, want 4", change.TrackingMajor)
	}
}

func TestTransformInitLine_AlreadyPinnedNoComment(t *testing.T) {
	client := newMockClient()
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@1234567890abcdef1234567890abcdef12345678"
	_, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for already pinned action")
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "already-pinned" {
		t.Errorf("status = %q, want %q", change.Status, "already-pinned")
	}
	if change.TrackingMajor != nil {
		t.Errorf("expected nil tracking major, got %d", *change.TrackingMajor)
	}
}

func TestTransformInitLine_SkippedDynamic(t *testing.T) {
	client := newMockClient()
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@${{ github.ref }}"
	_, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for dynamic ref")
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "skipped" {
		t.Errorf("status = %q, want %q", change.Status, "skipped")
	}
}

func TestTransformInitLine_SkippedNoMajorVersion(t *testing.T) {
	client := newMockClient()
	// Resolve "main" to a SHA, but don't add any tags pointing to that SHA
	client.AddResolve("actions", "checkout", "main", "abcdef1234567890abcdef1234567890abcdef12")
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@main"
	_, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change when major version cannot be determined")
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "skipped" {
		t.Errorf("status = %q, want %q", change.Status, "skipped")
	}
}

func TestTransformInitLine_ErrorRefNotFound(t *testing.T) {
	client := newMockClient()
	client.AddError("ResolveRef:actions/checkout/v99", &resolveError{ref: "v99"})
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@v99"
	_, _, _, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err == nil {
		t.Fatal("expected error for non-existent ref")
	}
}

func TestTransformInitLine_NotUsesLine(t *testing.T) {
	client := newMockClient()
	resolver := &initResolver{client: client}

	line := "      name: Build"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for non-uses line")
	}
	if newLine != line {
		t.Errorf("line changed unexpectedly")
	}
	if change != nil {
		t.Error("expected nil change for non-uses line")
	}
}

func TestTransformInitLine_LocalAction(t *testing.T) {
	client := newMockClient()
	resolver := &initResolver{client: client}

	line := "      uses: ./local-action"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change for local action")
	}
	if newLine != line {
		t.Errorf("line changed unexpectedly")
	}
	if change != nil {
		t.Error("expected nil change for local action")
	}
}

func TestTransformInitLine_SafetyWindowFallsBackToOlderTag(t *testing.T) {
	now := time.Now()
	newSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	client := newMockClient()
	client.AddResolve("actions", "checkout", "v4", newSHA)
	client.AddTags("actions", "checkout", []githubclient.Tag{
		{Name: "v4.2.0", CommitSHA: newSHA},
		{Name: "v4.1.0", CommitSHA: oldSHA},
	})
	client.AddCommit("actions", "checkout", newSHA, now.Add(-2*24*time.Hour), true)
	client.AddCommit("actions", "checkout", oldSHA, now.Add(-30*24*time.Hour), true)
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@v4"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected line to be changed")
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "pinned" {
		t.Fatalf("status = %q, want %q", change.Status, "pinned")
	}
	if change.NewRef != oldSHA {
		t.Fatalf("new ref = %q, want %q", change.NewRef, oldSHA)
	}
	if !strings.Contains(newLine, "@"+oldSHA) {
		t.Fatalf("expected line to include old safe SHA, got %q", newLine)
	}
}

func TestTransformInitLine_SafetyWindowNoEligibleRelease(t *testing.T) {
	now := time.Now()
	newSHA := "cccccccccccccccccccccccccccccccccccccccc"

	client := newMockClient()
	client.AddResolve("actions", "checkout", "v4", newSHA)
	client.AddTags("actions", "checkout", []githubclient.Tag{{Name: "v4.2.0", CommitSHA: newSHA}})
	client.AddCommit("actions", "checkout", newSHA, now.Add(-1*24*time.Hour), true)
	resolver := &initResolver{client: client}

	line := "      uses: actions/checkout@v4"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 14)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected no line change")
	}
	if newLine != line {
		t.Fatalf("line changed unexpectedly: %q", newLine)
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "skipped" {
		t.Fatalf("status = %q, want %q", change.Status, "skipped")
	}
}

func TestTransformInitLine_SubpathLatestUsesSubpathTags(t *testing.T) {
	sha := "dddddddddddddddddddddddddddddddddddddddd"

	client := newMockClient()
	client.AddResolve("anomalyco", "opencode", "latest", sha)
	client.AddTags("anomalyco", "opencode", []githubclient.Tag{
		{Name: "cli-v2.4.0", CommitSHA: sha},
		{Name: "github-v1.2.25", CommitSHA: sha},
	})
	resolver := &initResolver{client: client}

	line := "      uses: anomalyco/opencode/github@latest"
	newLine, change, changed, err := transformInitLine(context.Background(), resolver, "test.yml", line, 1, 0)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected line to be changed")
	}
	if change == nil {
		t.Fatal("expected change record")
	}
	if change.Status != "pinned" {
		t.Fatalf("status = %q, want %q", change.Status, "pinned")
	}
	if change.TrackingMajor == nil || *change.TrackingMajor != 1 {
		t.Fatalf("tracking major = %v, want 1", change.TrackingMajor)
	}
	if !strings.Contains(newLine, "# ghapm:v1") {
		t.Fatalf("expected ghapm:v1 annotation, got %q", newLine)
	}
}

// --- Integration Tests for check command ---

func TestParseWorkflowFile_FloatingRefs(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@main
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0].Status != "floating-ref" {
		t.Errorf("record[0].status = %q, want %q", records[0].Status, "floating-ref")
	}
	if records[1].Status != "floating-ref" {
		t.Errorf("record[1].status = %q, want %q", records[1].Status, "floating-ref")
	}
}

func TestParseWorkflowFile_TrackedAction(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@1234567890abcdef1234567890abcdef12345678 # ghapm:v4
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].Status != "tracked" {
		t.Errorf("status = %q, want %q", records[0].Status, "tracked")
	}
	if records[0].TrackingMajor == nil || *records[0].TrackingMajor != 4 {
		t.Errorf("tracking major = %v, want 4", records[0].TrackingMajor)
	}
	if !records[0].Pinned {
		t.Error("expected Pinned = true")
	}
}

func TestParseWorkflowFile_MissingTrackingComment(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@1234567890abcdef1234567890abcdef12345678
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].Status != "missing-tracking-comment" {
		t.Errorf("status = %q, want %q", records[0].Status, "missing-tracking-comment")
	}
}

func TestParseWorkflowFile_DynamicRef(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@${{ github.ref }}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].Status != "dynamic-ref" {
		t.Errorf("status = %q, want %q", records[0].Status, "dynamic-ref")
	}
}

func TestParseWorkflowFile_IgnoredLocalAction(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: ./local-action
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].Status != "ignored" {
		t.Errorf("status = %q, want %q", records[0].Status, "ignored")
	}
}

func TestParseWorkflowFile_InvalidTrackingComment(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@1234567890abcdef1234567890abcdef12345678 # some comment without ghapm
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	records, err := parseWorkflowFile(path)
	if err != nil {
		t.Fatalf("parseWorkflowFile() error = %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].Status != "invalid-tracking-comment" {
		t.Errorf("status = %q, want %q", records[0].Status, "invalid-tracking-comment")
	}
}

func TestBuildCheckReport(t *testing.T) {
	content := `name: Test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@1234567890abcdef1234567890abcdef12345678 # ghapm:v3
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := buildCheckReport(dir)
	if err != nil {
		t.Fatalf("buildCheckReport() error = %v", err)
	}

	if report.Summary.WorkflowCount != 1 {
		t.Errorf("workflow count = %d, want 1", report.Summary.WorkflowCount)
	}
	if report.Summary.ActionCount != 2 {
		t.Errorf("action count = %d, want 2", report.Summary.ActionCount)
	}
	if report.Summary.RemoteCount != 2 {
		t.Errorf("remote count = %d, want 2", report.Summary.RemoteCount)
	}
	if report.Summary.FloatingCount != 1 {
		t.Errorf("floating count = %d, want 1", report.Summary.FloatingCount)
	}
	if report.Summary.TrackedCount != 1 {
		t.Errorf("tracked count = %d, want 1", report.Summary.TrackedCount)
	}
}

func TestBuildCheckReport_MissingDirectory(t *testing.T) {
	report, err := buildCheckReport("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("buildCheckReport() error = %v", err)
	}
	if !report.WorkflowDirMissing {
		t.Error("expected WorkflowDirMissing = true")
	}
}

// --- Upgrade command helper tests ---

func TestDisplayRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"1234567890abcdef", "1234567890ab"},
		{"123456789012", "123456789012"},
		{"1234567890123", "123456789012"},
		{"short", "short"},
		{"", ""},
		{"  1234567890abcdef  ", "1234567890ab"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := displayRef(tt.ref)
			if got != tt.want {
				t.Errorf("displayRef(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestFindTagForCommit(t *testing.T) {
	tags := []githubclient.Tag{
		{Name: "v1.0.0", CommitSHA: "aaa"},
		{Name: "v2.0.0", CommitSHA: "bbb"},
		{Name: "v1.5.0", CommitSHA: "ccc"},
	}

	tests := []struct {
		commit string
		want   string
	}{
		{"aaa", "v1.0.0"},
		{"bbb", "v2.0.0"},
		{"AAA", "v1.0.0"},
		{"ddd", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.commit, func(t *testing.T) {
			got := findTagForCommit("actions/checkout", tags, tt.commit)
			if got != tt.want {
				t.Errorf("findTagForCommit(%q) = %q, want %q", tt.commit, got, tt.want)
			}
		})
	}
}

func TestFindTagForCommit_SubpathPrefersMatchingPrefix(t *testing.T) {
	tags := []githubclient.Tag{
		{Name: "v1.16.2", CommitSHA: "aaa"},
		{Name: "github-v1.2.19", CommitSHA: "aaa"},
	}

	got := findTagForCommit("anomalyco/opencode/github", tags, "aaa")
	if got != "github-v1.2.19" {
		t.Fatalf("findTagForCommit() = %q, want %q", got, "github-v1.2.19")
	}
}

func TestParseTagVersion(t *testing.T) {
	tests := []struct {
		name      string
		wantMajor int
		wantMinor int
		wantPatch int
	}{
		{"v1.2.3", 1, 2, 3},
		{"v4.0.0", 4, 0, 0},
		{"1.0.0", 1, 0, 0},
		{"v1.2", 0, 0, 0},
		{"v1", 0, 0, 0},
		{"latest", 0, 0, 0},
		{"", 0, 0, 0},
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

func TestSelectUpgradeTarget_DoesNotDowngradeUnsafeCurrent(t *testing.T) {
	now := time.Now()

	client := newMockClient()
	client.AddCommit("actions", "checkout", "sha603", now.Add(-2*24*time.Hour), true)
	client.AddCommit("actions", "checkout", "sha602", now.Add(-30*24*time.Hour), true)
	client.AddCommit("actions", "checkout", "sha700", now.Add(-2*24*time.Hour), true)

	resolver := &tagResolver{
		client:           client,
		allowMajor:       false,
		enforceSafety:    true,
		safetyWindowDays: 14,
		cutoff:           now.Add(-14 * 24 * time.Hour),
	}

	tags := []githubclient.Tag{
		{Name: "v7.0.0", CommitSHA: "sha700"},
		{Name: "v6.0.3", CommitSHA: "sha603"},
		{Name: "v6.0.2", CommitSHA: "sha602"},
	}

	target, state, _, _, _, err := resolver.selectUpgradeTarget(context.Background(), "actions/checkout", "actions", "checkout", tags, 6, "sha603")
	if err != nil {
		t.Fatalf("selectUpgradeTarget() error = %v", err)
	}
	if state != upgradeStateCurrent {
		t.Fatalf("state = %v, want %v", state, upgradeStateCurrent)
	}
	if target != nil {
		t.Fatalf("target should be nil when staying on current pin, got %+v", *target)
	}
}

func TestSelectUpgradeTarget_SubpathIgnoresRootTags(t *testing.T) {
	now := time.Now()

	client := newMockClient()
	client.AddCommit("anomalyco", "opencode", "sha119", now.Add(-30*24*time.Hour), true)
	client.AddCommit("anomalyco", "opencode", "sha120", now.Add(-29*24*time.Hour), true)
	client.AddCommit("anomalyco", "opencode", "sharoot", now.Add(-60*24*time.Hour), true)

	resolver := &tagResolver{
		client:           client,
		allowMajor:       false,
		enforceSafety:    true,
		safetyWindowDays: 14,
		cutoff:           now.Add(-14 * 24 * time.Hour),
	}

	tags := []githubclient.Tag{
		{Name: "v1.16.2", CommitSHA: "sharoot"},
		{Name: "github-v1.2.20", CommitSHA: "sha120"},
		{Name: "github-v1.2.19", CommitSHA: "sha119"},
	}

	target, state, _, _, _, err := resolver.selectUpgradeTarget(context.Background(), "anomalyco/opencode/github", "anomalyco", "opencode", tags, 1, "sha119")
	if err != nil {
		t.Fatalf("selectUpgradeTarget() error = %v", err)
	}
	if state != upgradeStateUpgrade {
		t.Fatalf("state = %v, want %v", state, upgradeStateUpgrade)
	}
	if target == nil {
		t.Fatal("expected upgrade target")
	}
	if target.Name != "github-v1.2.20" {
		t.Fatalf("target = %q, want %q", target.Name, "github-v1.2.20")
	}
}
