package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
)

// agentCheckCmd runs the agent-readiness probe on one site without a crawl.
func agentCheckCmd() *cobra.Command {
	var (
		userAgent string
		jsonOut   bool
		noRobots  bool
	)
	cmd := &cobra.Command{
		Use:   "agent-check <url>",
		Short: "Check how ready a site is for AI agents (robots.txt AI rules, llms.txt, markdown, /.well-known agent files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			siteURL := ensureScheme(args[0])
			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			client := crawler.DefaultClient(10*time.Second, nil)
			opts := crawler.AgentCheckOptions{Fetch: crawler.FetchOptions{Client: client, UserAgent: userAgent}}
			if !noRobots {
				rc := &crawler.RobotsChecker{Client: client}
				if err := rc.Init(ctx, siteURL, userAgent); err == nil {
					opts.Allowed = func(_ context.Context, u string) bool { return rc.IsAllowed(u, userAgent) }
				}
			}
			rep := crawler.CheckAgentReadiness(ctx, siteURL, opts)
			if rep.Error != "" {
				return fmt.Errorf("%s: %s", siteURL, rep.Error)
			}
			stats := analysis.BuildAgentReadiness([]types.AgentHostReport{rep}, nil)
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(stats)
			}
			printAgentReport(stats.Hosts[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&userAgent, "user-agent", "Micelio/1.0", "User-Agent for the requests")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the report as JSON")
	cmd.Flags().BoolVar(&noRobots, "no-robots", false, "Do not apply robots.txt to the check's own requests")
	return cmd
}

func printAgentReport(h types.AgentHostReport) {
	fmt.Printf("\nAgent readiness: %s\n", h.URL)
	fmt.Printf("Score %d/100 · %s · %d requests\n", h.Score, h.Level, h.Requests)
	marks := map[string]string{types.AgentPass: "PASS", types.AgentWarn: "WARN", types.AgentFail: "FAIL", types.AgentNA: " -- "}
	for _, cat := range h.Categories {
		score := "n/a"
		if cat.Score >= 0 {
			score = fmt.Sprintf("%d", cat.Score)
		}
		fmt.Printf("\n%s (%s)\n", strings.ToUpper(cat.Category), score)
		for _, c := range h.Checks {
			if c.Category != cat.Category {
				continue
			}
			fmt.Printf("  [%s] %-45s %s\n", marks[c.Status], c.Title, c.Evidence)
			if c.Fix != "" {
				fmt.Printf("         fix: %s\n", c.Fix)
			}
		}
	}
	if len(h.Bots) > 0 {
		fmt.Println("\nAI BOTS (robots.txt rules for the checked page)")
		for _, b := range h.Bots {
			state := "unknown"
			if b.Allowed != nil {
				state = map[bool]string{true: "allowed", false: "blocked"}[*b.Allowed]
			}
			if b.Explicit {
				state += " (own rules)"
			}
			if b.LiveStatus != 0 {
				state += fmt.Sprintf(", live %d", b.LiveStatus)
			}
			fmt.Printf("  %-22s %-12s %-9s %s\n", b.Bot, b.Owner, b.Purpose, state)
		}
	}
	if len(h.ContentSignals) > 0 {
		fmt.Printf("\nContent-Signal: %s\n", strings.Join(h.ContentSignals, " | "))
	}
	fmt.Println()
}
