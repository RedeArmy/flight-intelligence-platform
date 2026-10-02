package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file keeps the dashboard, the SLO rules and the metrics catalogue (docs/operations/telemetry.md) in step: a metric
// that is renamed in code and in the catalogue but not in a panel or a rule would leave a dashboard silently empty or an
// alert that can never fire. The rules themselves are tested with promtool (make observability-check); this checks the
// wiring around them.

var (
	// metricCandidate matches the identifiers that look like one of our metrics. Label names, functions and keywords do
	// not start with these prefixes.
	metricCandidate = regexp.MustCompile(`\b(?:http_server_[a-z_]+|auth_failures_[a-z_]+|rate_limited_[a-z_]+|readiness_[a-z_]+|db_pool_[a-z_]+|go_[a-z_]+|slo:[a-z0-9_:]+)\b`)
	histogramSuffix = regexp.MustCompile(`_(bucket|count|sum)$`)
	recordedRule    = regexp.MustCompile(`(?m)^\s*- record: ([a-z0-9_:]+)\s*$`)
	runbookLine     = regexp.MustCompile(`(?m)^\s*runbook: (\S+)\s*$`)
)

// catalogueMetrics returns the Prometheus metric names listed in the metrics table of docs/operations/telemetry.md.
func catalogueMetrics(t *testing.T, root string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "docs", "operations", "telemetry.md"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		if name := strings.Trim(strings.TrimSpace(cells[2]), "`"); name != "" && !strings.ContainsAny(name, " .") {
			names[name] = true
		}
	}
	return names
}

