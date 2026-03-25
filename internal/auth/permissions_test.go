package auth

import "testing"

func TestOwnedResourceKey(t *testing.T) {
	if got := ownedResourceKey("providerId"); got != "provider_ids" {
		t.Fatalf("unexpected key %q", got)
	}
}

func TestMatchPattern(t *testing.T) {
	params, ok := matchPattern("/v2/providers/:providerId", "/v2/providers/provider-1")
	if !ok {
		t.Fatal("expected path to match")
	}
	if params["providerId"] != "provider-1" {
		t.Fatalf("unexpected param value: %+v", params)
	}
}

func TestOwnsAll(t *testing.T) {
	principal := &Principal{
		OwnedResources: map[string]map[string]struct{}{
			"provider_ids": {"provider-1": {}},
		},
	}
	if !ownsAll(principal, map[string]string{"providerId": "provider-1"}) {
		t.Fatal("expected principal to own provider")
	}
}

func TestValidateRuleRejectsOwnPermissionWithoutIdentifier(t *testing.T) {
	err := validateRule("/v2/zones", map[Permission]struct{}{CreateOwn: {}})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestBestMatchingRulePrefersSpecificRoute(t *testing.T) {
	rule, _, ok := bestMatchingRule([]Rule{
		{Pattern: "/v2/*"},
		{Pattern: "/v2/providers/:providerId"},
	}, "/v2/providers/provider-1")
	if !ok {
		t.Fatal("expected matching rule")
	}
	if rule.Pattern != "/v2/providers/:providerId" {
		t.Fatalf("unexpected rule %q", rule.Pattern)
	}
}

func TestMethodAllowedSupportsExactAndWildcardPatterns(t *testing.T) {
	rules := []MethodRule{
		{Pattern: "com.omlox.identify"},
		{Pattern: "com.vendor.*"},
	}
	if !methodAllowed(rules, "com.omlox.identify") {
		t.Fatal("expected exact rpc method match")
	}
	if !methodAllowed(rules, "com.vendor.reboot") {
		t.Fatal("expected wildcard rpc method match")
	}
	if methodAllowed(rules, "com.omlox.ping") {
		t.Fatal("did not expect unrelated rpc method match")
	}
}

func TestAuthorizeWebSocketTopicSupportsExactAndWildcardPatterns(t *testing.T) {
	registry := &Registry{
		roles: map[string]rolePolicy{
			"reader": {
				WebSocket: WebSocketPolicy{
					Subscribe: []MethodRule{{Pattern: "fence_events"}, {Pattern: "location_*"}},
					Publish:   []MethodRule{{Pattern: "proximity_updates"}},
				},
			},
		},
	}
	principal := &Principal{Roles: []string{"reader"}}

	if err := registry.AuthorizeWebSocketSubscribe(principal, "location_updates"); err != nil {
		t.Fatalf("expected wildcard subscribe to match: %v", err)
	}
	if err := registry.AuthorizeWebSocketPublish(principal, "proximity_updates"); err != nil {
		t.Fatalf("expected exact publish to match: %v", err)
	}
	if err := registry.AuthorizeWebSocketPublish(principal, "location_updates"); err == nil {
		t.Fatal("expected publish denial")
	}
}
