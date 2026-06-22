package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var (
	checkWorkflowDir string
	checkOutputJSON  bool

	constCheckExitFindings = 2

	checkCmd = &cobra.Command{
		Use:   "check",
		Short: "Report available updates for pinned actions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := buildCheckReport(checkWorkflowDir)
			if err != nil {
				return err
			}

			if checkOutputJSON {
				if err := writeCheckReportJSON(cmd, report); err != nil {
					return err
				}
			} else {
				writeCheckReportText(cmd, report, checkWorkflowDir)
			}

			if code := checkExitCode(report); code != 0 {
				return newExitCodeError(code, "")
			}

			return nil
		},
	}
)

func checkExitCode(report checkReport) int {
	if report.WorkflowDirMissing {
		return 0
	}

	summary := report.Summary
	if summary.FloatingCount > 0 || summary.DynamicCount > 0 || summary.MissingCount > 0 || summary.InvalidCount > 0 {
		return constCheckExitFindings
	}

	return 0
}

type checkRecord struct {
	Workflow      string `json:"workflow"`
	Line          int    `json:"line"`
	Action        string `json:"action"`
	Ref           string `json:"ref"`
	Pinned        bool   `json:"pinned"`
	TrackingMajor *int   `json:"trackingMajor,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
}

type checkSummary struct {
	WorkflowCount int `json:"workflowCount"`
	ActionCount   int `json:"actionCount"`
	RemoteCount   int `json:"remoteCount"`
	IgnoredCount  int `json:"ignoredCount"`
	FloatingCount int `json:"floatingCount"`
	DynamicCount  int `json:"dynamicCount"`
	MissingCount  int `json:"missingCount"`
	InvalidCount  int `json:"invalidCount"`
	TrackedCount  int `json:"trackedCount"`
}

type checkReport struct {
	Summary            checkSummary  `json:"summary"`
	Actions            []checkRecord `json:"actions"`
	WorkflowDirMissing bool          `json:"workflowDirMissing"`
}

var (
	usesLineExpr = regexp.MustCompile(`^(\s*(?:-\s*)?uses:\s*)(.+?)(\s*)(?:#\s*(.+))?$`)
)

func buildCheckReport(workflowDir string) (checkReport, error) {
	files, err := discoverWorkflowFiles(workflowDir)
	if err != nil {
		if errors.Is(err, errWorkflowDirMissing) {
			return checkReport{WorkflowDirMissing: true}, nil
		}
		return checkReport{}, err
	}

	var (
		records []checkRecord
		summary checkSummary
	)

	summary.WorkflowCount = len(files)

	for _, file := range files {
		fileRecords, err := parseWorkflowFile(file)
		if err != nil {
			return checkReport{}, err
		}

		records = append(records, fileRecords...)
	}

	summary.ActionCount = len(records)

	for _, record := range records {
		if record.Status == "ignored" {
			summary.IgnoredCount++
			continue
		}

		summary.RemoteCount++

		switch record.Status {
		case "floating-ref":
			summary.FloatingCount++
		case "dynamic-ref":
			summary.DynamicCount++
		case "missing-tracking-comment":
			summary.MissingCount++
		case "invalid-tracking-comment":
			summary.InvalidCount++
		case "tracked":
			summary.TrackedCount++
		}
	}

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Workflow == records[j].Workflow {
			return records[i].Line < records[j].Line
		}
		return records[i].Workflow < records[j].Workflow
	})

	return checkReport{
		Summary: summary,
		Actions: records,
	}, nil
}

func parseWorkflowFile(path string) ([]checkRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open workflow %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	line := 0

	var records []checkRecord

	for scanner.Scan() {
		line++
		text := scanner.Text()

		match := usesLineExpr.FindStringSubmatch(text)
		if match == nil {
			continue
		}

		usesValue := strings.TrimSpace(match[2])
		commentValue := ""
		if len(match) >= 5 {
			commentValue = strings.TrimSpace(match[4])
		}

		record := checkRecord{
			Workflow: filepath.ToSlash(path),
			Line:     line,
		}

		actionMatch := actionRefExpr.FindStringSubmatch(usesValue)
		if actionMatch == nil {
			record.Status = "ignored"
			record.Message = "Not a remote action reference"
			record.Ref = usesValue
			records = append(records, record)
			continue
		}

		record.Action = actionMatch[1]
		record.Ref = actionMatch[2]

		if strings.Contains(record.Ref, "${{") {
			record.Status = "dynamic-ref"
			record.Message = "Ref is computed at runtime; ghapm cannot assess it"
			records = append(records, record)
			continue
		}

		record.Pinned = shaExpr.MatchString(record.Ref)

		var trackingMajor *int
		if commentValue != "" {
			if sub := trackingCommentRe.FindStringSubmatch(commentValue); sub != nil {
				majorValue, err := strconv.Atoi(sub[1])
				if err != nil {
					record.Status = "invalid-tracking-comment"
					record.Message = "Unable to parse ghapm major version"
					records = append(records, record)
					continue
				}

				trackingMajor = new(int)
				*trackingMajor = majorValue
				record.TrackingMajor = trackingMajor
			} else {
				record.Status = "invalid-tracking-comment"
				record.Message = "Comment present but no ghapm major version found"
				records = append(records, record)
				continue
			}
		}

		switch {
		case !record.Pinned:
			record.Status = "floating-ref"
			record.Message = "Ref is not pinned to a commit SHA"
		case trackingMajor == nil:
			record.Status = "missing-tracking-comment"
			record.Message = "Pinned action missing '# ghapm:v<major>' tracking comment"
		default:
			record.Status = "tracked"
			record.Message = fmt.Sprintf("Pinned and tracking major v%d", *trackingMajor)
		}

		records = append(records, record)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan workflow %q: %w", path, err)
	}

	return records, nil
}

