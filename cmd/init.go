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
	"time"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
	"github.com/spf13/cobra"
)

var (
	initWorkflowDir string
	initDryRun      bool
	initOutputJSON  bool
	initUseAPI      bool
	initSafetyWindow int

	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Pin all workflow actions to specific commits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runInit(cmd.Context(), initWorkflowDir, initDryRun, initUseAPI, initSafetyWindow)
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
	initUsesLineExpr        = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*)(.+?)(\s*)(?:#\s*(.+))?$`)
	majorVersionCandidateRe = regexp.MustCompile(`^[vV]?(\d+)`)
	trackingRefExpr         = regexp.MustCompile(`^([A-Za-z0-9_.-]*?)[vV](\d+)`)
	trackingRefBareExpr     = regexp.MustCompile(`^(\d+)`)
)

type initResolver struct {
	client githubclient.Client
}

func (r *initResolver) Resolve(ctx context.Context, owner, repo, ref string) (string, error) {
	return r.client.ResolveRef(ctx, owner, repo, ref)
}

func (r *initResolver) CommitDate(ctx context.Context, owner, repo, sha string) (time.Time, bool, error) {
	return r.client.CommitDate(ctx, owner, repo, sha)
}

func runInit(ctx context.Context, workflowDir string, dryRun bool, useAPI bool, safetyWindowDays int) (initReport, error) {
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
		fileChanges, stats, fileChanged, err := processInitWorkflowFile(ctx, file, resolver, dryRun, safetyWindowDays)
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

func processInitWorkflowFile(ctx context.Context, path string, resolver *initResolver, dryRun bool, safetyWindowDays int) ([]initChange, initFileStats, bool, error) {
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

		newLine, change, lineChanged, err := transformInitLine(ctx, resolver, workflowPath, line, lineNumber, safetyWindowDays)
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

func transformInitLine(ctx context.Context, resolver *initResolver, workflow, line string, lineNumber int, safetyWindowDays int) (string, *initChange, bool, error) {
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
		if tracked, ok := parseTrackingComment(commentValue); ok {
			change.TrackingMajor = &tracked.Major
			change.Message = fmt.Sprintf("Already pinned (tracking major v%d)", tracked.Major)
		} else if commentValue != "" {
			change.Message = "Already pinned; preserving existing comment"
		} else {
			change.Message = "Already pinned"
		}
		return line, &change, false, nil
	}

	trackingPrefix := ""
	if tracked, ok := parseTrackingComment(commentValue); ok {
		trackingPrefix = tracked.TagPrefix
	}

	// Try to detect major version from the ref name first
	major, ok := detectMajorVersion(change.OriginalRef)
	if refPrefix, _, prefixOK := detectTrackingFromRef(change.OriginalRef); prefixOK && trackingPrefix == "" {
		trackingPrefix = refPrefix
	}
	if !ok {
		// For refs like 'latest', 'main', 'master', we need to resolve to SHA first
		// then try to find associated tags to determine major version
		sha, err := resolver.Resolve(ctx, owner, repo, change.OriginalRef)
		if err != nil {
			change.Status = "error"
			change.Message = err.Error()
			return line, &change, false, err
		}

		// Try to find tags pointing to this commit to determine major version
		major, detectedPrefix, ok := detectMajorVersionFromCommit(ctx, resolver, actionMatch[1], owner, repo, sha, trackingPrefix)
		if !ok {
			change.Status = "skipped"
			change.Message = fmt.Sprintf("Cannot determine major version from ref %q", change.OriginalRef)
			return line, &change, false, nil
		}
		if trackingPrefix == "" {
			trackingPrefix = detectedPrefix
		}

		safeSHA, safeReason, err := enforceInitSafety(ctx, resolver, actionMatch[1], owner, repo, trackingPrefix, major, sha, safetyWindowDays)
		if err != nil {
			change.Status = "error"
			change.Message = err.Error()
			return line, &change, false, err
		}
		if safeSHA == "" {
			change.Status = "skipped"
			change.Message = safeReason
			return line, &change, false, nil
		}

		// Successfully resolved and found major version
		change.Status = "pinned"
		change.NewRef = safeSHA
		change.TrackingMajor = &major
		change.Message = fmt.Sprintf("Pinned to commit %s", shortCommit(safeSHA))

		newUses := actionMatch[1] + "@" + safeSHA
		comment := mergeTrackingComment(commentValue, trackingPrefix, major)
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

	sha, err := resolver.Resolve(ctx, owner, repo, change.OriginalRef)
	if err != nil {
		change.Status = "error"
		change.Message = err.Error()
		return line, &change, false, err
	}


	safeSHA, safeReason, err := enforceInitSafety(ctx, resolver, actionMatch[1], owner, repo, trackingPrefix, major, sha, safetyWindowDays)
	if err != nil {
		change.Status = "error"
		change.Message = err.Error()
		return line, &change, false, err
	}
	if safeSHA == "" {
		change.Status = "skipped"
		change.Message = safeReason
		return line, &change, false, nil
	}

	change.Status = "pinned"
	change.NewRef = safeSHA
	change.TrackingMajor = &major
	change.Message = fmt.Sprintf("Pinned to commit %s", shortCommit(safeSHA))

	newUses := actionMatch[1] + "@" + safeSHA
	comment := mergeTrackingComment(commentValue, trackingPrefix, major)
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

func detectMajorVersionFromCommit(ctx context.Context, resolver *initResolver, actionPath, owner, repo, sha, trackingPrefix string) (int, string, bool) {
	_ = actionPath
	query := trackingPrefix + "v"
	if strings.TrimSpace(query) == "v" {
		query = defaultTagQueryPrefix
	}
	tags, err := listTagsForActionWithQuery(ctx, resolver.client, owner, repo, query)
	if err != nil {
		return 0, "", false
	}

	var highestMajor int
	var selectedPrefix string
	found := false

	for _, tag := range tags {
		if tag.CommitSHA == sha {
			normalizedTag, ok := normalizeTagForTracking(tag.Name, trackingPrefix)
			if !ok {
				continue
			}
			if _, major, ok := detectTrackingFromRef(normalizedTag); ok {
				prefix := trackingPrefix
				if prefix == "" {
					if detectedPrefix, _, detected := detectTrackingFromRef(tag.Name); detected {
						prefix = detectedPrefix
					}
				}
				if major > highestMajor || !found {
					highestMajor = major
					selectedPrefix = prefix
					found = true
				}
			}
		}
	}

	return highestMajor, selectedPrefix, found
}

func enforceInitSafety(ctx context.Context, resolver *initResolver, actionPath, owner, repo, trackingPrefix string, major int, resolvedSHA string, safetyWindowDays int) (string, string, error) {
	if safetyWindowDays <= 0 {
		return resolvedSHA, "", nil
	}

	cutoff := time.Now().Add(-time.Duration(safetyWindowDays) * 24 * time.Hour)
	when, ok, err := resolver.CommitDate(ctx, owner, repo, resolvedSHA)
	if err != nil {
		return "", "", err
	}
	if ok && !when.After(cutoff) {
		return resolvedSHA, "", nil
	}

	tags, err := listTagsForActionWithQuery(ctx, resolver.client, owner, repo, trackingPrefixForQuery(actionPath, trackingPrefix))
	if err != nil {
		return "", "", err
	}

	for _, tag := range tags {
		normalizedTag, ok := normalizeTagForTracking(tag.Name, trackingPrefix)
		if !ok {
			continue
		}

		tagMajor, tagOK := detectMajorVersion(normalizedTag)
		if !tagOK || tagMajor != major {
			continue
		}

		tagWhen, tagWhenOK, err := resolver.CommitDate(ctx, owner, repo, tag.CommitSHA)
		if err != nil {
			return "", "", err
		}
		if !tagWhenOK || tagWhen.After(cutoff) {
			continue
		}

		return tag.CommitSHA, "", nil
	}

	if ok {
		return "", fmt.Sprintf("No eligible release in major v%d satisfies the %d-day safety window (resolved ref date %s)", major, safetyWindowDays, when.Format(time.RFC3339)), nil
	}
	return "", fmt.Sprintf("No eligible release in major v%d satisfies the %d-day safety window", major, safetyWindowDays), nil
}

func mergeTrackingComment(existing string, prefix string, major int) string {
	trimmed := strings.TrimSpace(existing)
	annotation := trackingAnnotation(prefix, major)

	if trimmed == "" {
		return annotation
	}

	if trackingCommentRe.MatchString(trimmed) {
		return trackingCommentRe.ReplaceAllString(trimmed, annotation)
	}

	return fmt.Sprintf("%s; %s", trimmed, annotation)
}

func detectTrackingFromRef(ref string) (string, int, bool) {
	candidate := ref
	if strings.Contains(candidate, "/") {
		parts := strings.Split(candidate, "/")
		candidate = parts[len(parts)-1]
	}

	if match := trackingRefExpr.FindStringSubmatch(candidate); match != nil {
		major, err := strconv.Atoi(match[2])
		if err == nil {
			return match[1], major, true
		}
	}
	if match := trackingRefBareExpr.FindStringSubmatch(candidate); match != nil {
		major, err := strconv.Atoi(match[1])
		if err == nil {
			return "", major, true
		}
	}
	return "", 0, false
}

func normalizeTagForTracking(tagName, prefix string) (string, bool) {
	if strings.TrimSpace(prefix) == "" {
		if _, _, ok := detectTrackingFromRef(tagName); ok {
			return tagName, true
		}
		return "", false
	}
	if strings.HasPrefix(tagName, prefix) {
		return strings.TrimPrefix(tagName, prefix), true
	}
	return "", false
}

func trackingPrefixForQuery(actionPath, trackingPrefix string) string {
	_ = actionPath
	if strings.TrimSpace(trackingPrefix) != "" {
		return trackingPrefix + "v"
	}
	return defaultTagQueryPrefix
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
	initCmd.Flags().IntVar(&initSafetyWindow, "safety-window", 14, "Minimum release age in days before pinning is allowed (set 0 to disable)")
	rootCmd.AddCommand(initCmd)
}
