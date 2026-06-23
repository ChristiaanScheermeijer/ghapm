package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
)

var (
	errWorkflowDirMissing = errors.New("workflow directory missing")

	actionRefExpr     = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)@([^@]+)$`)
	shaExpr           = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	trackingCommentRe = regexp.MustCompile(`ghapm:\s*([A-Za-z0-9_.-]*?)v(\d+)`)
)

type trackingInfo struct {
	TagPrefix string
	Major     int
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

func intPtr(v int) *int {
	p := new(int)
	*p = v
	return p
}

const defaultTagQueryPrefix = "v"

func listTagsForActionWithQuery(ctx context.Context, client githubclient.Client, owner, repo, query string) ([]githubclient.Tag, error) {
	if withQuery, ok := client.(githubclient.QueryTagLister); ok {
		return withQuery.ListTagsWithQuery(ctx, owner, repo, query)
	}

	tags, err := client.ListTags(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	filtered := make([]githubclient.Tag, 0, len(tags))
	for _, tag := range tags {
		if strings.HasPrefix(strings.ToLower(tag.Name), strings.ToLower(query)) {
			filtered = append(filtered, tag)
		}
	}
	return filtered, nil
}

func parseTrackingComment(comment string) (trackingInfo, bool) {
	match := trackingCommentRe.FindStringSubmatch(comment)
	if match == nil {
		return trackingInfo{}, false
	}

	major, err := strconv.Atoi(match[2])
	if err != nil {
		return trackingInfo{}, false
	}

	return trackingInfo{TagPrefix: match[1], Major: major}, true
}

func trackingAnnotation(prefix string, major int) string {
	return fmt.Sprintf("ghapm:%sv%d", prefix, major)
}
