package locations

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	defaultHealthcheckWindow = 15 * time.Minute
	minHealthcheckWindow     = time.Minute
	maxHealthcheckWindow     = 24 * time.Hour
	defaultHealthcheckLimit  = 60
	maxHealthcheckLimit      = 500

	nodeHealthHealthy   = "Healthy"
	nodeHealthUnhealthy = "Unhealthy"
	nodeHealthUnknown   = "Unknown"
)

type locationHealthchecksResponse struct {
	Location string                  `json:"location"`
	From     time.Time               `json:"from"`
	To       time.Time               `json:"to"`
	Nodes    []nodeHealthcheckReport `json:"nodes"`
}

type nodeHealthcheckReport struct {
	Name            string              `json:"name"`
	NodeGroup       string              `json:"nodeGroup,omitempty"`
	Flavor          string              `json:"flavor,omitempty"`
	Ipv4            string              `json:"ipv4,omitempty"`
	Ipv6            string              `json:"ipv6,omitempty"`
	MaintenanceMode bool                `json:"maintenanceMode,omitempty"`
	Configured      bool                `json:"configured"`
	Status          string              `json:"status"`
	Checks          []healthcheckSeries `json:"checks"`
}

type healthcheckSeries struct {
	Name      string              `json:"name"`
	Type      string              `json:"type"`
	Target    string              `json:"target"`
	Alive     bool                `json:"alive"`
	LastCheck time.Time           `json:"lastCheck"`
	Results   []healthcheckResult `json:"results"`
	Sources   []healthcheckSource `json:"sources"`
}

type healthcheckSource struct {
	Source    string              `json:"source"`
	Alive     bool                `json:"alive"`
	LastCheck time.Time           `json:"lastCheck"`
	Results   []healthcheckResult `json:"results"`
}

type healthcheckResult struct {
	Source     string     `json:"source"`
	Time       time.Time  `json:"time"`
	Start      *time.Time `json:"start,omitempty"`
	Code       *int32     `json:"code,omitempty"`
	Message    string     `json:"message"`
	Alive      bool       `json:"alive"`
	DurationMs *float64   `json:"durationMs,omitempty"`
}

func (m *Module) SetHealthcheckDB(db app.HealthcheckReader) {
	m.healthchecks = db
}

