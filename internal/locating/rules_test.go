package locating

import (
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func TestLocatingRuleExpressions(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	generated := now.Add(-5 * time.Second)
	accuracy, floor, name := 0.5, -1.0, "forklift"
	location := gen.Location{ProviderType: "uwb", ProviderId: "tag-a", Source: "hall-a", TimestampGenerated: &generated, Accuracy: &accuracy, Floor: &floor}
	ctx := ruleContext{location: location, provider: gen.LocationProvider{Name: &name}, now: now}
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"type = 'uwb' AND timestamp_diff < 10000", true},
		{"(type = uwb AND accuracy <= 0.5) AND floor = -1", true},
		{"name = 'forklift' AND provider_id = 'tag-a' AND source != 'hall-b'", true},
		{"timestamp_diff >= 5000 AND timestamp_diff < 5001", true},
		{"TRUE AND FALSE", false}, {"type = gps", false}, {"speed != 0", false},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			n, err := parseLocatingExpression(tc.expression)
			if err != nil {
				t.Fatal(err)
			}
			if got := n.evaluate(ctx).boolean; got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	for _, expression := range []string{"", "type =", "accuracy = 'fast'", "speed OR TRUE", "1 AND TRUE", "unknown_property = 2", "(TRUE", "TRUE)", "type = 'uwb", "NaN = 0", "TRUE > FALSE"} {
		t.Run("invalid:"+expression, func(t *testing.T) {
			if _, err := parseLocatingExpression(expression); err == nil {
				t.Fatal("expected invalid expression")
			}
		})
	}
}
