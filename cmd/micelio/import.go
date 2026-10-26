package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/server"
)

func importCmd() *cobra.Command {
	var (
		serverURL        string
		authToken        string
		linkIntelligence bool
		local            bool
	)
	cmd := &cobra.Command{
		Use:   "import <crawl.db | crawl.jsonl[.gz|.zst]>",
		Short: "Open a crawl made elsewhere (e.g. a cloud server) in the dashboard",
		Long: `Adds a crawl to the dashboard's History and runs the dashboard's analysis
on this computer (stats, issues, PageRank, Universe, directory tree).

Takes the crawl database written by "micelio crawl" (best: it keeps the
crawl settings) or its JSONL output, plain or .gz/.zst.

If a dashboard is running (default http://127.0.0.1:3100) the file is sent to
it. Otherwise the crawl is imported straight into ~/.micelio and appears the
next time the dashboard starts.`,
		Example: `  # On the server
  micelio crawl https://example.com --max-pages 50000 --no-analysis --db site.db
  # On your computer
  scp server:site.db . && micelio import site.db --link-intelligence`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if _, err := os.Stat(path); err != nil {
				return err
			}
			if !local {
				if base, ok := reachableDashboard(serverURL); ok {
					return uploadImport(base, authToken, path, linkIntelligence)
				}
				if cmd.Flags().Changed("server") {
					return fmt.Errorf("no dashboard answering at %s", serverURL)
				}
			}
			return localImport(cmd.Context(), path, linkIntelligence)
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", "http://127.0.0.1:3100", "running dashboard to send the crawl to")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "dashboard auth token (if it was started with --auth-token)")
	cmd.Flags().BoolVar(&linkIntelligence, "link-intelligence", false, "run link analysis (centrality, suggestions) even if the crawl did not")
	cmd.Flags().BoolVar(&local, "local", false, "import into ~/.micelio directly, even if a dashboard is running")
	return cmd
}

func reachableDashboard(base string) (string, bool) {
	base = strings.TrimRight(base, "/")
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/update/status")
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	// 401 still means a dashboard is there (it needs --auth-token).
	return base, resp.StatusCode < 500
}

func uploadImport(base, token, path string, linkIntelligence bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	q := url.Values{"name": {filepath.Base(path)}}
	if linkIntelligence {
		q.Set("linkIntelligence", "1")
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/crawls/import?"+q.Encode(), f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	fmt.Fprintf(os.Stderr, "Sending %s to the dashboard at %s...\n", filepath.Base(path), base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return fmt.Errorf("import failed: %s", e.Error)
		}
		return fmt.Errorf("import failed: %s", resp.Status)
	}
	var job server.CrawlJob
	json.Unmarshal(body, &job)
	fmt.Printf("Imported: %s\nAnalysing in the dashboard. Open %s/#/results/%s\n", job.SeedURL, base, job.ID)
	return nil
}

func localImport(ctx context.Context, path string, linkIntelligence bool) error {
	store, err := server.OpenUiStore()
	if err != nil {
		return err
	}
	defer store.Close()
	manager := server.NewCrawlManager(store)
	defer manager.Close()

	start := time.Now()
	fmt.Fprintf(os.Stderr, "Importing %s...\n", filepath.Base(path))
	job, err := manager.ImportCrawl(ctx, path, server.ImportOptions{LinkIntelligence: linkIntelligence})
	if err != nil {
		return err
	}
	if linkIntelligence || job.Config["linkIntelligence"] == true {
		fmt.Fprintln(os.Stderr, "Running link analysis...")
	}
	manager.WaitBackground()
	fmt.Printf("Imported: %s (%d pages, %d errors) in %s\n", job.SeedURL, job.PageCount, job.ErrorCount, time.Since(start).Round(100*time.Millisecond))
	fmt.Printf("Open it in the dashboard: micelio ui, then History (crawl %s)\n", job.ID)
	return nil
}
