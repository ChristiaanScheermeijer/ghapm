package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
				return writeCheckReportJSON(cmd, report)
			}

			writeCheckReportText(cmd, report, checkWorkflowDir)
			return nil
		},
	}
)

var errWorkflowDirMissing = errors.New("workflow directory missing")

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
	usesLineExpr      = regexp.MustCompile(`^\s*uses:\s*([^\s#]+)\s*(?:#\s*(.+))?$`)
	actionRefExpr     = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)@([^@]+)$`)
	shaExpr           = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	trackingCommentRe = regexp.MustCompile(`ghapm:v(\d+)`)
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

func discoverWorkflowFiles(workflowDir string) ([]string, error) {
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errWorkflowDirMissing
		}
		return nil, fmt.Errorf("read workflow directory %q: %w", workflowDir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		switch strings.ToLower(filepath.Ext(name)) {
		case ".yml", ".yaml":
			files = append(files, filepath.Join(workflowDir, name))
		}
	}

	sort.Strings(files)
	return files, nil
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

		usesValue := strings.TrimSpace(match[1])
		commentValue := strings.TrimSpace(match[2])

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

	if len(report.Actions) == 0 {
		if report.WorkflowDirMissing {
			fmt.Fprintf(out, "Workflow directory %s not found; nothing to check\n", workflowDir)
		} else {
			fmt.Fprintf(out, "No GitHub Actions uses: references found under %s\n", workflowDir)
		}
		return
	}

	var (
		issues  []checkRecord
		tracked []checkRecord
		ignored []checkRecord
	)

	for _, record := range report.Actions {
		switch record.Status {
		case "tracked":
			tracked = append(tracked, record)
		case "ignored":
			ignored = append(ignored, record)
		default:
			issues = append(issues, record)
		}
	}

	if len(issues) > 0 {
		fmt.Fprintln(out, "Issues detected:")
		for _, issue := range issues {
			fmt.Fprintf(out, "- %s:%d %s@%s -> %s\n", issue.Workflow, issue.Line, issue.Action, issue.Ref, issue.Status)
			if issue.Message != "" {
				fmt.Fprintf(out, "  %s\n", issue.Message)
			}
		}
	}

	if len(tracked) > 0 {
		if len(issues) > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, "Tracked actions:")
		for _, track := range tracked {
			major := 0
			if track.TrackingMajor != nil {
				major = *track.TrackingMajor
			}
			fmt.Fprintf(out, "- %s:%d %s@%s (major v%d)\n", track.Workflow, track.Line, track.Action, track.Ref, major)
		}
	}

	if len(ignored) > 0 {
		if len(issues)+len(tracked) > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, "Ignored references:")
		for _, item := range ignored {
			fmt.Fprintf(out, "- %s:%d %s\n", item.Workflow, item.Line, item.Ref)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Summary: %d workflows, %d actions (%d remote)\n", report.Summary.WorkflowCount, report.Summary.ActionCount, report.Summary.RemoteCount)
	fmt.Fprintf(out, "- Floating refs: %d\n", report.Summary.FloatingCount)
	fmt.Fprintf(out, "- Dynamic refs: %d\n", report.Summary.DynamicCount)
	fmt.Fprintf(out, "- Missing tracking comments: %d\n", report.Summary.MissingCount)
	fmt.Fprintf(out, "- Invalid tracking comments: %d\n", report.Summary.InvalidCount)
	fmt.Fprintf(out, "- Tracked commits: %d\n", report.Summary.TrackedCount)
}

func init() {
	checkCmd.Flags().StringVar(&checkWorkflowDir, "workflows", ".github/workflows", "Directory that contains workflow files")
	checkCmd.Flags().BoolVar(&checkOutputJSON, "json", false, "Emit machine-readable JSON output")
	rootCmd.AddCommand(checkCmd)
}
