package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var dashboardAssetPattern = regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)

func TestDashboardFSIncludesIndexAndReferencedAssets(t *testing.T) {
	dashboard := dashboardFS()
	if dashboard == nil {
		t.Fatal("dashboard filesystem is missing index.html")
	}

	index, err := fs.ReadFile(dashboard, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}

	matches := dashboardAssetPattern.FindAllStringSubmatch(string(index), -1)
	if len(matches) == 0 {
		t.Fatal("embedded index.html contains no local asset references")
	}

	for _, match := range matches {
		asset := strings.TrimPrefix(match[1], "/")
		asset, _, _ = strings.Cut(asset, "?")
		if _, err := fs.Stat(dashboard, asset); err != nil {
			t.Errorf("embedded index.html references missing asset %q: %v", asset, err)
		}
	}
}
