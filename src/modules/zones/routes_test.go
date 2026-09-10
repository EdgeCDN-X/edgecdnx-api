package zones

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/auth"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newDNSEndpointTestModule(t *testing.T) *Module {
	t.Helper()

	casbinModel, err := model.NewModelFromString(auth.RBACWithDomainModel)
	if err != nil {
		t.Fatalf("create Casbin model: %v", err)
	}
	enforcer, err := casbin.NewEnforcer(casbinModel)
	if err != nil {
		t.Fatalf("create Casbin enforcer: %v", err)
	}
	if _, err := enforcer.AddPolicy("user@example.com", "project-a", "zone", "read"); err != nil {
		t.Fatalf("add zone read policy: %v", err)
	}
	if _, err := enforcer.AddPolicy("user@example.com", "project-a", "zone", "update"); err != nil {
		t.Fatalf("add zone update policy: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := infrastructurev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add infrastructure scheme: %v", err)
	}
	zone := &infrastructurev1alpha1.Zone{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example.com",
			Namespace: "edgecdnx",
			Labels:    map[string]string{"project": "project-a"},
		},
		Spec: infrastructurev1alpha1.ZoneSpec{Zone: "example.com"},
	}
	return &Module{
		cfg:      Config{Namespace: "edgecdnx"},
		client:   dynamicfake.NewSimpleDynamicClient(scheme, zone),
		enforcer: enforcer,
		middlewares: []gin.HandlerFunc{func(c *gin.Context) {
			c.Set("user_id", "user@example.com")
			c.Next()
		}},
	}
}

func performJSONRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestWriteAPIStatusError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("surfaces wrapped admission error", func(t *testing.T) {
		responseRecorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(responseRecorder)
		statusErr := &apierrors.StatusError{ErrStatus: metav1.Status{
			Code:    http.StatusBadRequest,
			Message: `admission webhook "vzone-v1alpha1.kb.io" denied the request: zone "api.example.com" overlaps with existing zones: [example-com]`,
			Reason:  metav1.StatusReasonInvalid,
			Details: &metav1.StatusDetails{Name: "api-example-com", Kind: "zones"},
		}}

		handled := writeAPIStatusError(context, fmt.Errorf("create zone: %w", statusErr))

		if !handled {
			t.Fatal("expected Kubernetes API status error to be handled")
		}
		if responseRecorder.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, responseRecorder.Code)
		}

		var response struct {
			Error   string                `json:"error"`
			Reason  metav1.StatusReason   `json:"reason"`
			Details *metav1.StatusDetails `json:"details"`
		}
		if err := json.NewDecoder(responseRecorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Error != statusErr.ErrStatus.Message {
			t.Fatalf("expected error %q, got %q", statusErr.ErrStatus.Message, response.Error)
		}
		if response.Reason != metav1.StatusReasonInvalid {
			t.Fatalf("expected reason %q, got %q", metav1.StatusReasonInvalid, response.Reason)
		}
		if response.Details == nil || response.Details.Name != "api-example-com" {
			t.Fatalf("expected status details, got %#v", response.Details)
		}
	})

	t.Run("ignores non-status error", func(t *testing.T) {
		responseRecorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(responseRecorder)

		if writeAPIStatusError(context, errors.New("connection refused")) {
			t.Fatal("expected generic error not to be handled")
		}
		if responseRecorder.Body.Len() != 0 {
			t.Fatalf("expected no response body, got %q", responseRecorder.Body.String())
		}
	})
}

func TestDNSEndpointLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newDNSEndpointTestModule(t)
	router := gin.New()
	module.RegisterRoutes(router)

	createResponse := performJSONRequest(router, http.MethodPost, "/project/project-a/zones/example.com/dns-endpoints", `{
		"dnsName":"www.example.com",
		"recordTTL":300,
		"recordType":"A",
		"targets":["192.0.2.1"]
	}`)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", createResponse.Code, createResponse.Body.String())
	}

	var created infrastructurev1alpha1.DNSEndpoint
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created DNS endpoint: %v", err)
	}
	if created.Spec.RoutingPolicy != "Simple" {
		t.Fatalf("expected Simple routing policy, got %q", created.Spec.RoutingPolicy)
	}
	if created.Labels["project"] != "project-a" || created.Labels["zone"] != "example.com" {
		t.Fatalf("unexpected ownership labels: %#v", created.Labels)
	}

	listResponse := performJSONRequest(router, http.MethodGet, "/project/project-a/zones/example.com/dns-endpoints", "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", listResponse.Code, listResponse.Body.String())
	}
	var listed []infrastructurev1alpha1.DNSEndpoint
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed DNS endpoints: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != created.Name || len(listed[0].Spec.Targets) != 1 {
		t.Fatalf("unexpected listed DNS endpoints: %#v", listed)
	}

	updatePath := "/project/project-a/zones/example.com/dns-endpoints/" + created.Name
	updateResponse := performJSONRequest(router, http.MethodPatch, updatePath, `{
		"recordTTL":600,
		"targets":["192.0.2.2"]
	}`)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update returned %d: %s", updateResponse.Code, updateResponse.Body.String())
	}

	var updated infrastructurev1alpha1.DNSEndpoint
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated DNS endpoint: %v", err)
	}
	if updated.Spec.RecordTTL != 600 || len(updated.Spec.Targets) != 1 || updated.Spec.Targets[0] != "192.0.2.2" {
		t.Fatalf("unexpected updated spec: %#v", updated.Spec)
	}

	deleteResponse := performJSONRequest(router, http.MethodDelete, updatePath, "")
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete returned %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := module.client.Resource(dnsEndpointGVR).Namespace(module.cfg.Namespace).Get(context.Background(), created.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted DNS endpoint to be absent, got %v", err)
	}
}

func TestCreateDNSEndpointValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	module := newDNSEndpointTestModule(t)
	router := gin.New()
	module.RegisterRoutes(router)
	path := "/project/project-a/zones/example.com/dns-endpoints"

	tests := []struct {
		name string
		body string
	}{
		{
			name: "unsupported routing policy",
			body: `{"dnsName":"www.example.com","routingPolicy":"Weighted","recordTTL":300,"recordType":"A","targets":["192.0.2.1"]}`,
		},
		{
			name: "DNS name outside zone",
			body: `{"dnsName":"www.other.example","recordTTL":300,"recordType":"A","targets":["192.0.2.1"]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performJSONRequest(router, http.MethodPost, path, test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, response.Code, response.Body.String())
			}
		})
	}
}
