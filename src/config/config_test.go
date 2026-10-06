package config

import "testing"

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
