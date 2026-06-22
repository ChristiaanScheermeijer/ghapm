package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// --- detectMajorVersion ---

func TestDetectMajorVersion(t *testing.T) {
	tests := []struct {
		ref      string
		wantMajor int
		wantOK   bool
	}{
		{"v4", 4, true},
		{"V4", 4, true},
		{"v4.2.1", 4, true},
		{"4", 4, true},
		{"4.0.0", 4, true},
		{"v1.0.0", 1, true},
		{"v10", 10, true},
		{"v0.1.0", 0, true},
		{"latest", 0, false},
		{"main", 0, false},
		{"master", 0, false},
		{"develop", 0, false},
		{"feature-branch", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			major, ok := detectMajorVersion(tt.ref)
			if ok != tt.wantOK {
				t.Errorf("detectMajorVersion(%q) ok = %v, want %v", tt.ref, ok, tt.wantOK)
			}
			if major != tt.wantMajor {
				t.Errorf("detectMajorVersion(%q) = %d, want %d", tt.ref, major, tt.wantMajor)
			}
		})
	}
}

func TestDetectMajorVersionWithPath(t *testing.T) {
	tests := []struct {
		ref      string
		wantMajor int
		wantOK   bool
	}{
		{"actions/checkout/v4", 4, true},
		{"owner/repo/v2.1.0", 2, true},
		{"owner/repo/subpath/v3", 3, true},
		{"owner/repo/main", 0, false},
		{"owner/repo/latest", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			major, ok := detectMajorVersion(tt.ref)
			if ok != tt.wantOK {
				t.Errorf("detectMajorVersion(%q) ok = %v, want %v", tt.ref, ok, tt.wantOK)
			}
			if major != tt.wantMajor {
				t.Errorf("detectMajorVersion(%q) = %d, want %d", tt.ref, major, tt.wantMajor)
			}
		})
	}
}

// --- mergeTrackingComment ---

func TestMergeTrackingComment(t *testing.T) {
	tests := []struct {
		existing string
		major    int
		want     string
	}{
		{"", 4, "ghapm:v4"},
		{"ghapm:v3", 4, "ghapm:v4"},
		{"ghapm:v3; custom note", 5, "ghapm:v5; custom note"},
		{"some other comment", 2, "some other comment; ghapm:v2"},
		{"  ghapm:v1  ", 3, "ghapm:v3"},
	}

	for _, tt := range tests {
		t.Run(tt.existing, func(t *testing.T) {
			got := mergeTrackingComment(tt.existing, tt.major)
			if got != tt.want {
				t.Errorf("mergeTrackingComment(%q, %d) = %q, want %q", tt.existing, tt.major, got, tt.want)
			}
		})
	}
}

// --- shortCommit ---

