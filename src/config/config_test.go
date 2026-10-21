package config

import "testing"

func TestServicesFeatureGate(t *testing.T) {
	for _, test := range []struct {
		name    string
		gates   string
		enabled bool
	}{
		{"disabled by default", "", false},
		{"enabled explicitly", "cachingreconciler", true},
		{"comma-separated with whitespace", "other, cachingreconciler ,another", true},
		{"unrelated gate", "other", false},
		{"not a substring match", "notcachingreconciler", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := AppConfig{FeatureGates: test.gates}
			modules := cfg.GetAuthenticatedModules()
			found := false
			for _, module := range modules {
				if module.Name == "Services" {
					found = true
					if module.Init() == nil {
						t.Fatal("enabled Services module must have a constructor")
					}
				}
			}
			if found != test.enabled {
				t.Fatalf("Services registered = %v, want %v", found, test.enabled)
			}
			wantCount := 6
			if test.enabled {
				wantCount++
			}
			if len(modules) != wantCount {
				t.Fatalf("other modules must remain enabled: got %d, want %d", len(modules), wantCount)
			}
		})
	}
}

func TestParseDefaultRouteSelector(t *testing.T) {
	selector, err := ParseDefaultRouteSelector(`{"matchLabels":{"project":"acme"},"matchExpressions":[{"key":"edgecdnx.com/route-kind","operator":"In","values":["static"]}]}`)
	if err != nil {
		t.Fatalf("ParseDefaultRouteSelector returned error: %v", err)
	}
	if selector == nil {
		t.Fatal("expected selector, got nil")
	}
	if got := selector.MatchLabels["project"]; got != "acme" {
		t.Fatalf("expected project label acme, got %q", got)
	}
	if len(selector.MatchExpressions) != 1 {
		t.Fatalf("expected 1 match expression, got %d", len(selector.MatchExpressions))
	}
	if selector.MatchExpressions[0].Key != "edgecdnx.com/route-kind" {
		t.Fatalf("expected route-kind key, got %q", selector.MatchExpressions[0].Key)
	}
}

func TestParseDefaultRouteSelectorEmpty(t *testing.T) {
	selector, err := ParseDefaultRouteSelector("")
	if err != nil {
		t.Fatalf("ParseDefaultRouteSelector returned error for empty selector: %v", err)
	}
	if selector != nil {
		t.Fatalf("expected nil selector for empty input, got %#v", selector)
	}
}
