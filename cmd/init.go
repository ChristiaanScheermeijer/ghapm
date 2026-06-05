package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
	"github.com/spf13/cobra"
)

var (
	initWorkflowDir string
	initDryRun      bool
	initOutputJSON  bool
	initUseAPI      bool

	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Pin all workflow actions to specific commits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runInit(cmd.Context(), initWorkflowDir, initDryRun, initUseAPI)
			if err != nil {
				return err
			}

			if initOutputJSON {
				return writeInitReportJSON(cmd, report)
			}

			writeInitReportText(cmd, report, initWorkflowDir, initDryRun)
			return nil
		},
	}
)

type initChange struct {
	Workflow      string `json:"workflow"`
	Line          int    `json:"line"`
	Action        string `json:"action"`
	OriginalRef   string `json:"originalRef"`
	NewRef        string `json:"newRef,omitempty"`
	TrackingMajor *int   `json:"trackingMajor,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
}

type initSummary struct {
	WorkflowCount      int  `json:"workflowCount"`
	ModifiedWorkflows  int  `json:"modifiedWorkflows"`
	ActionCount        int  `json:"actionCount"`
	PinnedCount        int  `json:"pinnedCount"`
	AlreadyPinnedCount int  `json:"alreadyPinnedCount"`
	SkippedCount       int  `json:"skippedCount"`
	FailedCount        int  `json:"failedCount"`
	DryRun             bool `json:"dryRun"`
}

type initReport struct {
	Summary            initSummary  `json:"summary"`
	Changes            []initChange `json:"changes"`
	WorkflowDirMissing bool         `json:"workflowDirMissing"`
}

type initFileStats struct {
	Actions       int
	Pinned        int
	AlreadyPinned int
	Skipped       int
	Failed        int
}

var (
	initUsesLineExpr        = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*)([^\s#]+)(\s*)(?:#\s*(.+))?$`)
	majorVersionCandidateRe = regexp.MustCompile(`^[vV]?(\d+)`)
)

type initResolver struct {
	client githubclient.Client
}

func (r *initResolver) Resolve(ctx context.Context, owner, repo, ref string) (string, error) {
	return r.client.ResolveRef(ctx, owner, repo, ref)
}

func runInit(ctx context.Context, workflowDir string, dryRun bool, useAPI bool) (initReport, error) {
	files, err := discoverWorkflowFiles(workflowDir)
	if err != nil {
		if errors.Is(err, errWorkflowDirMissing) {
			return initReport{WorkflowDirMissing: true, Summary: initSummary{DryRun: dryRun}}, nil
		}
		return initReport{}, err
	}

	report := initReport{
		Summary: initSummary{
			WorkflowCount: len(files),
			DryRun:        dryRun,
		},
	}

	resolver := &initResolver{client: newGitHubClient(useAPI)}

	for _, file := range files {
		fileChanges, stats, fileChanged, err := processInitWorkflowFile(ctx, file, resolver, dryRun)
		report.Changes = append(report.Changes, fileChanges...)
		report.Summary.ActionCount += stats.Actions
		report.Summary.PinnedCount += stats.Pinned
		report.Summary.AlreadyPinnedCount += stats.AlreadyPinned
		report.Summary.SkippedCount += stats.Skipped
		report.Summary.FailedCount += stats.Failed

		if err != nil {
			return report, err
		}

		if fileChanged {
			report.Summary.ModifiedWorkflows++
		}
	}

	return report, nil
}