func TestShortCommit(t *testing.T) {
	tests := []struct {
		sha  string
		want string
	}{
		{"abc123", "abc123"},
		{"1234567890abcdef1234567890abcdef12345678", "1234567890ab"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaa"},
		{"", ""},
		{"short", "short"},
		{"123456789012", "123456789012"},
		{"1234567890123", "123456789012"},
	}

	for _, tt := range tests {
		t.Run(tt.sha, func(t *testing.T) {
			got := shortCommit(tt.sha)
			if got != tt.want {
				t.Errorf("shortCommit(%q) = %q, want %q", tt.sha, got, tt.want)
			}
		})
	}
}

// --- intPtr ---

func TestIntPtr(t *testing.T) {
	v := 42
	p := intPtr(v)
	if *p != 42 {
		t.Errorf("intPtr(42) = %d, want 42", *p)
	}
	if p == &v {
		t.Error("intPtr should return a new pointer, not the original")
	}
}

// --- colorize ---

func TestColorize(t *testing.T) {
	original := colorEnabled

	t.Run("colors when enabled", func(t *testing.T) {
		colorEnabled = true
		got := colorize("hello", ansiGreen)
		want := "\033[32mhello\033[0m"
		if got != want {
			t.Errorf("colorize with color = %q, want %q", got, want)
		}
	})

	t.Run("no colors when disabled", func(t *testing.T) {
		colorEnabled = false
		got := colorize("hello", ansiGreen)
		if got != "hello" {
			t.Errorf("colorize without color = %q, want %q", got, "hello")
		}
	})

	t.Run("no colors when color is empty", func(t *testing.T) {
		colorEnabled = true
		got := colorize("hello", "")
		if got != "hello" {
			t.Errorf("colorize with empty color = %q, want %q", got, "hello")
		}
	})

	colorEnabled = original
}

func TestColorizeEnvVar(t *testing.T) {
	original := os.Getenv("NO_COLOR")
	defer os.Setenv("NO_COLOR", original)

	os.Setenv("NO_COLOR", "1")

	colorEnabled = os.Getenv("NO_COLOR") == ""
	got := colorize("test", ansiRed)
	if got != "test" {
		t.Errorf("colorize with NO_COLOR set = %q, want %q", got, "test")
	}

	os.Setenv("NO_COLOR", "")
	colorEnabled = os.Getenv("NO_COLOR") == ""
	got = colorize("test", ansiRed)
	want := "\033[31mtest\033[0m"
	if got != want {
		t.Errorf("colorize without NO_COLOR = %q, want %q", got, want)
	}
}

// --- discoverWorkflowFiles ---

func TestDiscoverWorkflowFiles(t *testing.T) {
	dir := t.TempDir()

	createFile := func(name string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
	}

	createFile("build.yml")
	createFile("deploy.yaml")
	createFile("test.json")
	createFile("README.md")
	createSubDir := filepath.Join(dir, "subdir")
	os.MkdirAll(createSubDir, 0755)
	createFileInSub := filepath.Join(createSubDir, "nested.yml")
	os.WriteFile(createFileInSub, []byte(""), 0644)

	t.Run("finds yml and yaml files", func(t *testing.T) {
		files, err := discoverWorkflowFiles(dir)
		if err != nil {
			t.Fatalf("discoverWorkflowFiles() error = %v", err)
		}
		if len(files) != 2 {
			t.Errorf("expected 2 files, got %d", len(files))
		}

		names := make(map[string]bool)
		for _, f := range files {
			names[filepath.Base(f)] = true
		}
		if !names["build.yml"] {
			t.Error("expected build.yml to be found")
		}
		if !names["deploy.yaml"] {
			t.Error("expected deploy.yaml to be found")
		}
	})

	t.Run("returns error for missing directory", func(t *testing.T) {
		_, err := discoverWorkflowFiles(filepath.Join(dir, "nonexistent"))
		if err == nil {
			t.Error("expected error for missing directory")
		}
	})

	t.Run("ignores subdirectories", func(t *testing.T) {
		files, err := discoverWorkflowFiles(dir)
		if err != nil {
			t.Fatalf("discoverWorkflowFiles() error = %v", err)
		}
		for _, f := range files {
			if filepath.Base(f) == "nested.yml" {
				t.Error("should not find files in subdirectories")
			}
		}
	})
}

// --- regex patterns ---

func TestActionRefExpr(t *testing.T) {
	tests := []struct {
		input    string
		wantMatch bool
		wantOwnerRepo string
		wantRef  string
	}{
		{"actions/checkout@v4", true, "actions/checkout", "v4"},
		{"actions/checkout@main", true, "actions/checkout", "main"},
		{"actions/checkout@latest", true, "actions/checkout", "latest"},
		{"slackapi/slack-github-action@v1.27.0", true, "slackapi/slack-github-action", "v1.27.0"},
		{"owner/repo/subpath@v2", true, "owner/repo/subpath", "v2"},
		{"./local-action", false, "", ""},
		{"docker://alpine:3.8", false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			match := actionRefExpr.FindStringSubmatch(tt.input)
			if (match != nil) != tt.wantMatch {
				t.Errorf("actionRefExpr match %v, want %v", match != nil, tt.wantMatch)
				return
			}
			if tt.wantMatch {
				if match[1] != tt.wantOwnerRepo {
					t.Errorf("owner/repo = %q, want %q", match[1], tt.wantOwnerRepo)
				}
				if match[2] != tt.wantRef {
					t.Errorf("ref = %q, want %q", match[2], tt.wantRef)
				}
			}
		})
	}
}

func TestCheckExitCode(t *testing.T) {
	tests := []struct {
		name   string
		report checkReport
		want   int
	}{
		{
			name: "no findings",
			report: checkReport{
				Summary: checkSummary{TrackedCount: 2},
			},
			want: 0,
		},
		{
			name: "floating refs",
			report: checkReport{
				Summary: checkSummary{FloatingCount: 1},
			},
			want: constCheckExitFindings,
		},
		{
			name: "dynamic refs",
			report: checkReport{
				Summary: checkSummary{DynamicCount: 1},
			},
			want: constCheckExitFindings,
		},
		{
			name: "missing tracking comment",
			report: checkReport{
				Summary: checkSummary{MissingCount: 1},
			},
			want: constCheckExitFindings,
		},
		{
			name: "invalid tracking comment",
			report: checkReport{
				Summary: checkSummary{InvalidCount: 1},
			},
			want: constCheckExitFindings,
		},
		{
			name: "workflow directory missing",
			report: checkReport{
				WorkflowDirMissing: true,
				Summary:            checkSummary{FloatingCount: 3},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkExitCode(tt.report)
			if got != tt.want {
				t.Errorf("checkExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShaExpr(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1234567890abcdef1234567890abcdef12345678", true},
		{"ABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"1234567890abcdef1234567890abcdef1234567", false},
		{"1234567890abcdef1234567890abcdef123456789", false},
		{"v4", false},
		{"main", false},
		{"not-a-sha", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shaExpr.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("shaExpr.MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTrackingCommentRe(t *testing.T) {
	tests := []struct {
		input    string
		wantMatch bool
		wantMajor string
	}{
		{"ghapm:v4", true, "4"},
		{"ghapm:v10", true, "10"},
		{"ghapm:v0", true, "0"},
		{"some comment; ghapm:v3", true, "3"},
		{"ghapm:v", false, ""},
		{"ghapm:4", false, ""},
		{"v4", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			match := trackingCommentRe.FindStringSubmatch(tt.input)
			if (match != nil) != tt.wantMatch {
				t.Errorf("trackingCommentRe match %v, want %v", match != nil, tt.wantMatch)
				return
			}
			if tt.wantMatch {
				if match[1] != tt.wantMajor {
					t.Errorf("major = %q, want %q", match[1], tt.wantMajor)
				}
			}
		})
	}
}
