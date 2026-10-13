package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResourceTypesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(Config{}).RegisterRoutes(router)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/resource-types", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	var got []string
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode resource types response: %v", err)
	}
	want := []string{
		ResourceTypeAll,
		ResourceTypeAPIKey,
		ResourceTypeHealthCheckProfile,
		ResourceTypeLocation,
		ResourceTypePrefixList,
		ResourceTypeProject,
		ResourceTypeService,
		ResourceTypeZone,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected resource types %v, got %v", want, got)
	}
}
