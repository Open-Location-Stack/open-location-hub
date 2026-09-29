package hub

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/transform"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestLocatingRuleExpressions(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	generated := now.Add(-5 * time.Second)
	location := gen.Location{ProviderType: "uwb", ProviderId: "tag-a", Source: "hall-a", TimestampGenerated: &generated, Accuracy: float64Ptr(0.5), Floor: float64Ptr(-1)}
	ctx := ruleContext{location: location, provider: gen.LocationProvider{Name: stringPtrValueRef("forklift")}, now: now}
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

func TestLocatingRulesPublishedPriorityAndAgeExample(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	rules := []gen.LocatingRule{{Expression: "type = 'uwb' AND timestamp_diff < 10000", Priority: 10}, {Expression: "type = 'gps' AND accuracy < 10", Priority: 9}}
	service, trackable := selectionTestService(t, &now, &rules)
	send := func(provider, technology string, age time.Duration) {
		t.Helper()
		loc := testLocation(t, stringPtrValueRef("EPSG:4326"))
		loc.ProviderId = provider
		loc.ProviderType = technology
		at := now.Add(-age)
		loc.TimestampGenerated = &at
		loc.Accuracy = float64Ptr(5)
		if err := service.ProcessLocations(context.Background(), []gen.Location{loc}); err != nil {
			t.Fatal(err)
		}
	}
	selected := func(want string) {
		t.Helper()
		loc, err := service.GetTrackableLocation(context.Background(), trackable.Id)
		if err != nil {
			t.Fatal(err)
		}
		if loc.ProviderId != want {
			t.Fatalf("selected %s want %s", loc.ProviderId, want)
		}
		motion, err := service.GetTrackableMotion(context.Background(), trackable.Id)
		if err != nil || motion.Location.ProviderId != want {
			t.Fatalf("motion must be available with collisions disabled: %+v %v", motion, err)
		}
	}
	send("uwb-tag", "uwb", 0)
	selected("uwb-tag")
	now = now.Add(5 * time.Second)
	send("gps-tag", "gps", 0)
	selected("uwb-tag")
	now = now.Add(15 * time.Second)
	send("gps-tag", "gps", 0)
	selected("gps-tag")
	// A new weak observation can make a previously retained candidate win after
	// the former winner's time-sensitive rule expires.
	now = now.Add(time.Second)
	send("uwb-tag", "uwb", 15*time.Second)
	selected("gps-tag")
}

func TestTrackableSelectionRejectsOldAndEqualGeneratedTimestamps(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	service, trackable := selectionTestService(t, &now, nil)
	send := func(provider string, offset time.Duration) {
		t.Helper()
		loc := testLocation(t, stringPtrValueRef("EPSG:4326"))
		loc.ProviderId = provider
		at := now.Add(offset)
		loc.TimestampGenerated = &at
		if err := service.ProcessLocations(context.Background(), []gen.Location{loc}); err != nil {
			t.Fatal(err)
		}
	}
	send("uwb-tag", 0)
	send("gps-tag", -time.Second)
	send("gps-tag", 0)
	loc, err := service.GetTrackableLocation(context.Background(), trackable.Id)
	if err != nil || loc.ProviderId != "uwb-tag" {
		t.Fatalf("old/equal update replaced selection: %+v %v", loc, err)
	}
	send("gps-tag", time.Second)
	send("gps-tag", -time.Second)
	loc, err = service.GetTrackableLocation(context.Background(), trackable.Id)
	if err != nil || loc.ProviderId != "gps-tag" || !loc.TimestampGenerated.Equal(now.Add(time.Second)) {
		t.Fatalf("out of order update replaced newest: %+v %v", loc, err)
	}
}

func TestRuleSelectionKeepsProviderStreamIndependent(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	rules := []gen.LocatingRule{{Expression: "type = 'uwb'", Priority: 10}}
	service, trackable := selectionTestService(t, &now, &rules)
	ch, unsubscribe := service.bus.Subscribe(16)
	defer unsubscribe()
	for i, p := range []string{"uwb-tag", "gps-tag"} {
		loc := testLocation(t, stringPtrValueRef("EPSG:4326"))
		loc.ProviderId = p
		loc.ProviderType = []string{"uwb", "gps"}[i]
		at := now.Add(time.Duration(i) * time.Second)
		loc.TimestampGenerated = &at
		if err := service.ProcessLocations(context.Background(), []gen.Location{loc}); err != nil {
			t.Fatal(err)
		}
	}
	events := collectEvents(ch, 3)
	providers, motions := 0, 0
	for _, event := range events {
		switch event.Kind {
		case EventLocation:
			providers++
		case EventTrackableMotion:
			motions++
			m, err := Decode[TrackableMotionEnvelope](event)
			if err != nil || m.Motion.Location.ProviderId != "uwb-tag" || m.Motion.Id != trackable.Id.String() {
				t.Fatalf("unexpected selected motion %+v %v", m, err)
			}
		}
	}
	if providers != 2 || motions != 1 {
		t.Fatalf("provider=%d motion=%d want 2/1", providers, motions)
	}
}

