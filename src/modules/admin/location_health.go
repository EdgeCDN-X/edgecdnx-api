package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type locationHealthResponse struct {
	Status string             `json:"status"`
	Data   locationHealthData `json:"data"`
}

type locationHealthData struct {
	ResultType       string               `json:"resultType"`
	Sources          []string             `json:"sources"`
	Locations        []locationHealthItem `json:"locations"`
	UnmatchedMetrics []locationHealthNode `json:"unmatchedMetrics,omitempty"`
}

type locationHealthItem struct {
	Name            string                 `json:"name"`
	Status          string                 `json:"status,omitempty"`
	MaintenanceMode bool                   `json:"maintenanceMode,omitempty"`
	Sources         []locationHealthSource `json:"sources"`
}

type locationHealthSource struct {
	Source         string               `json:"source"`
	TotalNodes     int                  `json:"totalNodes"`
	HealthyNodes   int                  `json:"healthyNodes"`
	UnhealthyNodes int                  `json:"unhealthyNodes"`
	UnknownNodes   int                  `json:"unknownNodes"`
	Nodes          []locationHealthNode `json:"nodes"`
}

type locationHealthNode struct {
	Location        string            `json:"location,omitempty"`
	Source          string            `json:"source,omitempty"`
	NodeName        string            `json:"nodeName,omitempty"`
	NodeGroupName   string            `json:"nodeGroupName,omitempty"`
	NodeGroupFlavor string            `json:"nodeGroupFlavor,omitempty"`
	IP              string            `json:"ip,omitempty"`
	Instance        string            `json:"instance"`
	Role            string            `json:"role,omitempty"`
	Healthy         bool              `json:"healthy"`
	Value           string            `json:"value"`
	Timestamp       float64           `json:"timestamp"`
	Matched         bool              `json:"matched"`
	MatchField      string            `json:"matchField,omitempty"`
	Labels          map[string]string `json:"labels"`
}

type prometheusVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

type locationNodeReference struct {
	NodeName        string
	NodeGroupName   string
	NodeGroupFlavor string
	IP              string
	MatchField      string
}

type locationHealthAccumulator struct {
	Name            string
	Status          string
	MaintenanceMode bool
	Sources         map[string]*locationHealthSourceAccumulator
}

type locationHealthSourceAccumulator struct {
	Source         string
	HealthyNodes   int
	UnhealthyNodes int
	UnknownNodes   int
	Nodes          []locationHealthNode
}

func (m *Module) buildLocationHealthResponse(ctx context.Context, queryResponse *app.PrometheusQueryResponse) (*locationHealthResponse, error) {
	locations, err := m.listLocations(ctx)
	if err != nil {
		return nil, err
	}

	samples, err := decodePrometheusVector(queryResponse.Data.Result)
	if err != nil {
		return nil, err
	}

	response := buildLocationHealthData(locations, queryResponse.Data.ResultType, samples)
	response.Status = queryResponse.Status

	return &response, nil
}

func (m *Module) listLocations(ctx context.Context) ([]infrastructurev1alpha1.Location, error) {
	objList, err := m.dynClient.Resource(locationGVR).Namespace(m.cfg.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list locations: %w", err)
	}

	items := make([]infrastructurev1alpha1.Location, 0, len(objList.Items))
	for _, item := range objList.Items {
		location := &infrastructurev1alpha1.Location{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, location); err != nil {
			return nil, fmt.Errorf("failed to convert location: %w", err)
		}
		items = append(items, *location)
	}

	return items, nil
}

