package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/types"
)

const (
	bqEndpoint    = "https://bigquery.googleapis.com/bigquery/v2"
	bqMaxBodySize = 16 << 20 // 16 MB
	bqBatchSize   = 1000     // URLs per query batch
)

var safeBqName = regexp.MustCompile(`^[\w.\-]+$`)

// GscBqOptions configures a BigQuery GSC bulk export query.
type GscBqOptions struct {
	Dataset     string
	AccessToken string
	URLs        []string
	Days        int
}

// FetchGscFromBigQuery queries the GSC bulk export table in BigQuery.
// The caller must provide an access token with BigQuery read scope.
// Returns a map of URL → GscData for merge with crawl data.
func FetchGscFromBigQuery(ctx context.Context, opts GscBqOptions) (map[string]*types.GscData, error) {
	if len(opts.URLs) == 0 {
		return map[string]*types.GscData{}, nil
	}

	if opts.AccessToken == "" {
		return nil, fmt.Errorf("bigquery: no access token provided; use --gsc-key-file or run 'micelio gsc-auth' first")
	}

	// Parse project and dataset
	dotIdx := strings.Index(opts.Dataset, ".")
	if dotIdx == -1 {
		return nil, fmt.Errorf(
			"invalid BigQuery dataset format %q; expected project_id.dataset_name (e.g., my-project.searchconsole)",
			opts.Dataset,
		)
	}
	projectID := opts.Dataset[:dotIdx]
	datasetID := opts.Dataset[dotIdx+1:]

	if !safeBqName.MatchString(projectID) || !safeBqName.MatchString(datasetID) {
		return nil, fmt.Errorf("invalid BigQuery project or dataset name; names may only contain alphanumeric characters, hyphens, underscores, and dots")
	}

	days := opts.Days
	if days <= 0 {
		days = 90
	}
	endDate := time.Now().AddDate(0, 0, -3) // GSC ~3 day lag
	startDate := endDate.AddDate(0, 0, -days)
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	results := make(map[string]*types.GscData, len(opts.URLs))
	client := &http.Client{Timeout: 60 * time.Second}

	// Batch URLs to avoid query size limits
	for i := 0; i < len(opts.URLs); i += bqBatchSize {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		end := i + bqBatchSize
		if end > len(opts.URLs) {
			end = len(opts.URLs)
		}
		batch := opts.URLs[i:end]

		// Build parameterized query
		paramNames := make([]string, len(batch))
		queryParams := []bqQueryParam{
			{Name: "start_date", ParameterType: bqParamType{Type: "DATE"}, ParameterValue: bqParamValue{Value: startStr}},
			{Name: "end_date", ParameterType: bqParamType{Type: "DATE"}, ParameterValue: bqParamValue{Value: endStr}},
		}
		for j, u := range batch {
			name := fmt.Sprintf("url_%d", i+j)
			paramNames[j] = "@" + name
			queryParams = append(queryParams, bqQueryParam{
				Name:           name,
				ParameterType:  bqParamType{Type: "STRING"},
				ParameterValue: bqParamValue{Value: u},
			})
		}

		query := fmt.Sprintf(`
			SELECT
				url,
				SUM(impressions) as total_impressions,
				SUM(clicks) as total_clicks,
				SAFE_DIVIDE(SUM(clicks), SUM(impressions)) as avg_ctr,
				SAFE_DIVIDE(SUM(sum_position), SUM(impressions)) as avg_position
			FROM `+"`%s.%s.searchdata_url_impression`"+`
			WHERE data_date BETWEEN @start_date AND @end_date
				AND url IN (%s)
			GROUP BY url`,
			projectID, datasetID, strings.Join(paramNames, ", "))

		rows, err := runBqQuery(ctx, client, opts.AccessToken, projectID, query, queryParams)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "Not found") {
				return nil, fmt.Errorf(
					"BigQuery table not found: %s.%s.searchdata_url_impression\n"+
						"Make sure bulk data export is enabled in Google Search Console settings.\n"+
						"Go to: Search Console > Settings > Bulk data export",
					projectID, datasetID,
				)
			}
			if strings.Contains(errMsg, "Access Denied") || strings.Contains(errMsg, "403") {
				return nil, fmt.Errorf(
					"BigQuery access denied for dataset %s.\n"+
						"Make sure the service account or authenticated user has BigQuery Data Viewer role.\n"+
						"Also ensure the dataset exists and bulk export is enabled.",
					opts.Dataset,
				)
			}
			return nil, fmt.Errorf("BigQuery query failed: %w", err)
		}

		for _, row := range rows {
			if len(row) < 5 {
				continue
			}
			results[row[0]] = &types.GscData{
				Impressions: parseIntFromBQ(row[1]),
				Clicks:      parseIntFromBQ(row[2]),
				CTR:         parseFloatFromBQ(row[3]),
				Position:    math.Round(parseFloatFromBQ(row[4])*10) / 10,
			}
		}
	}

	return results, nil
}

// EstimateQueryCost estimates BigQuery cost for GSC data query.
func EstimateQueryCost(urlCount, days int) (estimatedMB int, estimatedCost string) {
	estimatedRows := urlCount * days * 10
	estimatedBytes := estimatedRows * 200
	estimatedMB = estimatedBytes / (1024 * 1024)
	costUSD := float64(estimatedBytes) / (1024 * 1024 * 1024 * 1024) * 5

	if costUSD < 0.001 {
		estimatedCost = "Free tier (< $0.001)"
	} else if costUSD < 0.01 {
		estimatedCost = fmt.Sprintf("~$%.3f", costUSD)
	} else {
		estimatedCost = fmt.Sprintf("~$%.2f", costUSD)
	}
	return
}

// --- BigQuery REST API types ---

type bqParamType struct {
	Type string `json:"type"`
}

type bqParamValue struct {
	Value string `json:"value"`
}

type bqQueryParam struct {
	Name           string       `json:"name"`
	ParameterType  bqParamType  `json:"parameterType"`
	ParameterValue bqParamValue `json:"parameterValue"`
}

type bqQueryRequest struct {
	Query           string         `json:"query"`
	ParameterMode   string         `json:"parameterMode"`
	QueryParameters []bqQueryParam `json:"queryParameters"`
	UseLegacySql    bool           `json:"useLegacySql"`
}

// runBqQuery executes a BigQuery query and returns rows as [][]string.
func runBqQuery(ctx context.Context, client *http.Client, token, projectID, query string, params []bqQueryParam) ([][]string, error) {
	reqBody := bqQueryRequest{
		Query:           query,
		UseLegacySql:    false,
		QueryParameters: params,
		ParameterMode:   "NAMED",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/projects/%s/queries", bqEndpoint, projectID)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, bqMaxBodySize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var result struct {
		Rows []struct {
			F []struct {
				V string `json:"v"`
			} `json:"f"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	rows := make([][]string, 0, len(result.Rows))
	for _, r := range result.Rows {
		row := make([]string, len(r.F))
		for j, f := range r.F {
			row[j] = f.V
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseIntFromBQ(s string) int {
	f := parseFloatFromBQ(s)
	return int(f)
}

func parseFloatFromBQ(s string) float64 {
	if s == "" || s == "null" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