func TestNormalizeTrackableRejectsInvalidLocatingRule(t *testing.T) {
	rules := []gen.LocatingRule{{Expression: "system('anything')", Priority: 1}}
	if _, _, err := normalizeTrackable(gen.TrackableWrite{Type: gen.TrackableWriteTypeVirtual, LocatingRules: &rules}, uuid.Nil); err == nil {
		t.Fatal("invalid expression must fail before persistence")
	}
	rules[0] = gen.LocatingRule{Expression: "TRUE", Priority: -1}
	if _, _, err := normalizeTrackable(gen.TrackableWrite{Type: gen.TrackableWriteTypeVirtual, LocatingRules: &rules}, uuid.Nil); err == nil {
		t.Fatal("negative priority must fail")
	}
}

func selectionTestService(t *testing.T, now *time.Time, rules *[]gen.LocatingRule) (*Service, gen.Trackable) {
	t.Helper()
	providers := []string{"uwb-tag", "gps-tag"}
	trackable := gen.Trackable{Id: uuid.New(), Type: gen.TrackableTypeVirtual, LocationProviders: &providers, LocatingRules: rules}
	clock := func() time.Time { return *now }
	s := &Service{now: clock, state: NewProcessingState(clock), bus: NewEventBus(), logger: zap.NewNop(), crsTransformer: transform.NewCRSTransformer(), transformCache: transform.NewCache(), cfg: Config{LocationTTL: time.Minute, DedupTTL: time.Minute}, metadata: &MetadataCache{snapshot: newMetadataSnapshot(nil, nil, []trackableRecord{{Trackable: trackable}}, nil)}}
	return s, trackable
}

func TestRuleSelectionCanChoosePreviouslyRetainedProvider(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	rules := []gen.LocatingRule{{Expression: "type = 'uwb' AND timestamp_diff < 10000", Priority: 10}, {Expression: "type = 'gps'", Priority: 9}, {Expression: "TRUE", Priority: 1}}
	service, trackable := selectionTestService(t, &now, &rules)
	send := func(provider, technology string, generated time.Time) {
		t.Helper()
		loc := testLocation(t, stringPtrValueRef("EPSG:4326"))
		loc.ProviderId = provider
		loc.ProviderType = technology
		loc.TimestampGenerated = &generated
		if err := service.ProcessLocations(context.Background(), []gen.Location{loc}); err != nil {
			t.Fatal(err)
		}
	}
	send("uwb-tag", "uwb", now)
	now = now.Add(5 * time.Second)
	gpsTime := now
	send("gps-tag", "gps", gpsTime)
	now = now.Add(15 * time.Second)
	send("uwb-tag", "uwb", now.Add(-14*time.Second))
	selected, err := service.GetTrackableLocation(context.Background(), trackable.Id)
	if err != nil || selected.ProviderId != "gps-tag" || !selected.TimestampGenerated.Equal(gpsTime) {
		t.Fatalf("expected retained GPS candidate, got %+v %v", selected, err)
	}
}

func TestConcurrentProvidersPreserveNewestTrackableSelection(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	service, trackable := selectionTestService(t, &now, nil)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			loc := testLocation(t, stringPtrValueRef("EPSG:4326"))
			loc.ProviderId = []string{"uwb-tag", "gps-tag"}[i%2]
			generated := now.Add(time.Duration(i) * time.Millisecond)
			loc.TimestampGenerated = &generated
			if err := service.ProcessLocations(context.Background(), []gen.Location{loc}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	selected, err := service.GetTrackableLocation(context.Background(), trackable.Id)
	if err != nil || !selected.TimestampGenerated.Equal(now.Add(31*time.Millisecond)) {
		t.Fatalf("newest concurrent candidate lost: %+v %v", selected, err)
	}
}

func TestReprocessTrackablePreservesProviderDataAndEmitsOnlyChanges(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	service, trackable := selectionTestService(t, &now, nil)
	ch, unsubscribe := service.bus.Subscribe(16)
	defer unsubscribe()
	location := testLocation(t, stringPtrValueRef("EPSG:4326"))
	location.ProviderId = "uwb-tag"
	location.TimestampGenerated = &now
	if err := service.ProcessLocations(context.Background(), []gen.Location{location}); err != nil {
		t.Fatal(err)
	}
	_ = collectEvents(ch, 2)
	before, _ := json.Marshal(service.state.ListLatestLocations())
	if err := service.ReprocessTrackable(context.Background(), trackable.Id); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		t.Fatalf("unchanged reprocessing emitted %s", event.Kind)
	default:
	}
	trackable.Name = stringPtrValueRef("renamed asset")
	service.metadata.UpsertTrackable(trackable, "renamed")
	if err := service.ReprocessTrackable(context.Background(), trackable.Id); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(ch, 1)
	if len(events) != 1 || events[0].Kind != EventTrackableMotion {
		t.Fatalf("expected changed motion: %+v", events)
	}
	motion, err := service.GetTrackableMotion(context.Background(), trackable.Id)
	if err != nil || motion.Name == nil || *motion.Name != "renamed asset" {
		t.Fatalf("metadata change missing from motion: %+v %v", motion, err)
	}
	after, _ := json.Marshal(service.state.ListLatestLocations())
	if string(before) != string(after) {
		t.Fatal("forced reprocessing modified provider observations")
	}
}
