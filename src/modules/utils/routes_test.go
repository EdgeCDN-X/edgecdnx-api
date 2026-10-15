package utils

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/gin-gonic/gin"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"success", 200, `{"response":{"rcode":"NOERROR","answer":[{"data":"192.0.2.1"}]}}`, 200},
		{"DNS failure", 200, `{"response":{"rcode":"NXDOMAIN","answer":[]}}`, 200},
		{"router validation", 400, `{"error":"invalid domain"}`, 400},
		{"router failure", 502, `{"error":"failed to query DNS"}`, 502},
		{"HTML", 400, `<html>Bad request</html>`, 502},
		{"array", 200, `[]`, 502},
		{"empty", 200, ``, 502},
		{"redirect", 302, `{"redirect":true}`, 502},
		{"oversized", 200, `{"data":"` + strings.Repeat("a", maxResponseBytes) + `"}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/route" || r.URL.RawQuery != "test=1" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected headers: %v", r.Header)
				}
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if len(payload) != 2 || payload["domain"] != "app.example.com" || payload["source_ip"] != "85.216.128.10" {
					t.Errorf("unexpected payload: %v", payload)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()

			m := New()
			defer m.Shutdown()
			authCalled := false
			m.SetMiddlewares(func(c *gin.Context) { authCalled = true; c.Next() })
			r := gin.New()
			m.RegisterRoutes(r)
			body, err := json.Marshal(resolveRequest{
				NS: upstream.URL + "/route?test=1", Domain: "app.example.com", SourceIP: "85.216.128.10",
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/utils/resolve", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer caller-token")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if !authCalled || w.Code != tc.want || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("auth=%v status=%d body=%s", authCalled, w.Code, w.Body)
			}
			if tc.want == tc.status && w.Body.String() != tc.body {
				t.Fatalf("response changed: %s", w.Body)
			}
		})
	}
}

func TestResolveValidation(t *testing.T) {
	m := New()
	defer m.Shutdown()
	r := gin.New()
	m.RegisterRoutes(r)
	for _, body := range []string{
		``, `{`, `{}`, `{"ns":123,"domain":"app.example.com"}`,
		`{"ns":"ftp://example.com/route","domain":"app.example.com"}`,
		`{"ns":"/route","domain":"app.example.com"}`,
		`{"ns":"http://user:password@example.com/route","domain":"app.example.com"}`,
		`{"ns":"http://example.com/route#fragment","domain":"app.example.com"}`,
		`{"ns":"http://example.com/route","domain":" "}`,
		`{"ns":"http://example.com/route","domain":"app.example.com","source_ip":"invalid"}`,
		`{"ns":"http://example.com/route","domain":"app.example.com","record_type":"BOGUS"}`,
		`{"ns":"http://example.com/route","domain":"app.example.com","record_type":28}`,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/utils/resolve", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%s status=%d response=%s", body, w.Code, w.Body)
		}
	}
}

func TestResolveRecordTypes(t *testing.T) {
	for _, recordType := range []string{"A", "AAAA", "CNAME", "TXT", "MX", "SRV", "NS", " txt "} {
		t.Run(recordType, func(t *testing.T) {
			wantType := strings.ToUpper(strings.TrimSpace(recordType))
			response := `{"response":{"question":[{"type":"` + wantType + `"}],"rcode":"NOERROR"}}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload routeRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.RecordType != wantType || payload.Domain != "_sip._tcp.example.com" {
					t.Errorf("unexpected forwarded request: %+v", payload)
				}
				_, _ = w.Write([]byte(response))
			}))
			defer upstream.Close()
			m := New()
			defer m.Shutdown()
			r := gin.New()
			m.RegisterRoutes(r)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/utils/resolve", strings.NewReader(
				`{"ns":"`+upstream.URL+`/route","domain":"_sip._tcp.example.com","record_type":"`+recordType+`"}`)))
			if w.Code != http.StatusOK || w.Body.String() != response {
				t.Fatalf("status=%d response=%s", w.Code, w.Body)
			}
		})
	}
}

func TestResolveOptionalSourceIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload) != 1 || payload["domain"] != "app.example.com" {
			t.Errorf("unexpected payload: %v", payload)
		}
		_, _ = w.Write([]byte(`{"response":{}}`))
	}))
	defer upstream.Close()
	m := New()
	defer m.Shutdown()
	r := gin.New()
	m.RegisterRoutes(r)
	body := `{"ns":"` + upstream.URL + `/route","domain":"app.example.com"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/utils/resolve", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d response=%s", w.Code, w.Body)
	}
}

func TestResolveTransportErrors(t *testing.T) {
	logger.Init(false)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	endpoint := upstream.URL
	defer upstream.Close()
	m := New()
	defer m.Shutdown()
	m.client.Timeout = 20 * time.Millisecond
	r := gin.New()
	m.RegisterRoutes(r)
	check := func(want int) {
		t.Helper()
		body := `{"ns":"` + endpoint + `/route","domain":"app.example.com"}`
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/utils/resolve", strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("status=%d response=%s", w.Code, w.Body)
		}
	}
	check(http.StatusGatewayTimeout)
	upstream.Close()
	check(http.StatusBadGateway)
}
