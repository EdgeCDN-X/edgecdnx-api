package zones

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/gin-gonic/gin"
)

func TestDNSEndpointRoutingPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, policy := range []string{"Simple", "Failover", "RoundRobin", "Weighted", "Geolocation"} {
		t.Run(policy, func(t *testing.T) {
			module := newDNSEndpointTestModule(t)
			router := gin.New()
			module.RegisterRoutes(router)
			path := "/project/project-a/zones/example.com/dns-endpoints"
			routing := `"targets":["192.0.2.1"]`
			dynamic := policy != "Simple" && policy != "Failover"
			if dynamic {
				routing = `"routeSelector":{"matchLabels":{"region":"eu"}}`
			}
			response := performJSONRequest(router, http.MethodPost, path, fmt.Sprintf(
				`{"dnsName":"www.example.com","routingPolicy":%q,"recordTTL":300,"recordType":"A",%s}`, policy, routing))
			if response.Code != http.StatusCreated {
				t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
			}
			var created infrastructurev1alpha1.DNSEndpoint
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.Spec.RoutingPolicy != policy {
				t.Fatalf("unexpected policy: %s", created.Spec.RoutingPolicy)
			}
			if dynamic {
				want := map[string]string{"region": "eu", "edgecdnx.com/tenant": "project-a"}
				if created.Spec.RouteSelector == nil || !reflect.DeepEqual(created.Spec.RouteSelector.MatchLabels, want) {
					t.Fatalf("unexpected selector: %#v", created.Spec.RouteSelector)
				}
			}
			itemPath := path + "/" + created.Name
			response = performJSONRequest(router, http.MethodPatch, itemPath, `{"recordTTL":600}`)
			if response.Code != http.StatusOK {
				t.Fatalf("patch returned %d: %s", response.Code, response.Body.String())
			}
			var updated infrastructurev1alpha1.DNSEndpoint
			if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
				t.Fatal(err)
			}
			if updated.Spec.RoutingPolicy != policy || !reflect.DeepEqual(updated.Spec.RouteSelector, created.Spec.RouteSelector) {
				t.Fatalf("patch changed routing: %#v", updated.Spec)
			}
			if dynamic {
				response = performJSONRequest(router, http.MethodPatch, itemPath, `{"routingPolicy":"Simple","targets":["192.0.2.2"]}`)
				if response.Code != http.StatusOK {
					t.Fatalf("switch to Simple returned %d: %s", response.Code, response.Body.String())
				}
				updated = infrastructurev1alpha1.DNSEndpoint{}
				if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
					t.Fatal(err)
				}
				if updated.Spec.RouteSelector != nil || updated.Spec.RoutingPolicy != "Simple" {
					t.Fatalf("switch did not clear selector: %#v", updated.Spec)
				}
			}
		})
	}
}

func TestDNSEndpointRoutingValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, routing := range []string{
		`"routingPolicy":"RoundRobin"`,
		`"routingPolicy":"Weighted","routeSelector":null`,
		`"routingPolicy":"Geolocation","routeSelector":{"matchLabels":{"edgecdnx.com/tenant":"project-b"}}`,
		`"routingPolicy":"Geolocation","routeSelector":{"matchLabels":{"edgecdnx.com/tenant":""}}`,
		`"routingPolicy":"RoundRobin","routeSelector":{"matchLabels":{"bad/key/name":"eu"}}`,
		`"routingPolicy":"Weighted","routeSelector":{"matchLabels":{"region":"bad value"}}`,
		`"routingPolicy":"Weighted","routeSelector":{"matchExpressions":[{"key":"region","operator":"Exists"}]}`,
		`"routingPolicy":"Simple","targets":[]`,
		`"routingPolicy":"Failover"`,
	} {
		t.Run(routing, func(t *testing.T) {
			module := newDNSEndpointTestModule(t)
			router := gin.New()
			module.RegisterRoutes(router)
			path := "/project/project-a/zones/example.com/dns-endpoints"
			response := performJSONRequest(router, http.MethodPost, path, fmt.Sprintf(
				`{"dnsName":"www.example.com","recordTTL":300,"recordType":"A",%s}`, routing))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
			}
			response = performJSONRequest(router, http.MethodPost, path,
				`{"dnsName":"www.example.com","recordTTL":300,"recordType":"A","targets":["192.0.2.1"]}`)
			if response.Code != http.StatusCreated {
				t.Fatalf("setup returned %d: %s", response.Code, response.Body.String())
			}
			var created infrastructurev1alpha1.DNSEndpoint
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			// Failover can reuse the existing Simple targets on PATCH.
			if routing == `"routingPolicy":"Failover"` {
				return
			}
			response = performJSONRequest(router, http.MethodPatch, path+"/"+created.Name, "{"+routing+"}")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("patch returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDNSEndpointRoutingSwitchAndSelectorReplacement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newDNSEndpointTestModule(t)
	router := gin.New()
	module.RegisterRoutes(router)
	path := "/project/project-a/zones/example.com/dns-endpoints"
	response := performJSONRequest(router, http.MethodPost, path,
		`{"dnsName":"www.example.com","recordTTL":300,"recordType":"A","targets":["192.0.2.1"]}`)
	var endpoint infrastructurev1alpha1.DNSEndpoint
	if response.Code != http.StatusCreated {
		t.Fatalf("setup returned %d: %s", response.Code, response.Body.String())
	}

	if err := json.Unmarshal(response.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"routingPolicy":"RoundRobin","targets":[],"routeSelector":{"matchLabels":{"region":"eu"}}}`,
		`{"routingPolicy":"Weighted","routeSelector":{"matchLabels":{}}}`,
	} {
		response = performJSONRequest(router, http.MethodPatch, path+"/"+endpoint.Name, body)
		if response.Code != http.StatusOK {
			t.Fatalf("patch returned %d: %s", response.Code, response.Body.String())
		}
	}
	endpoint = infrastructurev1alpha1.DNSEndpoint{}
	if err := json.Unmarshal(response.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(endpoint.Spec.RouteSelector.MatchLabels, map[string]string{"edgecdnx.com/tenant": "project-a"}) {
		t.Fatalf("selector not replaced: %#v", endpoint.Spec.RouteSelector)
	}
}

func TestFailoverDNSEndpointLocationTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, recordType := range []string{"A", "AAAA", "CNAME", "TXT", "MX", "SRV", "NS"} {
		t.Run(recordType, func(t *testing.T) {
			module := newDNSEndpointTestModule(t)
			router := gin.New()
			module.RegisterRoutes(router)
			path := "/project/project-a/zones/example.com/dns-endpoints"
			dnsName := "www.example.com"
			if recordType == "SRV" {
				dnsName = "_sip._udp.example.com"
			}
			response := performJSONRequest(router, http.MethodPost, path, fmt.Sprintf(
				`{"dnsName":%q,"routingPolicy":"Failover","recordTTL":300,"recordType":%q,"targets":["fra1"]}`, dnsName, recordType))
			if response.Code != http.StatusCreated {
				t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
			}
			var endpoint infrastructurev1alpha1.DNSEndpoint
			if err := json.Unmarshal(response.Body.Bytes(), &endpoint); err != nil {
				t.Fatal(err)
			}
			response = performJSONRequest(router, http.MethodPatch, path+"/"+endpoint.Name, `{"targets":["ams1"]}`)
			if response.Code != http.StatusOK {
				t.Fatalf("patch returned %d: %s", response.Code, response.Body.String())
			}
			if err := json.Unmarshal(response.Body.Bytes(), &endpoint); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(endpoint.Spec.Targets, []string{"ams1"}) {
				t.Fatalf("unexpected Failover targets: %#v", endpoint.Spec.Targets)
			}
		})
	}
}
