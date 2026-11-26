package stream

import (
	"context"
	"testing"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/locating"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestConfigurationWatchActivatesOnlyACompleteNextRevision(t *testing.T) {
	t.Parallel()
	watch := ConfigurationWatch{hubID: "hub", snapshotCutoff: 2}
	initial := newLocatingConfiguration(2)
	watch.cache.Store(initial)
	rulesShard, providerShard := configurationShard("target"), configurationShard("gps")
	ctx := context.Background()
	bundle := &streampb.ConfigurationRevision{
		SchemaVersion: 1, OriginHubId: "hub", Revision: 3, RecordedAtUnixNano: time.Now().UnixNano(),
		Changes: []*streampb.ConfigurationRevision_Change{
			{ResourceKind: "trackable", Operation: "INSERT", CurrentJson: []byte(`{"id":"target","payload":{"locating_rules":[{"expression":"TRUE","priority":3}]}}`)},
			{ResourceKind: "location_provider", Operation: "INSERT", CurrentJson: []byte(`{"id":"gps","type":"gps","name":"GPS test"}`)},
		},
	}
	records, err := configRecords("config", bundle, 32)
	if err != nil || len(records) < 2 {
		t.Fatalf("fragment configuration: %v", err)
	}
	for _, record := range records[:len(records)-1] {
		if err := watch.applyRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		if watch.cache.Load().revision != 2 || len(watch.cache.Load().shards[rulesShard].rules) != 0 || len(watch.cache.Load().shards[providerShard].providers) != 0 {
			t.Fatal("partial configuration revision was visible")
		}
	}
	if err := watch.applyRecord(ctx, records[len(records)-1]); err != nil {
		t.Fatal(err)
	}
	if watch.cache.Load().revision != 3 || len(watch.cache.Load().shards[rulesShard].rules["target"]) != 1 || *watch.cache.Load().shards[providerShard].providers["gps"].Name != "GPS test" {
		t.Fatal("whole revision was not activated")
	}
	if len(initial.shards[rulesShard].rules) != 0 || len(initial.shards[providerShard].providers) != 0 {
		t.Fatal("activation modified a previous snapshot")
	}
	for _, record := range records {
		if err := watch.applyRecord(ctx, record); err != nil {
			t.Fatalf("identical retry: %v", err)
		}
	}
	bundle.Changes[0].CurrentJson = []byte(`{"id":"target","payload":{}}`)
	conflict, err := configRecords("config", bundle, configPartSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.applyRecord(ctx, conflict[0]); err == nil || len(watch.cache.Load().shards[rulesShard].rules["target"]) != 1 {
		t.Fatal("a conflicting duplicate replaced active rules")
	}
	bundle.Revision = 5
	gap, err := configRecords("config", bundle, configPartSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.applyRecord(ctx, gap[0]); err == nil || watch.cache.Load().revision != 3 {
		t.Fatal("revision gap was accepted")
	}
	bundle.Revision = 4
	bundle.Changes[1].CurrentJson = []byte(`{}`)
	invalid, err := configRecords("config", bundle, configPartSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.applyRecord(ctx, invalid[0]); err == nil || watch.cache.Load().revision != 3 || len(watch.cache.Load().shards[rulesShard].rules["target"]) != 1 {
		t.Fatal("malformed metadata partially replaced the active snapshot")
	}
}

func TestConfiguredEstimationUsesRetainedCandidateAfterRulesChange(t *testing.T) {
	t.Parallel()
	makeRules := func(provider string) locating.Rules {
		t.Helper()
		rules, err := locating.Compile(&[]gen.LocatingRule{{Expression: "provider_id = '" + provider + "'", Priority: 5}})
		if err != nil {
			t.Fatal(err)
		}
		return rules
	}
	snapshot := newLocatingConfiguration(10)
	snapshot.shards[configurationShard("asset")].rules["asset"] = makeRules("uwb")
	worker := &EstimateWorker{
		configuration: &ConfigurationWatch{hubID: "hub"},
		retention:     candidateRetention{location: time.Minute, proximity: time.Minute},
	}
	start := time.Now()
	uwb := testEstimateInput(t, start, "uwb", 8)
	first, checkpoint, outcome, err := worker.estimateConfigured(uwb, nil, snapshot)
	if err != nil || outcome != "estimated" || first.GetConfigurationRevision() != 10 {
		t.Fatalf("first estimate = %v, outcome = %s, err = %v", first, outcome, err)
	}
	gps := testEstimateInput(t, start.Add(-time.Second), "gps", 8.001)
	gps.Observation.ReceivedAtUnixNano = start.Add(time.Second).UnixNano()
	second, stored, outcome, err := worker.estimateConfigured(gps, checkpoint, snapshot)
	if err != nil || second != nil || len(stored.candidates) != 2 || outcome != "no_selection_change" {
		t.Fatalf("retaining lower priority input = %v, outcome = %s, err = %v", second, outcome, err)
	}

	raw, err := proto.Marshal(stored.checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var restored streampb.EstimateCheckpoint
	if err := proto.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	state, err := restoreCandidates(&restored)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = snapshot.withChanges(11, []locatingChange{{kind: "trackable", id: "asset", rules: makeRules("gps")}})
	trigger := testEstimateInput(t, start.Add(2*time.Second), "uwb", 8.002)
	result, next, outcome, err := worker.estimateConfigured(trigger, state, snapshot)
	if err != nil || outcome != "estimated" || result.GetConfigurationRevision() != 11 || result.GetSequence() != 2 ||
		!result.GetFilterReset() || !proto.Equal(gps, findCandidate(next.checkpoint, gps)) ||
		string(result.GetInputEventId()) != string(gps.EventId) || string(result.GetTriggerAssetInputId()) != string(trigger.EventId) {
		t.Fatalf("rule-driven correction = %v, outcome = %s, err = %v", result, outcome, err)
	}
}

func findCandidate(checkpoint *streampb.EstimateCheckpoint, input *streampb.AssetInput) *streampb.AssetInput {
	for _, candidate := range checkpoint.GetCandidates() {
		if string(candidate.EventId) == string(input.EventId) {
			return candidate
		}
	}
	return nil
}
