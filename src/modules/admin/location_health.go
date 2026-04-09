package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const locationHealthAlertSource = "prometheus-alerts"

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
	Alerts          []locationHealthAlert  `json:"alerts,omitempty"`
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
	Location        string                `json:"location,omitempty"`
	Source          string                `json:"source,omitempty"`
	NodeName        string                `json:"nodeName,omitempty"`
	NodeGroupName   string                `json:"nodeGroupName,omitempty"`
	NodeGroupFlavor string                `json:"nodeGroupFlavor,omitempty"`
	IP              string                `json:"ip,omitempty"`
	Instance        string                `json:"instance"`
	Role            string                `json:"role,omitempty"`
	Healthy         bool                  `json:"healthy"`
	Value           string                `json:"value"`
	Timestamp       float64               `json:"timestamp"`
	Matched         bool                  `json:"matched"`
	MatchField      string                `json:"matchField,omitempty"`
	AlertScope      string                `json:"alertScope,omitempty"`
	Alerts          []locationHealthAlert `json:"alerts,omitempty"`
	Labels          map[string]string     `json:"labels"`
	NodeKey         string                `json:"-"`
}

type locationHealthAlert struct {
	AlertName string            `json:"alertName"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp float64           `json:"timestamp,omitempty"`
}

type prometheusVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

type locationNodeReference struct {
	NodeKey         string
	NodeName        string
	NodeGroupName   string
	NodeGroupFlavor string
	IP              string
	MatchField      string
}

type locationAlertMatcherTarget struct {
	LocationName   string
	LocationScoped bool
	Matcher        infrastructurev1alpha1.PrometheusAlertMatcherSpec
	NodeReference  locationNodeReference
}

type matchedLocationAlert struct {
	LocationName   string
	LocationScoped bool
	NodeReference  locationNodeReference
	Alert          locationHealthAlert
	Labels         map[string]string
	Timestamp      float64
}

type locationHealthAccumulator struct {
	Name            string
	Status          string
	MaintenanceMode bool
	Alerts          []locationHealthAlert
	Sources         map[string]*locationHealthSourceAccumulator
}

type locationHealthSourceAccumulator struct {
	Source         string
	HealthyNodes   int
	UnhealthyNodes int
	UnknownNodes   int
	Nodes          []locationHealthNode
}

func (m *Module) buildLocationHealthResponse(ctx context.Context, queryResponse *app.PrometheusQueryResponse, alertResponse *app.PrometheusQueryResponse) (*locationHealthResponse, error) {
	locations, err := m.listLocations(ctx)
	if err != nil {
		return nil, err
	}

	samples, err := decodePrometheusVector(queryResponse.Data.Result)
	if err != nil {
		return nil, err
	}

	alertSamples := []prometheusVectorSample{}
	if alertResponse != nil {
		alertSamples, err = decodePrometheusVector(alertResponse.Data.Result)
		if err != nil {
			return nil, err
		}
	}

	response := buildLocationHealthData(locations, mergeLocationHealthResultType(queryResponse.Data.ResultType, alertResponse), samples, alertSamples)
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

func buildLocationHealthData(locations []infrastructurev1alpha1.Location, resultType string, samples []prometheusVectorSample, alertSamples []prometheusVectorSample) locationHealthResponse {
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
			nodeHealth.NodeKey = reference.NodeKey
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

	for _, alert := range matchLocationAlerts(locations, alertSamples) {
		location, ok := locationIndex[alert.LocationName]
		if !ok {
			continue
		}

		locationAccumulator := accumulators[alert.LocationName]
		if locationAccumulator == nil {
			locationAccumulator = &locationHealthAccumulator{
				Name:            location.Name,
				Status:          location.Status.Status,
				MaintenanceMode: location.Spec.MaintenanceMode,
				Sources:         map[string]*locationHealthSourceAccumulator{},
			}
			accumulators[alert.LocationName] = locationAccumulator
		}

		sourceSet[locationHealthAlertSource] = struct{}{}
		if alert.LocationScoped {
			locationAccumulator.Alerts = append(locationAccumulator.Alerts, alert.Alert)
			appendSyntheticAlertNode(locationAccumulator, locationHealthNode{
				Location:   alert.LocationName,
				Source:     locationHealthAlertSource,
				NodeName:   alert.Alert.AlertName,
				Instance:   alert.Alert.AlertName,
				Role:       "location-alert",
				Healthy:    false,
				Value:      "firing",
				Timestamp:  alert.Timestamp,
				Matched:    true,
				MatchField: "alert",
				AlertScope: "location",
				Alerts:     []locationHealthAlert{alert.Alert},
				Labels:     alert.Labels,
			})
			continue
		}

		if applyAlertToExistingNode(locationAccumulator, alert) {
			continue
		}

		appendSyntheticAlertNode(locationAccumulator, locationHealthNode{
			Location:        alert.LocationName,
			Source:          locationHealthAlertSource,
			NodeName:        alert.NodeReference.NodeName,
			NodeGroupName:   alert.NodeReference.NodeGroupName,
			NodeGroupFlavor: alert.NodeReference.NodeGroupFlavor,
			IP:              alert.NodeReference.IP,
			Instance:        firstNonEmpty(alert.Labels["instance"], alert.NodeReference.IP, alert.NodeReference.NodeName),
			Role:            "node-alert",
			Healthy:         false,
			Value:           "firing",
			Timestamp:       alert.Timestamp,
			Matched:         true,
			MatchField:      "alert",
			AlertScope:      "node",
			Alerts:          []locationHealthAlert{alert.Alert},
			Labels:          alert.Labels,
			NodeKey:         alert.NodeReference.NodeKey,
		})
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
			Alerts:          accumulator.Alerts,
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
		appendLocationNodeIndex(index, node, buildLocationNodeStatusKey(node.Name), "", "")
	}
	for _, nodeGroup := range location.Spec.NodeGroups {
		for _, node := range nodeGroup.Nodes {
			appendLocationNodeIndex(index, node, buildNodeGroupNodeStatusKey(nodeGroup.Name, nodeGroup.Flavor, node.Name), nodeGroup.Name, nodeGroup.Flavor)
		}
	}

	return index
}

func appendLocationNodeIndex(index map[string]locationNodeReference, node infrastructurev1alpha1.NodeSpec, nodeKey string, nodeGroupName string, nodeGroupFlavor string) {
	if normalized := normalizeIP(node.Ipv4); normalized != "" {
		index[normalized] = locationNodeReference{
			NodeKey:         nodeKey,
			NodeName:        node.Name,
			NodeGroupName:   nodeGroupName,
			NodeGroupFlavor: nodeGroupFlavor,
			IP:              normalized,
			MatchField:      "ipv4",
		}
	}
	if normalized := normalizeIP(node.Ipv6); normalized != "" {
		index[normalized] = locationNodeReference{
			NodeKey:         nodeKey,
			NodeName:        node.Name,
			NodeGroupName:   nodeGroupName,
			NodeGroupFlavor: nodeGroupFlavor,
			IP:              normalized,
			MatchField:      "ipv6",
		}
	}
}

func buildLocationAlertMatcherIndex(locations []infrastructurev1alpha1.Location) map[string][]locationAlertMatcherTarget {
	index := map[string][]locationAlertMatcherTarget{}
	for _, location := range locations {
		for _, matcher := range location.Spec.Alerts {
			index[matcher.AlertName] = append(index[matcher.AlertName], locationAlertMatcherTarget{
				LocationName:   location.Name,
				LocationScoped: true,
				Matcher:        matcher,
			})
		}

		for _, node := range location.Spec.Nodes {
			reference := locationNodeReference{
				NodeKey:    buildLocationNodeStatusKey(node.Name),
				NodeName:   node.Name,
				IP:         firstNonEmpty(normalizeIP(node.Ipv4), normalizeIP(node.Ipv6)),
				MatchField: firstNonEmpty(matchFieldForNode(node.Ipv4, "ipv4"), matchFieldForNode(node.Ipv6, "ipv6")),
			}
			for _, matcher := range node.Alerts {
				index[matcher.AlertName] = append(index[matcher.AlertName], locationAlertMatcherTarget{
					LocationName:  location.Name,
					Matcher:       matcher,
					NodeReference: reference,
				})
			}
		}

		for _, nodeGroup := range location.Spec.NodeGroups {
			for _, node := range nodeGroup.Nodes {
				reference := locationNodeReference{
					NodeKey:         buildNodeGroupNodeStatusKey(nodeGroup.Name, nodeGroup.Flavor, node.Name),
					NodeName:        node.Name,
					NodeGroupName:   nodeGroup.Name,
					NodeGroupFlavor: nodeGroup.Flavor,
					IP:              firstNonEmpty(normalizeIP(node.Ipv4), normalizeIP(node.Ipv6)),
					MatchField:      firstNonEmpty(matchFieldForNode(node.Ipv4, "ipv4"), matchFieldForNode(node.Ipv6, "ipv6")),
				}
				for _, matcher := range node.Alerts {
					index[matcher.AlertName] = append(index[matcher.AlertName], locationAlertMatcherTarget{
						LocationName:  location.Name,
						Matcher:       matcher,
						NodeReference: reference,
					})
				}
			}
		}
	}

	return index
}

func matchLocationAlerts(locations []infrastructurev1alpha1.Location, samples []prometheusVectorSample) []matchedLocationAlert {
	matcherIndex := buildLocationAlertMatcherIndex(locations)
	if len(matcherIndex) == 0 || len(samples) == 0 {
		return nil
	}

	matches := make([]matchedLocationAlert, 0)
	for _, sample := range samples {
		if !sample.boolValue() {
			continue
		}

		alertName := strings.TrimSpace(sample.Metric["alertname"])
		if alertName == "" {
			continue
		}

		for _, target := range matcherIndex[alertName] {
			if !alertSampleMatches(target.Matcher, sample.Metric) {
				continue
			}

			matches = append(matches, matchedLocationAlert{
				LocationName:   target.LocationName,
				LocationScoped: target.LocationScoped,
				NodeReference:  target.NodeReference,
				Alert: locationHealthAlert{
					AlertName: alertName,
					Labels:    cloneLabels(sample.Metric),
					Timestamp: sample.timestamp(),
				},
				Labels:    cloneLabels(sample.Metric),
				Timestamp: sample.timestamp(),
			})
		}
	}

	return matches
}

func alertSampleMatches(matcher infrastructurev1alpha1.PrometheusAlertMatcherSpec, labels map[string]string) bool {
	if strings.TrimSpace(labels["alertname"]) != strings.TrimSpace(matcher.AlertName) {
		return false
	}

	if state := strings.TrimSpace(labels["alertstate"]); state != "" && state != "firing" {
		return false
	}

	for key, expected := range matcher.Labels {
		if labels[key] != expected {
			return false
		}
	}

	return true
}

func applyAlertToExistingNode(locationAccumulator *locationHealthAccumulator, alert matchedLocationAlert) bool {
	matchedExistingNode := false
	for _, sourceAccumulator := range locationAccumulator.Sources {
		for index := range sourceAccumulator.Nodes {
			node := &sourceAccumulator.Nodes[index]
			if node.NodeKey == "" || node.NodeKey != alert.NodeReference.NodeKey {
				continue
			}

			node.AlertScope = "node"
			node.Alerts = append(node.Alerts, alert.Alert)
			if node.Healthy {
				node.Healthy = false
				sourceAccumulator.HealthyNodes -= 1
				sourceAccumulator.UnhealthyNodes += 1
			}
			matchedExistingNode = true
		}
	}

	return matchedExistingNode
}

func appendSyntheticAlertNode(locationAccumulator *locationHealthAccumulator, node locationHealthNode) {
	sourceAccumulator := locationAccumulator.Sources[locationHealthAlertSource]
	if sourceAccumulator == nil {
		sourceAccumulator = &locationHealthSourceAccumulator{Source: locationHealthAlertSource}
		locationAccumulator.Sources[locationHealthAlertSource] = sourceAccumulator
	}

	sourceAccumulator.UnhealthyNodes += 1
	sourceAccumulator.Nodes = append(sourceAccumulator.Nodes, node)
}

func mergeLocationHealthResultType(resultType string, alertResponse *app.PrometheusQueryResponse) string {
	if strings.TrimSpace(resultType) != "" {
		return resultType
	}
	if alertResponse == nil {
		return ""
	}
	return alertResponse.Data.ResultType
}

func buildLocationNodeStatusKey(nodeName string) string {
	return nodeName
}

func buildNodeGroupNodeStatusKey(nodeGroupName string, nodeGroupFlavor string, nodeName string) string {
	return fmt.Sprintf("%s/%s/%s", nodeGroupName, nodeGroupFlavor, nodeName)
}

func matchFieldForNode(value string, field string) string {
	if normalizeIP(value) == "" {
		return ""
	}
	return field
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}

	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func (s prometheusVectorSample) boolValue() bool {
	if len(s.Value) < 2 {
		return false
	}

	var numeric float64
	if err := json.Unmarshal(s.Value[1], &numeric); err == nil {
		return numeric == 1
	}

	var value string
	if err := json.Unmarshal(s.Value[1], &value); err == nil {
		parsed, err := strconv.ParseFloat(value, 64)
		return err == nil && parsed == 1
	}

	return false
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