func buildLocationHealthData(locations []infrastructurev1alpha1.Location, resultType string, samples []prometheusVectorSample) locationHealthResponse {
	locationIndex := make(map[string]infrastructurev1alpha1.Location, len(locations))
	nodeIndex := make(map[string]map[string]locationNodeReference, len(locations))
	for _, location := range locations {
		locationIndex[location.Name] = location
		nodeIndex[location.Name] = buildLocationNodeIndex(location)
	}

	accumulators := make(map[string]*locationHealthAccumulator)
	sourceSet := map[string]struct{}{}
	unmatchedMetrics := make([]locationHealthNode, 0)

	for _, sample := range samples {
		locationName := strings.TrimSpace(sample.Metric["location"])
		source := strings.TrimSpace(sample.Metric["cluster"])
		if source == "" {
			source = "unknown"
		}
		sourceSet[source] = struct{}{}

		nodeHealth := locationHealthNode{
			Location:  locationName,
			Source:    source,
			Instance:  sample.Metric["instance"],
			Role:      sample.Metric["role"],
			Healthy:   sample.health(),
			Value:     sample.value(),
			Timestamp: sample.timestamp(),
			Labels:    sample.Metric,
		}

		if ip := normalizeIPFromInstance(nodeHealth.Instance); ip != "" {
			nodeHealth.IP = ip
		}

		location, ok := locationIndex[locationName]
		if !ok {
			unmatchedMetrics = append(unmatchedMetrics, nodeHealth)
			continue
		}

		locationAccumulator := accumulators[locationName]
		if locationAccumulator == nil {
			locationAccumulator = &locationHealthAccumulator{
				Name:            location.Name,
				Status:          location.Status.Status,
				MaintenanceMode: location.Spec.MaintenanceMode,
				Sources:         map[string]*locationHealthSourceAccumulator{},
			}
			accumulators[locationName] = locationAccumulator
		}

		sourceAccumulator := locationAccumulator.Sources[source]
		if sourceAccumulator == nil {
			sourceAccumulator = &locationHealthSourceAccumulator{Source: source}
			locationAccumulator.Sources[source] = sourceAccumulator
		}

		if reference, found := nodeIndex[locationName][nodeHealth.IP]; found {
			nodeHealth.NodeName = reference.NodeName
			nodeHealth.NodeGroupName = reference.NodeGroupName
			nodeHealth.NodeGroupFlavor = reference.NodeGroupFlavor
			nodeHealth.Matched = true
			nodeHealth.MatchField = reference.MatchField
		} else {
			sourceAccumulator.UnknownNodes += 1
		}

		if nodeHealth.Healthy {
			sourceAccumulator.HealthyNodes += 1
		} else {
			sourceAccumulator.UnhealthyNodes += 1
		}

		sourceAccumulator.Nodes = append(sourceAccumulator.Nodes, nodeHealth)
	}

	locationItems := make([]locationHealthItem, 0, len(accumulators))
	for _, accumulator := range accumulators {
		sources := make([]locationHealthSource, 0, len(accumulator.Sources))
		for _, sourceAccumulator := range accumulator.Sources {
			sort.Slice(sourceAccumulator.Nodes, func(i, j int) bool {
				if sourceAccumulator.Nodes[i].Healthy != sourceAccumulator.Nodes[j].Healthy {
					return !sourceAccumulator.Nodes[i].Healthy
				}
				if sourceAccumulator.Nodes[i].Matched != sourceAccumulator.Nodes[j].Matched {
					return sourceAccumulator.Nodes[i].Matched
				}
				if sourceAccumulator.Nodes[i].NodeName != sourceAccumulator.Nodes[j].NodeName {
					return sourceAccumulator.Nodes[i].NodeName < sourceAccumulator.Nodes[j].NodeName
				}
				return sourceAccumulator.Nodes[i].Instance < sourceAccumulator.Nodes[j].Instance
			})

			sources = append(sources, locationHealthSource{
				Source:         sourceAccumulator.Source,
				TotalNodes:     len(sourceAccumulator.Nodes),
				HealthyNodes:   sourceAccumulator.HealthyNodes,
				UnhealthyNodes: sourceAccumulator.UnhealthyNodes,
				UnknownNodes:   sourceAccumulator.UnknownNodes,
				Nodes:          sourceAccumulator.Nodes,
			})
		}

		sort.Slice(sources, func(i, j int) bool {
			return sources[i].Source < sources[j].Source
		})

		locationItems = append(locationItems, locationHealthItem{
			Name:            accumulator.Name,
			Status:          accumulator.Status,
			MaintenanceMode: accumulator.MaintenanceMode,
			Sources:         sources,
		})
	}

	sort.Slice(locationItems, func(i, j int) bool {
		return locationItems[i].Name < locationItems[j].Name
	})

	sources := make([]string, 0, len(sourceSet))
	for source := range sourceSet {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	sort.Slice(unmatchedMetrics, func(i, j int) bool {
		if unmatchedMetrics[i].Source != unmatchedMetrics[j].Source {
			return unmatchedMetrics[i].Source < unmatchedMetrics[j].Source
		}
		if unmatchedMetrics[i].Location != unmatchedMetrics[j].Location {
			return unmatchedMetrics[i].Location < unmatchedMetrics[j].Location
		}
		return unmatchedMetrics[i].Instance < unmatchedMetrics[j].Instance
	})

	return locationHealthResponse{
		Data: locationHealthData{
			ResultType:       resultType,
			Sources:          sources,
			Locations:        locationItems,
			UnmatchedMetrics: unmatchedMetrics,
		},
	}
}

func buildLocationNodeIndex(location infrastructurev1alpha1.Location) map[string]locationNodeReference {
	index := map[string]locationNodeReference{}
	for _, node := range location.Spec.Nodes {
		appendLocationNodeIndex(index, node, "", "")
	}
	for _, nodeGroup := range location.Spec.NodeGroups {
		for _, node := range nodeGroup.Nodes {
			appendLocationNodeIndex(index, node, nodeGroup.Name, nodeGroup.Flavor)
		}
	}

	return index
}

func appendLocationNodeIndex(index map[string]locationNodeReference, node infrastructurev1alpha1.NodeSpec, nodeGroupName string, nodeGroupFlavor string) {
	if normalized := normalizeIP(node.Ipv4); normalized != "" {
		index[normalized] = locationNodeReference{
			NodeName:        node.Name,
			NodeGroupName:   nodeGroupName,
			NodeGroupFlavor: nodeGroupFlavor,
			IP:              normalized,
			MatchField:      "ipv4",
		}
	}
	if normalized := normalizeIP(node.Ipv6); normalized != "" {
		index[normalized] = locationNodeReference{
			NodeName:        node.Name,
			NodeGroupName:   nodeGroupName,
			NodeGroupFlavor: nodeGroupFlavor,
			IP:              normalized,
			MatchField:      "ipv6",
		}
	}
}

func decodePrometheusVector(raw json.RawMessage) ([]prometheusVectorSample, error) {
	if len(raw) == 0 {
		return []prometheusVectorSample{}, nil
	}

	samples := []prometheusVectorSample{}
	if err := json.Unmarshal(raw, &samples); err != nil {
		return nil, fmt.Errorf("failed to decode prometheus vector: %w", err)
	}

	return samples, nil
}

func (s prometheusVectorSample) value() string {
	if len(s.Value) < 2 {
		return ""
	}

	var value string
	if err := json.Unmarshal(s.Value[1], &value); err == nil {
		return value
	}

	var numeric float64
	if err := json.Unmarshal(s.Value[1], &numeric); err == nil {
		return fmt.Sprintf("%v", numeric)
	}

	return ""
}

func (s prometheusVectorSample) health() bool {
	return s.value() == "1"
}

func (s prometheusVectorSample) timestamp() float64 {
	if len(s.Value) == 0 {
		return 0
	}

	var timestamp float64
	if err := json.Unmarshal(s.Value[0], &timestamp); err == nil {
		return timestamp
	}

	return 0
}

func normalizeIPFromInstance(instance string) string {
	trimmed := strings.TrimSpace(instance)
	if trimmed == "" {
		return ""
	}

	toParse := trimmed
	if !strings.Contains(toParse, "://") {
		toParse = "http://" + toParse
	}

	parsed, err := url.Parse(toParse)
	if err == nil {
		if normalized := normalizeIP(parsed.Hostname()); normalized != "" {
			return normalized
		}
	}

	host := trimmed
	if strings.Contains(host, "/") {
		segments := strings.SplitN(host, "/", 2)
		host = segments[0]
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}

	return normalizeIP(host)
}

func normalizeIP(value string) string {
	trimmed := strings.Trim(strings.TrimSpace(value), "[]")
	if trimmed == "" {
		return ""
	}

	parsed := net.ParseIP(trimmed)
	if parsed == nil {
		return ""
	}

	return parsed.String()
}