func writeCheckReportJSON(cmd *cobra.Command, report checkReport) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeCheckReportText(cmd *cobra.Command, report checkReport, workflowDir string) {
	out := cmd.OutOrStdout()

	if report.WorkflowDirMissing {
		fmt.Fprintln(out, colorize(fmt.Sprintf("Workflow directory %s not found; nothing to check", workflowDir), ansiRed))
		return
	}

	if len(report.Actions) == 0 {
		fmt.Fprintln(out, colorize(fmt.Sprintf("No GitHub Actions uses: references found under %s", workflowDir), ansiGray))
		return
	}

	issuesByMessage := make(map[string][]checkRecord)
	var (
		issueOrder []string
		tracked    []checkRecord
		ignored    []checkRecord
	)

	for _, record := range report.Actions {
		switch record.Status {
		case "tracked":
			tracked = append(tracked, record)
		case "ignored":
			ignored = append(ignored, record)
		default:
			message := strings.TrimSpace(record.Message)
			if message == "" {
				message = "Unspecified issue"
			}
			if _, seen := issuesByMessage[message]; !seen {
				issueOrder = append(issueOrder, message)
			}
			issuesByMessage[message] = append(issuesByMessage[message], record)
		}
	}

	needsGap := false

	if len(issueOrder) > 0 {
		fmt.Fprintln(out, colorize("Issues detected:", ansiRed))
		for idx, message := range issueOrder {
			fmt.Fprintln(out, colorize("- "+message, ansiRed))
			for _, issue := range issuesByMessage[message] {
				line := fmt.Sprintf("  - %s:%d %s@%s -> %s", issue.Workflow, issue.Line, issue.Action, issue.Ref, issue.Status)
				fmt.Fprintln(out, colorize(line, ansiRed))
			}
			if idx < len(issueOrder)-1 {
				fmt.Fprintln(out)
			}
		}
		needsGap = true
	}

	if len(tracked) > 0 {
		if needsGap {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, colorize("Tracked actions:", ansiGreen))
		for _, track := range tracked {
			major := 0
			if track.TrackingMajor != nil {
				major = *track.TrackingMajor
			}
			line := fmt.Sprintf("- %s:%d %s@%s (major v%d)", track.Workflow, track.Line, track.Action, track.Ref, major)
			fmt.Fprintln(out, colorize(line, ansiGreen))
		}
		needsGap = true
	}

	if len(ignored) > 0 {
		if needsGap {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, colorize("Ignored references:", ansiGray))
		for _, item := range ignored {
			line := fmt.Sprintf("- %s:%d %s", item.Workflow, item.Line, item.Ref)
			fmt.Fprintln(out, colorize(line, ansiGray))
		}
		needsGap = true
	}

	if needsGap {
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, colorize(fmt.Sprintf("Summary: %d workflows, %d actions (%d remote)", report.Summary.WorkflowCount, report.Summary.ActionCount, report.Summary.RemoteCount), ansiMagenta))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Floating refs: %d", report.Summary.FloatingCount), ansiYellow))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Dynamic refs: %d", report.Summary.DynamicCount), ansiYellow))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Missing tracking comments: %d", report.Summary.MissingCount), ansiYellow))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Invalid tracking comments: %d", report.Summary.InvalidCount), ansiYellow))
	fmt.Fprintln(out, colorize(fmt.Sprintf("- Tracked commits: %d", report.Summary.TrackedCount), ansiGreen))
}

func init() {
	checkCmd.Flags().StringVar(&checkWorkflowDir, "workflows", ".github/workflows", "Directory that contains workflow files")
	checkCmd.Flags().BoolVar(&checkOutputJSON, "json", false, "Emit machine-readable JSON output")
	rootCmd.AddCommand(checkCmd)
}
