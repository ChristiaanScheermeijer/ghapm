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
	upgradeAllowMajor       bool
	upgradeWorkflowDir      string
	upgradeDryRun           bool
	upgradeOutputJSON       bool
	upgradeSafetyWindowDays int
	upgradeUseAPI           bool

	upgradeCmd = &cobra.Command{
		Use:   "upgrade",
		Short: "Move pinned actions forward to the latest safe release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runUpgrade(cmd.Context())
			if err != nil {
				return err
			}

			if upgradeOutputJSON {
				return writeUpgradeReportJSON(cmd, report)
			}

			writeUpgradeReportText(cmd, report, upgradeWorkflowDir, upgradeDryRun, upgradeAllowMajor)
			return nil
		},
	}
)

type upgradeChange struct {
	Workflow              string `json:"workflow"`
	Line                  int    `json:"line"`
	Action                string `json:"action"`
	CurrentRef            string `json:"currentRef"`
	CurrentTag            string `json:"currentTag,omitempty"`
	TrackedMajor          *int   `json:"trackedMajor,omitempty"`
	TargetRef             string `json:"targetRef,omitempty"`
	TargetMajor           *int   `json:"targetMajor,omitempty"`
	TargetTag             string `json:"targetTag,omitempty"`
	Status                string `json:"status"`
	Message               string `json:"message,omitempty"`
	MajorUpgradeAvailable bool   `json:"majorUpgradeAvailable,omitempty"`
	MajorUpgradeTag       string `json:"majorUpgradeTag,omitempty"`
	MajorUpgradeMessage   string `json:"majorUpgradeMessage,omitempty"`
}

type upgradeSummary struct {
	WorkflowCount              int  `json:"workflowCount"`
	ModifiedWorkflows          int  `json:"modifiedWorkflows"`
	ActionCount                int  `json:"actionCount"`
	UpgradedCount              int  `json:"upgradedCount"`
	AlreadyCurrentCount        int  `json:"alreadyCurrentCount"`
	SkippedCount               int  `json:"skippedCount"`
	FailedCount                int  `json:"failedCount"`
	DryRun                     bool `json:"dryRun"`
	AllowMajor                 bool `json:"allowMajor"`
	SafetyWindowDays           int  `json:"safetyWindowDays"`
	MajorUpgradeAvailableCount int  `json:"majorUpgradeAvailableCount"`
}

type upgradeReport struct {
	Summary            upgradeSummary  `json:"summary"`
	Changes            []upgradeChange `json:"changes"`
	WorkflowDirMissing bool            `json:"workflowDirMissing"`
}

type upgradeFileStats struct {
	Actions  int
	Upgraded int
	Already  int
	Skipped  int
	Failed   int
}

type upgradeState int

const (
	upgradeStateNone upgradeState = iota
	upgradeStateCurrent
	upgradeStateUpgrade
)

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeAllowMajor, "major", false, "Allow upgrades to the next major version")
	upgradeCmd.Flags().StringVar(&upgradeWorkflowDir, "workflows", ".github/workflows", "Directory that contains workflow files")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "Preview changes without modifying files")
	upgradeCmd.Flags().BoolVar(&upgradeOutputJSON, "json", false, "Emit machine-readable JSON output")
	upgradeCmd.Flags().IntVar(&upgradeSafetyWindowDays, "safety-window", 14, "Minimum release age in days before an upgrade is allowed (set 0 to disable)")
	upgradeCmd.Flags().BoolVar(&upgradeUseAPI, "api", false, "Use GitHub REST API instead of the gh CLI")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(ctx context.Context) (upgradeReport, error) {
	files, err := discoverWorkflowFiles(upgradeWorkflowDir)
	if err != nil {
		if errors.Is(err, errWorkflowDirMissing) {
			return upgradeReport{WorkflowDirMissing: true, Summary: upgradeSummary{DryRun: upgradeDryRun, AllowMajor: upgradeAllowMajor, SafetyWindowDays: upgradeSafetyWindowDays}}, nil
		}
		return upgradeReport{}, err
	}

	client := newGitHubClient(upgradeUseAPI)

	report := upgradeReport{
		Summary: upgradeSummary{
			WorkflowCount:    len(files),
			DryRun:           upgradeDryRun,
			AllowMajor:       upgradeAllowMajor,
			SafetyWindowDays: upgradeSafetyWindowDays,
		},
	}

	enforceSafety := upgradeSafetyWindowDays > 0
	var cutoff time.Time
	if enforceSafety {
		cutoff = time.Now().Add(-time.Duration(upgradeSafetyWindowDays) * 24 * time.Hour)
	}

	resolver := &tagResolver{
		client:           client,
		allowMajor:       upgradeAllowMajor,
		enforceSafety:    enforceSafety,
		safetyWindowDays: upgradeSafetyWindowDays,
		cutoff:           cutoff,
	}

	for _, file := range files {
		fileChanges, stats, fileChanged, err := processUpgradeWorkflowFile(ctx, file, resolver, upgradeDryRun)
		report.Changes = append(report.Changes, fileChanges...)
		report.Summary.ActionCount += stats.Actions
		report.Summary.UpgradedCount += stats.Upgraded
		report.Summary.AlreadyCurrentCount += stats.Already
		report.Summary.SkippedCount += stats.Skipped
		report.Summary.FailedCount += stats.Failed
		for _, change := range fileChanges {
			if change.MajorUpgradeAvailable {
				report.Summary.MajorUpgradeAvailableCount++
			}
		}

		if err != nil {
			return report, err
		}

		if fileChanged {
			report.Summary.ModifiedWorkflows++
		}
	}

	return report, nil
}