func (m *Module) getLocationHealthchecks(c *gin.Context) {
	window, ok := parseHealthcheckWindow(c)
	if !ok {
		return
	}
	limit, ok := parseHealthcheckLimit(c)
	if !ok {
		return
	}
	if m.healthchecks == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": app.ErrHealthcheckDBNotConfigured.Error()})
		return
	}

	object, found := m.getProjectLocation(c)
	if !found {
		return
	}
	var location infrastructurev1alpha1.Location
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &location); err != nil {
		writeLocationError(c, "convert", err)
		return
	}

	activeChecks, err := m.activeLocationChecks(c.Request.Context(), location)
	if err != nil {
		logger.L().Error("Failed to resolve active healthchecks", zap.String("location", location.Name), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to resolve active healthcheck profiles"})
		return
	}

	to := time.Now().UTC()
	from := to.Add(-window)
	// The healthchecker reports locations as "<namespace>/<name>".
	locationKey := m.cfg.Namespace + "/" + location.Name
	records, err := m.healthchecks.RecentHealthchecks(c, locationKey, from)
	if err != nil {
		logger.L().Error("Failed to query healthchecks", zap.String("location", locationKey), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to query healthchecks"})
		return
	}

	c.JSON(http.StatusOK, locationHealthchecksResponse{
		Location: location.Name,
		From:     from,
		To:       to,
		Nodes:    buildNodeHealthcheckReports(location.Spec, filterActiveHealthchecks(records, activeChecks), limit),
	})
}

type healthcheckKey struct{ name, typ, target string }
type activeNodeChecks map[string]map[healthcheckKey]struct{}

func (m *Module) activeLocationChecks(ctx context.Context, location infrastructurev1alpha1.Location) (activeNodeChecks, error) {
	profiles := map[string]infrastructurev1alpha1.HealthCheckProfile{}
	active := activeNodeChecks{}
	for _, group := range location.Spec.NodeGroups {
		for _, node := range group.Nodes {
			ref := group.HealthCheck
			if node.HealthCheck != nil {
				ref = node.HealthCheck
			}
			if ref == nil || ref.Name == "" {
				continue
			}
			profile, exists := profiles[ref.Name]
			if !exists {
				object, err := m.client.Resource(healthCheckProfileGVR).Namespace(location.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
				if err != nil {
					return nil, fmt.Errorf("get healthcheck profile %q: %w", ref.Name, err)
				}
				if object.GetLabels()[locationProjectLabel] != location.Labels[locationProjectLabel] {
					return nil, fmt.Errorf("healthcheck profile %q does not belong to the location's project", ref.Name)
				}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &profile); err != nil {
					return nil, fmt.Errorf("convert healthcheck profile %q: %w", ref.Name, err)
				}
				profiles[ref.Name] = profile
			}
			if active[node.Name] == nil {
				active[node.Name] = map[healthcheckKey]struct{}{}
			}
			for _, probe := range profile.Spec.Probes {
				addActiveProbe(active[node.Name], node, probe)
			}
		}
	}
	return active, nil
}

func addActiveProbe(active map[healthcheckKey]struct{}, node infrastructurev1alpha1.NodeSpec, probe infrastructurev1alpha1.HealthCheckProbeSpec) {
	var stack infrastructurev1alpha1.StackType
	var target string
	switch probe.Type {
	case infrastructurev1alpha1.HealthCheckProbeTypeHTTP:
		if probe.HTTP == nil {
			return
		}
		stack, target = probe.HTTP.Stack, probe.HTTP.Target
	case infrastructurev1alpha1.HealthCheckProbeTypeTCP:
		if probe.TCP == nil {
			return
		}
		stack, target = probe.TCP.Stack, probe.TCP.Target
	case infrastructurev1alpha1.HealthCheckProbeTypeASSUME:
		if probe.Assume != nil {
			active[healthcheckKey{probe.Name, string(probe.Type), string(probe.Assume.Status)}] = struct{}{}
		}
		return
	default:
		return
	}

	stacks := []infrastructurev1alpha1.StackType{stack}
	if stack == infrastructurev1alpha1.StackTypeDual {
		stacks = []infrastructurev1alpha1.StackType{infrastructurev1alpha1.StackTypeIPv4, infrastructurev1alpha1.StackTypeIPv6}
	}
	for _, ipStack := range stacks {
		address := ""
		switch ipStack {
		case infrastructurev1alpha1.StackTypeIPv4:
			address = node.Ipv4
		case infrastructurev1alpha1.StackTypeIPv6:
			address = node.Ipv6
		}
		// The healthchecker skips missing node addresses for Dual, even with a target override.
		if stack == infrastructurev1alpha1.StackTypeDual && address == "" {
			continue
		}
		if target != "" {
			address = target
		}
		active[healthcheckKey{probe.Name, string(probe.Type), address}] = struct{}{}
	}
}

func filterActiveHealthchecks(records []app.HealthcheckRecord, active activeNodeChecks) []app.HealthcheckRecord {
	filtered := make([]app.HealthcheckRecord, 0, len(records))
	for _, record := range records {
		if _, exists := active[record.Node][healthcheckKey{record.Name, record.Type, record.Target}]; exists {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func parseHealthcheckWindow(c *gin.Context) (time.Duration, bool) {
	raw := c.Query("window")
	if raw == "" {
		return defaultHealthcheckWindow, true
	}
	window, err := time.ParseDuration(raw)
	if err != nil || window < minHealthcheckWindow || window > maxHealthcheckWindow {
		c.JSON(http.StatusBadRequest, gin.H{"error": "window must be a duration between 1m and 24h"})
		return 0, false
	}
	return window, true
}

func parseHealthcheckLimit(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultHealthcheckLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxHealthcheckLimit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be an integer between 1 and " + strconv.Itoa(maxHealthcheckLimit)})
		return 0, false
	}
	return limit, true
}

// Records arrive newest first. Keep independent limits for combined and per-source histories,
// and calculate health from each source's latest result, not just the latest result overall.
func buildNodeHealthcheckReports(spec infrastructurev1alpha1.LocationSpec, records []app.HealthcheckRecord, limit int) []nodeHealthcheckReport {
	type seriesKey struct{ name, typ, target string }

	reports := []*nodeHealthcheckReport{}
	byNode := map[string]*nodeHealthcheckReport{}
	seriesByNode := map[string]map[seriesKey]*healthcheckSeries{}
	sourcesBySeries := map[*healthcheckSeries]map[string]*healthcheckSource{}

	for _, group := range spec.NodeGroups {
		for _, node := range group.Nodes {
			if _, exists := byNode[node.Name]; exists {
				continue
			}
			report := &nodeHealthcheckReport{
				Name: node.Name, NodeGroup: group.Name, Flavor: group.Flavor,
				Ipv4: node.Ipv4, Ipv6: node.Ipv6, MaintenanceMode: node.MaintenanceMode,
				Configured: true,
			}
			reports = append(reports, report)
			byNode[node.Name] = report
		}
	}

	unconfigured := []*nodeHealthcheckReport{}
	for _, record := range records {
		report, exists := byNode[record.Node]
		if !exists {
			report = &nodeHealthcheckReport{Name: record.Node}
			unconfigured = append(unconfigured, report)
			byNode[record.Node] = report
		}
		if seriesByNode[record.Node] == nil {
			seriesByNode[record.Node] = map[seriesKey]*healthcheckSeries{}
		}
		key := seriesKey{record.Name, record.Type, record.Target}
		series, exists := seriesByNode[record.Node][key]
		if !exists {
			series = &healthcheckSeries{Name: record.Name, Type: record.Type, Target: record.Target, Alive: true, LastCheck: record.Time}
			seriesByNode[record.Node][key] = series
			sourcesBySeries[series] = map[string]*healthcheckSource{}
		}
		source, exists := sourcesBySeries[series][record.Source]
		if !exists {
			source = &healthcheckSource{Source: record.Source, Alive: record.Alive, LastCheck: record.Time}
			sourcesBySeries[series][record.Source] = source
			series.Alive = series.Alive && source.Alive
		}
		result := healthcheckResult{Source: record.Source, Time: record.Time, Start: record.Start, Code: record.Code, Message: record.Message, Alive: record.Alive}
		if record.Duration != nil {
			ms := float64(*record.Duration) / float64(time.Millisecond)
			result.DurationMs = &ms
		}
		if len(series.Results) < limit {
			series.Results = append(series.Results, result)
		}
		if len(source.Results) < limit {
			source.Results = append(source.Results, result)
		}
	}

	sort.Slice(unconfigured, func(i, j int) bool { return unconfigured[i].Name < unconfigured[j].Name })
	reports = append(reports, unconfigured...)

	out := make([]nodeHealthcheckReport, 0, len(reports))
	for _, report := range reports {
		report.Checks = []healthcheckSeries{}
		for _, series := range seriesByNode[report.Name] {
			for i, j := 0, len(series.Results)-1; i < j; i, j = i+1, j-1 {
				series.Results[i], series.Results[j] = series.Results[j], series.Results[i]
			}
			series.Sources = make([]healthcheckSource, 0, len(sourcesBySeries[series]))
			for _, source := range sourcesBySeries[series] {
				for i, j := 0, len(source.Results)-1; i < j; i, j = i+1, j-1 {
					source.Results[i], source.Results[j] = source.Results[j], source.Results[i]
				}
				series.Sources = append(series.Sources, *source)
			}
			sort.Slice(series.Sources, func(i, j int) bool { return series.Sources[i].Source < series.Sources[j].Source })
			report.Checks = append(report.Checks, *series)
		}
		sort.Slice(report.Checks, func(i, j int) bool {
			a, b := report.Checks[i], report.Checks[j]
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			return a.Target < b.Target
		})
		report.Status = nodeHealthStatus(report.Checks)
		out = append(out, *report)
	}
	return out
}

func nodeHealthStatus(checks []healthcheckSeries) string {
	if len(checks) == 0 {
		return nodeHealthUnknown
	}
	for _, check := range checks {
		if !check.Alive {
			return nodeHealthUnhealthy
		}
	}
	return nodeHealthHealthy
}
