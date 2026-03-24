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