// unknownMetrics returns the metric-looking identifiers of text that are neither in the catalogue nor recorded by a rule.
func unknownMetrics(text string, known map[string]bool) []string {
	seen := map[string]bool{}
	for _, tok := range metricCandidate.FindAllString(text, -1) {
		base := histogramSuffix.ReplaceAllString(tok, "")
		if !known[tok] && !known[base] {
			seen[tok] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tok := range seen {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSLORulesOnlyUseMetricsFromTheCatalogue(t *testing.T) {
	root := moduleRoot(t)
	rules := readFile(t, filepath.Join(root, "deployments", "local", "prometheus-rules", "slo.yml"))
	known := catalogueMetrics(t, root)
	if len(known) < 10 {
		t.Fatalf("parsed only %d metrics from telemetry.md; the table format changed and this test checks nothing", len(known))
	}
	for _, m := range recordedRule.FindAllStringSubmatch(rules, -1) {
		known[m[1]] = true
	}
	if bad := unknownMetrics(rules, known); len(bad) > 0 {
		t.Errorf("slo.yml uses metrics that are not in docs/operations/telemetry.md: %v", bad)
	}
}

func TestEveryAlertHasASeverityAndARunbookThatExists(t *testing.T) {
	root := moduleRoot(t)
	rules := readFile(t, filepath.Join(root, "deployments", "local", "prometheus-rules", "slo.yml"))
	blocks := strings.Split(rules, "- alert: ")[1:]
	if len(blocks) == 0 {
		t.Fatal("no alerts found in slo.yml")
	}
	for _, b := range blocks {
		name, _, _ := strings.Cut(b, "\n")
		if !strings.Contains(b, "severity: page") && !strings.Contains(b, "severity: ticket") {
			t.Errorf("alert %s needs `severity: page` or `severity: ticket`", name)
		}
		m := runbookLine.FindStringSubmatch(b)
		if m == nil {
			t.Errorf("alert %s has no runbook annotation: every alert says where to look (reliability-and-observability.md)", name)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("alert %s points to %s, which does not exist", name, m[1])
		}
		if strings.Contains(b, "severity: page") && !strings.HasPrefix(m[1], "docs/operations/runbooks/") {
			t.Errorf("alert %s pages, so its runbook must be one of docs/operations/runbooks, not %s", name, m[1])
		}
	}
}

// dashboard is the part of a Grafana dashboard this test reads.
type dashboard struct {
	UID    string `json:"uid"`
	Panels []struct {
		ID         int    `json:"id"`
		Title      string `json:"title"`
		Datasource struct {
			UID string `json:"uid"`
		} `json:"datasource"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

// knownMetricNames is the catalogue plus the metrics the SLO rules record and the ones Prometheus itself provides.
func knownMetricNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	known := catalogueMetrics(t, root)
	rules := readFile(t, filepath.Join(root, "deployments", "local", "prometheus-rules", "slo.yml"))
	for _, m := range recordedRule.FindAllStringSubmatch(rules, -1) {
		known[m[1]] = true
	}
	known["ALERTS"] = true
	return known
}

// checkDashboard returns the problems of one dashboard file.
func checkDashboard(t *testing.T, path, dataSourceUID string, known map[string]bool) []string {
	t.Helper()
	name := filepath.Base(path)
	var d dashboard
	if err := json.Unmarshal([]byte(readFile(t, path)), &d); err != nil {
		return []string{name + " is not valid JSON: " + err.Error()}
	}
	var problems []string
	if d.UID == "" || len(d.Panels) == 0 {
		problems = append(problems, name+" needs a uid and at least one panel")
	}
	ids := map[int]bool{}
	for _, p := range d.Panels {
		if ids[p.ID] {
			problems = append(problems, name+": a panel id is used twice")
		}
		ids[p.ID] = true
		problems = append(problems, checkPanel(name, p.Title, p.Datasource.UID, dataSourceUID, p.Targets, known)...)
	}
	return problems
}

func checkPanel(file, title, panelUID, wantUID string, targets []struct {
	Expr string `json:"expr"`
}, known map[string]bool) []string {
	var problems []string
	if panelUID != wantUID {
		problems = append(problems, file+": panel \""+title+"\" uses data source \""+panelUID+"\", but the provisioned one is \""+wantUID+"\"")
	}
	for _, tg := range targets {
		if strings.TrimSpace(tg.Expr) == "" {
			problems = append(problems, file+": panel \""+title+"\" has an empty query")
		}
		if bad := unknownMetrics(tg.Expr, known); len(bad) > 0 {
			problems = append(problems, file+": panel \""+title+"\" queries metrics that are not in the catalogue: "+strings.Join(bad, ", "))
		}
	}
	return problems
}

func TestDashboardsUseTheProvisionedDataSourceAndKnownMetrics(t *testing.T) {
	root := moduleRoot(t)
	graf := filepath.Join(root, "deployments", "local", "grafana")
	ds := readFile(t, filepath.Join(graf, "provisioning", "datasources", "prometheus.yml"))
	uid := regexp.MustCompile(`(?m)^\s+uid: (\S+)\s*$`).FindStringSubmatch(ds)
	if uid == nil {
		t.Fatal("the provisioned data source has no uid")
	}
	known := knownMetricNames(t, root)

	files, err := filepath.Glob(filepath.Join(graf, "dashboards", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards found (%v)", err)
	}
	for _, f := range files {
		for _, p := range checkDashboard(t, f, uid[1], known) {
			t.Error(p)
		}
	}
}

func TestUnknownMetricsFindsWhatItShould(t *testing.T) {
	known := map[string]bool{"http_server_requests_total": true, "http_server_duration_seconds": true, "slo:api_error_ratio:rate5m": true}
	cases := map[string][]string{
		`sum(rate(http_server_requests_total{job="api"}[5m]))`:                   nil,
		`histogram_quantile(0.9, rate(http_server_duration_seconds_bucket[5m]))`: nil,
		`slo:api_error_ratio:rate5m > 0.01`:                                      nil,
		`rate(http_server_request_total[5m])`:                                    {"http_server_request_total"},
		`db_pool_acquired_connections / db_pool_max_connections`:                 {"db_pool_acquired_connections", "db_pool_max_connections"},
		`sum by (job) (up)`:          nil, // not one of ours
		`slo:api_error_ratio:rate7m`: {"slo:api_error_ratio:rate7m"},
	}
	for expr, want := range cases {
		got := unknownMetrics(expr, known)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, want %v", expr, got, want)
		}
	}
}