func processUpgradeWorkflowFile(ctx context.Context, path string, resolver *tagResolver, dryRun bool) ([]upgradeChange, upgradeFileStats, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, upgradeFileStats{}, false, fmt.Errorf("read workflow %q: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	newLines := make([]string, len(lines))
	workflowPath := filepath.ToSlash(path)

	var (
		changes    []upgradeChange
		stats      upgradeFileStats
		fileChange bool
	)

	for idx, line := range lines {
		lineNumber := idx + 1

		newLine, change, lineChanged, err := resolver.transformLine(ctx, workflowPath, line, lineNumber)
		if change != nil {
			stats.Actions++
			switch change.Status {
			case "upgraded":
				stats.Upgraded++
			case "up-to-date":
				stats.Already++
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

type tagResolver struct {
	client           githubclient.Client
	allowMajor       bool
	enforceSafety    bool
	cutoff           time.Time
	safetyWindowDays int
}

var upgradeUsesLineExpr = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*)(.+?)(\s*)(?:#\s*(.+))?$`)

func (r *tagResolver) transformLine(ctx context.Context, workflow, line string, lineNumber int) (string, *upgradeChange, bool, error) {
	match := upgradeUsesLineExpr.FindStringSubmatch(line)
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

	actionPath := actionMatch[1]
	parts := strings.Split(actionPath, "/")
	if len(parts) < 2 {
		return line, nil, false, nil
	}

	currentRef := actionMatch[2]

	change := upgradeChange{
		Workflow:   workflow,
		Line:       lineNumber,
		Action:     actionPath,
		CurrentRef: currentRef,
	}

	if strings.Contains(currentRef, "${{") {
		change.Status = "skipped"
		change.Message = "Ref is computed at runtime; cannot upgrade"
		return line, &change, false, nil
	}

	if !shaExpr.MatchString(currentRef) {
		change.Status = "skipped"
		change.Message = "Ref is not pinned to a commit SHA"
		return line, &change, false, nil
	}

	trackingComment := trackingCommentRe.FindStringSubmatch(commentValue)
	if trackingComment == nil {
		change.Status = "skipped"
		change.Message = "Missing '# ghapm:v<major>' tracking comment"
		return line, &change, false, nil
	}

	trackedMajor, err := strconv.Atoi(trackingComment[1])
	if err != nil {
		change.Status = "skipped"
		change.Message = "Unable to parse tracked major from comment"
		return line, &change, false, nil
	}

	change.TrackedMajor = intPtr(trackedMajor)

	tags, err := r.client.ListTags(ctx, parts[0], parts[1])
	if err != nil {
		change.Status = "error"
		change.Message = err.Error()
		return line, &change, false, err
	}

	targetTag, state, reason, majorCandidate, majorReason, err := r.selectUpgradeTarget(ctx, actionPath, parts[0], parts[1], tags, trackedMajor, currentRef)
	if err != nil {
		change.Status = "error"
		change.Message = err.Error()
		return line, &change, false, err
	}

	if majorCandidate != nil {
		change.MajorUpgradeTag = majorCandidate.Name
		if majorReason == "" {
			change.MajorUpgradeAvailable = true
		} else {
			change.MajorUpgradeMessage = majorReason
		}
	} else if majorReason != "" {
		change.MajorUpgradeMessage = majorReason
	}

	switch state {
	case upgradeStateNone:
		change.Status = "skipped"
		if reason != "" {
			change.Message = reason
		} else {
			change.Message = "No eligible tagged release found"
		}
		return line, &change, false, nil
	case upgradeStateCurrent:
		change.Status = "up-to-date"
		return line, &change, false, nil
	case upgradeStateUpgrade:
		// continue below
	default:
		change.Status = "skipped"
		change.Message = "Unknown upgrade state"
		return line, &change, false, nil
	}

	ver := parseTagVersion(targetTag.Name)
	change.TargetMajor = intPtr(ver.major)
	change.TargetTag = targetTag.Name
	change.TargetRef = targetTag.CommitSHA
	change.CurrentTag = findTagForCommit(actionPath, tags, currentRef)
	currentVersion := change.CurrentTag
	if strings.TrimSpace(currentVersion) == "" {
		currentVersion = displayRef(change.CurrentRef)
	}
	if strings.TrimSpace(targetTag.Name) != "" {
		change.Message = fmt.Sprintf("(%s -> %s)", currentVersion, targetTag.Name)
	}

	if change.MajorUpgradeAvailable && change.MajorUpgradeTag == change.TargetTag {
		change.MajorUpgradeAvailable = false
	}

	newUses := actionPath + "@" + targetTag.CommitSHA
	comment := mergeTrackingComment(commentValue, *change.TargetMajor)
	spacing := commentSpacing
	if comment != "" && spacing == "" {
		spacing = " "
	}

	newLine := prefix + newUses
	if comment != "" {
		newLine += spacing + "# " + comment
	}

	change.Status = "upgraded"
	return newLine, &change, newLine != line, nil
}

func (r *tagResolver) selectUpgradeTarget(ctx context.Context, actionPath, owner, repo string, tags []githubclient.Tag, trackedMajor int, currentCommit string) (*githubclient.Tag, upgradeState, string, *githubclient.Tag, string, error) {
	var (
		highestMajor       *githubclient.Tag
		highestMajorVer    tagVersion
		highestMajorSafe   *githubclient.Tag
		highestMajorSafeVer tagVersion
		highestMajorReason string
		sameMajorReason    string
		bestSameMajor      *githubclient.Tag
		bestSameMajorVer   tagVersion
		currentIsPinned    bool
		currentVersion     tagVersion
		hasCurrentVersion  bool
	)

	for _, tag := range tags {
		normalized, ok := normalizeActionTagName(actionPath, tag.Name)
		if !ok {
			continue
		}

		ver := parseTagVersion(normalized)
		if !ver.valid || ver.major != trackedMajor {
			continue
		}

		if strings.EqualFold(tag.CommitSHA, currentCommit) {
			currentIsPinned = true
			if !hasCurrentVersion || ver.compare(currentVersion) > 0 {
				currentVersion = ver
				hasCurrentVersion = true
			}
		}
	}

	for _, tag := range tags {
		normalized, ok := normalizeActionTagName(actionPath, tag.Name)
		if !ok {
			continue
		}

		ver := parseTagVersion(normalized)
		if !ver.valid {
			continue
		}

		if ver.major > trackedMajor {
			if highestMajor == nil || ver.compare(highestMajorVer) > 0 {
				clone := tag
				highestMajor = &clone
				highestMajorVer = ver
			}

			safe, reason, err := r.isTagSafe(ctx, owner, repo, tag.CommitSHA)
			if err != nil {
				return nil, upgradeStateNone, "", highestMajorSafe, reason, err
			}
			if safe {
				if highestMajorSafe == nil || ver.compare(highestMajorSafeVer) > 0 {
					clone := tag
					highestMajorSafe = &clone
					highestMajorSafeVer = ver
					highestMajorReason = ""
				}
			} else if highestMajorReason == "" {
				highestMajorReason = fmt.Sprintf("%s: %s", tag.Name, reason)
			}
			continue
		}

		if ver.major != trackedMajor {
			continue
		}

		safe, reason, err := r.isTagSafe(ctx, owner, repo, tag.CommitSHA)
		if err != nil {
			return nil, upgradeStateNone, "", highestMajorSafe, highestMajorReason, err
		}

		if !safe {
			if sameMajorReason == "" {
				sameMajorReason = fmt.Sprintf("%s: %s", tag.Name, reason)
			}
			continue
		}

		if hasCurrentVersion && ver.compare(currentVersion) <= 0 {
			continue
		}

		clone := tag
		if bestSameMajor == nil || ver.compare(bestSameMajorVer) > 0 {
			bestSameMajor = &clone
			bestSameMajorVer = ver
		}
	}

	if r.allowMajor && highestMajorSafe != nil {
		return highestMajorSafe, upgradeStateUpgrade, "", nil, "", nil
	}

	if bestSameMajor != nil {
		return bestSameMajor, upgradeStateUpgrade, "", highestMajorSafe, highestMajorReason, nil
	}

	var majorCandidate *githubclient.Tag
	var majorReason string
	if highestMajorSafe != nil {
		majorCandidate = highestMajorSafe
	} else if highestMajor != nil {
		majorCandidate = highestMajor
		majorReason = highestMajorReason
	}

	if currentIsPinned {
		return nil, upgradeStateCurrent, "", majorCandidate, majorReason, nil
	}

	if sameMajorReason != "" {
		return nil, upgradeStateNone, sameMajorReason, majorCandidate, majorReason, nil
	}

	return nil, upgradeStateNone, "No eligible tagged release found", majorCandidate, majorReason, nil
}

func (r *tagResolver) isTagSafe(ctx context.Context, owner, repo, sha string) (bool, string, error) {
	if !r.enforceSafety {
		return true, "", nil
	}

	when, ok, err := r.client.CommitDate(ctx, owner, repo, sha)
	if err != nil {
		return false, "", err
	}
	if !ok {
		return false, fmt.Sprintf("release metadata unavailable to enforce %d-day safety window", r.safetyWindowDays), nil
	}
	if when.After(r.cutoff) {
		return false, fmt.Sprintf("release published %s (< %d days)", when.Format(time.RFC3339), r.safetyWindowDays), nil
	}
	return true, "", nil
}

func writeUpgradeReportJSON(cmd *cobra.Command, report upgradeReport) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeUpgradeReportText(cmd *cobra.Command, report upgradeReport, workflowDir string, dryRun, allowMajor bool) {
	out := cmd.OutOrStdout()

	if report.WorkflowDirMissing {
		fmt.Fprintln(out, colorize(fmt.Sprintf("Workflow directory %s not found; nothing to upgrade", workflowDir), ansiRed))
		return
	}

	if report.Summary.ActionCount == 0 {
		fmt.Fprintln(out, colorize(fmt.Sprintf("No GitHub Actions uses: references found under %s", workflowDir), ansiGray))
		return
	}

	printGroup := func(title string, color string, changes []upgradeChange, formatter func(upgradeChange) string) {
		if len(changes) == 0 {
			return
		}
		fmt.Fprintln(out, colorize(title, color))
		for _, change := range changes {
			fmt.Fprintln(out, colorize(formatter(change), color))
		}
		fmt.Fprintln(out)
	}

	printGroup("Upgraded actions:", ansiGreen, report.filterChanges("upgraded"), func(change upgradeChange) string {
		var targetMajor int
		if change.TargetMajor != nil {
			targetMajor = *change.TargetMajor
		} else if change.TrackedMajor != nil {
			targetMajor = *change.TrackedMajor
		}
		currentDisplay := change.CurrentTag
		if currentDisplay == "" {
			currentDisplay = displayRef(change.CurrentRef)
		}
		targetDisplay := change.TargetTag
		if strings.TrimSpace(targetDisplay) == "" {
			targetDisplay = displayRef(change.TargetRef)
		}
		if targetDisplay == "" {
			targetDisplay = "(unknown)"
		}
		base := fmt.Sprintf("- %s:%d %s@%s -> %s (major v%d)", change.Workflow, change.Line, change.Action, currentDisplay, targetDisplay, targetMajor)
		if msg := strings.TrimSpace(change.Message); msg != "" {
			base = base + " " + msg
		}
		suffix := majorSuffix(change)
		if suffix != "" {
			base += suffix
		}
		return base
	})

	printGroup("Already up to date:", ansiCyan, report.filterChanges("up-to-date"), func(change upgradeChange) string {
		currentDisplay := change.CurrentTag
		if currentDisplay == "" {
			currentDisplay = displayRef(change.CurrentRef)
		}
		base := fmt.Sprintf("- %s:%d %s@%s", change.Workflow, change.Line, change.Action, currentDisplay)
		suffix := majorSuffix(change)
		if suffix == "" {
			suffix = " -> Up to date"
		}
		return base + suffix
	})

	printGroup("Skipped:", ansiYellow, report.filterChanges("skipped"), func(change upgradeChange) string {
		currentDisplay := change.CurrentTag
		if currentDisplay == "" {
			currentDisplay = displayRef(change.CurrentRef)
		}
		base := fmt.Sprintf("- %s:%d %s@%s", change.Workflow, change.Line, change.Action, currentDisplay)
		suffix := ""
		if msg := strings.TrimSpace(change.Message); msg != "" {
			suffix = " -> " + msg
		}
		suffix += majorSuffix(change)
		return base + suffix
	})

	printGroup("Failed:", ansiRed, report.filterChanges("error"), func(change upgradeChange) string {
		currentDisplay := change.CurrentTag
		if currentDisplay == "" {
			currentDisplay = displayRef(change.CurrentRef)
		}
		base := fmt.Sprintf("- %s:%d %s@%s", change.Workflow, change.Line, change.Action, currentDisplay)
		suffix := ""
		if msg := strings.TrimSpace(change.Message); msg != "" {
			suffix = " -> " + msg
		}
		return base + suffix
	})

	fmt.Fprintln(out, colorize(fmt.Sprintf("Summary: %d workflows scanned, %d actions processed", report.Summary.WorkflowCount, report.Summary.ActionCount), ansiMagenta))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Upgraded: %d", report.Summary.UpgradedCount), ansiGreen))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Already current: %d", report.Summary.AlreadyCurrentCount), ansiCyan))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Workflows updated: %d", report.Summary.ModifiedWorkflows), ansiMagenta))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Major upgrades available: %d", report.Summary.MajorUpgradeAvailableCount), ansiGreen))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Safety window (days): %d", report.Summary.SafetyWindowDays), ansiGray))

	if dryRun {
		fmt.Fprintln(out, colorize("\nDry run mode; no files were modified.", ansiGray))
	}
	if allowMajor {
		fmt.Fprintln(out, colorize("\nMajor upgrades permitted by --major flag.", ansiMagenta))
	}
}