func processInitWorkflowFile(ctx context.Context, path string, resolver *initResolver, dryRun bool) ([]initChange, initFileStats, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, initFileStats{}, false, fmt.Errorf("read workflow %q: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	newLines := make([]string, len(lines))
	workflowPath := filepath.ToSlash(path)

	var (
		changes    []initChange
		stats      initFileStats
		fileChange bool
	)

	for idx, line := range lines {
		lineNumber := idx + 1

		newLine, change, lineChanged, err := transformInitLine(ctx, resolver, workflowPath, line, lineNumber)
		if change != nil {
			stats.Actions++
			switch change.Status {
			case "pinned":
				stats.Pinned++
			case "already-pinned":
				stats.AlreadyPinned++
			case "skipped":
				stats.Skipped++
			case "error":
				stats.Failed++
			}
			changes = append(changes, *change)
		}

		if err != nil {
			return changes, stats, false, err
		}

		newLines[idx] = newLine
		if lineChanged {
			fileChange = true
		}
	}

	if fileChange && !dryRun {
		info, err := os.Stat(path)
		if err != nil {
			return changes, stats, fileChange, fmt.Errorf("stat workflow %q: %w", path, err)
		}
		updated := strings.Join(newLines, "\n")
		if err := os.WriteFile(path, []byte(updated), info.Mode()); err != nil {
			return changes, stats, fileChange, fmt.Errorf("write workflow %q: %w", path, err)
		}
	}

	return changes, stats, fileChange, nil
}

func transformInitLine(ctx context.Context, resolver *initResolver, workflow, line string, lineNumber int) (string, *initChange, bool, error) {
	match := initUsesLineExpr.FindStringSubmatch(line)
	if match == nil {
		return line, nil, false, nil
	}

	prefix := match[1]
	usesValue := strings.TrimSpace(match[2])
	commentSpacing := match[3]
	commentValue := ""
	if len(match) >= 5 {
		commentValue = strings.TrimSpace(match[4])
	}

	actionMatch := actionRefExpr.FindStringSubmatch(usesValue)
	if actionMatch == nil {
		return line, nil, false, nil
	}

	parts := strings.Split(actionMatch[1], "/")
	if len(parts) < 2 {
		return line, nil, false, nil
	}

	owner := parts[0]
	repo := parts[1]

	change := initChange{
		Workflow:    workflow,
		Line:        lineNumber,
		Action:      actionMatch[1],
		OriginalRef: actionMatch[2],
	}

	if strings.Contains(change.OriginalRef, "${{") {
		change.Status = "skipped"
		change.Message = "Ref is computed at runtime; cannot pin"
		return line, &change, false, nil
	}

	if shaExpr.MatchString(change.OriginalRef) {
		change.Status = "already-pinned"
		if sub := trackingCommentRe.FindStringSubmatch(commentValue); sub != nil {
			major, err := strconv.Atoi(sub[1])
			if err == nil {
				change.TrackingMajor = &major
				change.Message = fmt.Sprintf("Already pinned (tracking major v%d)", major)
			}
		} else if commentValue != "" {
			change.Message = "Already pinned; preserving existing comment"
		} else {
			change.Message = "Already pinned"
		}
		return line, &change, false, nil
	}

	major, ok := detectMajorVersion(change.OriginalRef)
	if !ok {
		change.Status = "skipped"
		change.Message = fmt.Sprintf("Cannot determine major version from ref %q", change.OriginalRef)
		return line, &change, false, nil
	}

	sha, err := resolver.Resolve(ctx, owner, repo, change.OriginalRef)
	if err != nil {
		change.Status = "error"
		change.Message = err.Error()
		return line, &change, false, err
	}

	change.Status = "pinned"
	change.NewRef = sha
	change.TrackingMajor = &major
	change.Message = fmt.Sprintf("Pinned to commit %s", shortCommit(sha))

	newUses := actionMatch[1] + "@" + sha
	comment := mergeTrackingComment(commentValue, major)
	spacing := commentSpacing
	if comment != "" && spacing == "" {
		spacing = " "
	}

	newLine := prefix + newUses
	if comment != "" {
		newLine += spacing + "# " + comment
	}

	return newLine, &change, newLine != line, nil
}

func detectMajorVersion(ref string) (int, bool) {
	candidate := ref
	if strings.Contains(candidate, "/") {
		slashParts := strings.Split(candidate, "/")
		candidate = slashParts[len(slashParts)-1]
	}

	if match := majorVersionCandidateRe.FindStringSubmatch(candidate); match != nil {
		major, err := strconv.Atoi(match[1])
		if err == nil {
			return major, true
		}
	}

	if match := majorVersionCandidateRe.FindStringSubmatch(ref); match != nil {
		major, err := strconv.Atoi(match[1])
		if err == nil {
			return major, true
		}
	}

	return 0, false
}

func mergeTrackingComment(existing string, major int) string {
	trimmed := strings.TrimSpace(existing)
	annotation := fmt.Sprintf("ghapm:v%d", major)

	if trimmed == "" {
		return annotation
	}

	if trackingCommentRe.MatchString(trimmed) {
		return trackingCommentRe.ReplaceAllString(trimmed, annotation)
	}

	return fmt.Sprintf("%s; %s", trimmed, annotation)
}

func shortCommit(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func writeInitReportJSON(cmd *cobra.Command, report initReport) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeInitReportText(cmd *cobra.Command, report initReport, workflowDir string, dryRun bool) {
	out := cmd.OutOrStdout()

	if report.WorkflowDirMissing {
		fmt.Fprintln(out, colorize(fmt.Sprintf("Workflow directory %s not found; nothing to initialize", workflowDir), ansiRed))
		return
	}

	if report.Summary.ActionCount == 0 {
		fmt.Fprintln(out, colorize(fmt.Sprintf("No GitHub Actions uses: references found under %s", workflowDir), ansiGray))
		return
	}

	var (
		pinned  []initChange
		already []initChange
		skipped []initChange
		failed  []initChange
	)

	for _, change := range report.Changes {
		switch change.Status {
		case "pinned":
			pinned = append(pinned, change)
		case "already-pinned":
			already = append(already, change)
		case "skipped":
			skipped = append(skipped, change)
		case "error":
			failed = append(failed, change)
		}
	}

	needsGap := false

	printChanges := func(title string, color string, items []initChange, formatter func(initChange) (string, string)) {
		if len(items) == 0 {
			return
		}
		if needsGap {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, colorize(title, color))
		for _, change := range items {
			line, message := formatter(change)
			fmt.Fprintln(out, colorize(line, color))
			if msg := strings.TrimSpace(message); msg != "" {
				fmt.Fprintln(out, colorize("  - "+msg, color))
			}
		}
		needsGap = true
	}

	printChanges("Pinned actions:", ansiGreen, pinned, func(change initChange) (string, string) {
		major := 0
		if change.TrackingMajor != nil {
			major = *change.TrackingMajor
		}
		line := fmt.Sprintf("- %s:%d %s@%s -> %s (major v%d)", change.Workflow, change.Line, change.Action, change.OriginalRef, change.NewRef, major)
		return line, change.Message
	})

	printChanges("Already pinned:", ansiCyan, already, func(change initChange) (string, string) {
		major := ""
		if change.TrackingMajor != nil {
			major = fmt.Sprintf(" (major v%d)", *change.TrackingMajor)
		}
		line := fmt.Sprintf("- %s:%d %s@%s%s", change.Workflow, change.Line, change.Action, change.OriginalRef, major)
		return line, change.Message
	})

	printChanges("Skipped:", ansiYellow, skipped, func(change initChange) (string, string) {
		line := fmt.Sprintf("- %s:%d %s@%s", change.Workflow, change.Line, change.Action, change.OriginalRef)
		return line, change.Message
	})

	printChanges("Failed:", ansiRed, failed, func(change initChange) (string, string) {
		line := fmt.Sprintf("- %s:%d %s@%s", change.Workflow, change.Line, change.Action, change.OriginalRef)
		return line, change.Message
	})

	if needsGap {
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, colorize(fmt.Sprintf("Summary: %d workflows scanned, %d actions processed", report.Summary.WorkflowCount, report.Summary.ActionCount), ansiMagenta))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Newly pinned: %d", report.Summary.PinnedCount), ansiGreen))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Already pinned: %d", report.Summary.AlreadyPinnedCount), ansiCyan))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Skipped: %d", report.Summary.SkippedCount), ansiYellow))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Failed: %d", report.Summary.FailedCount), ansiRed))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Workflows updated: %d", report.Summary.ModifiedWorkflows), ansiMagenta))

	if dryRun {
		fmt.Fprintln(out, colorize("\nDry run mode; no files were modified.", ansiGray))
	}
}

func init() {
	initCmd.Flags().StringVar(&initWorkflowDir, "workflows", ".github/workflows", "Directory that contains workflow files")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Preview changes without modifying files")
	initCmd.Flags().BoolVar(&initOutputJSON, "json", false, "Emit machine-readable JSON output")
	initCmd.Flags().BoolVar(&initUseAPI, "api", false, "Use GitHub REST API instead of the gh CLI")
	rootCmd.AddCommand(initCmd)
}
