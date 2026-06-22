package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	errWorkflowDirMissing = errors.New("workflow directory missing")

	actionRefExpr     = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)@([^@]+)$`)
	shaExpr           = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	trackingCommentRe = regexp.MustCompile(`ghapm:v(\d+)`)
)

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

func normalizeActionTagName(actionPath, tagName string) (string, bool) {
	parts := strings.Split(actionPath, "/")
	if len(parts) <= 2 {
		return tagName, true
	}

	subpath := strings.Join(parts[2:], "/")
	prefixes := []string{subpath + "-", strings.ReplaceAll(subpath, "/", "-") + "-"}

	for _, prefix := range prefixes {
		if strings.HasPrefix(tagName, prefix) {
			return strings.TrimPrefix(tagName, prefix), true
		}
	}

	return "", false
}