func (r upgradeReport) filterChanges(status string) []upgradeChange {
	var filtered []upgradeChange
	for _, change := range r.Changes {
		if change.Status == status {
			filtered = append(filtered, change)
		}
	}
	return filtered
}

func majorSuffix(change upgradeChange) string {
	if change.MajorUpgradeAvailable {
		if change.MajorUpgradeTag != "" {
			return fmt.Sprintf(" -> Major upgrade available %s", change.MajorUpgradeTag)
		}
		return " -> Major upgrade available"
	}
	msg := strings.TrimSpace(change.MajorUpgradeMessage)
	if msg != "" {
		return " -> " + msg
	}
	return ""
}

func displayRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

func findTagForCommit(actionPath string, tags []githubclient.Tag, commit string) string {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return ""
	}

	best := ""
	bestVer := tagVersion{}
	found := false
	for _, tag := range tags {
		if !strings.EqualFold(tag.CommitSHA, commit) {
			continue
		}

		normalized, ok := normalizeActionTagName(actionPath, tag.Name)
		if !ok {
			continue
		}

		ver := parseTagVersion(normalized)
		if !ver.valid {
			continue
		}

		if !found || ver.compare(bestVer) > 0 {
			best = tag.Name
			bestVer = ver
			found = true
		}
	}

	if found {
		return best
	}

	return ""
}

type tagVersion struct {
	major int
	minor int
	patch int
	valid bool
}

func (v tagVersion) compare(other tagVersion) int {
	if v.major != other.major {
		if v.major > other.major {
			return 1
		}
		return -1
	}
	if v.minor != other.minor {
		if v.minor > other.minor {
			return 1
		}
		return -1
	}
	if v.patch != other.patch {
		if v.patch > other.patch {
			return 1
		}
		return -1
	}
	return 0
}

func parseTagVersion(name string) tagVersion {
	trimmed := strings.TrimPrefix(name, "v")
	parts := strings.SplitN(trimmed, ".", 3)
	if len(parts) != 3 {
		return tagVersion{}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return tagVersion{}
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return tagVersion{}
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return tagVersion{}
	}
	return tagVersion{major: major, minor: minor, patch: patch, valid: true}
}
