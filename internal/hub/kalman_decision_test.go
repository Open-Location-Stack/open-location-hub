package hub

import (
	"context"
	"testing"
	"time"
)

func TestKalmanDecisionStagePassthroughWithoutTrackables(t *testing.T) {
	t.Parallel()

	stage := newKalmanDecisionStage(NewProcessingState(time.Now), Config{
		LocationTTL:             time.Minute,
		KalmanLocationMaxPoints: 8,
		KalmanLocationMaxAge:    10 * time.Second,
	}, time.Now)
	location := testLocation(t, nil)

	results, err := stage.Process(context.Background(), location)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if !results[0].Emit {
		t.Fatal("expected passthrough result to emit")
	}
	if results[0].Location.Source != location.Source {
		t.Fatalf("unexpected source: %s", results[0].Location.Source)
	}
}

func TestKalmanDecisionStageRetainsIndependentTrackStatePerTrackable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	stage := newKalmanDecisionStage(NewProcessingState(func() time.Time { return now }), Config{
		LocationTTL:             time.Minute,
		KalmanLocationMaxPoints: 8,
		KalmanLocationMaxAge:    10 * time.Second,
	}, func() time.Time { return now })
	crs := "local"
	location := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{10, 20})
	trackables := []string{"trackable-a", "trackable-b"}
	location.Trackables = &trackables

	results, err := stage.Process(context.Background(), location)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two track-scoped results, got %d", len(results))
	}
	for _, result := range results {
		if result.Location.Trackables == nil || len(*result.Location.Trackables) != 1 {
			t.Fatalf("expected single-trackable result, got %+v", result.Location.Trackables)
		}
	}
}

func TestKalmanDecisionStageDerivesCourseAndSpeed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	stage := newKalmanDecisionStage(NewProcessingState(func() time.Time { return now }), Config{
		LocationTTL:             time.Minute,
		KalmanLocationMaxPoints: 8,
		KalmanLocationMaxAge:    10 * time.Second,
	}, func() time.Time { return now })
	crs := "local"
	trackables := []string{"trackable-a"}

	first := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{0, 0})
	first.Trackables = &trackables
	firstTime := now
	first.TimestampGenerated = &firstTime
	results, err := stage.Process(context.Background(), first)
	if err != nil {
		t.Fatalf("first process failed: %v", err)
	}
	if results[0].Location.Speed != nil || results[0].Location.Course != nil {
		t.Fatal("expected first decision sample to omit derived motion")
	}

	now = now.Add(time.Second)
	second := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{10, 0})
	second.Trackables = &trackables
	second.TimestampGenerated = &now
	results, err = stage.Process(context.Background(), second)
	if err != nil {
		t.Fatalf("second process failed: %v", err)
	}
	if results[0].Location.Speed == nil || *results[0].Location.Speed <= 0 {
		t.Fatalf("expected positive derived speed, got %+v", results[0].Location.Speed)
	}
	if results[0].Location.Course == nil {
		t.Fatal("expected derived course")
	}
	if results[0].Location.Properties == nil || (*results[0].Location.Properties)[kalmanNormalizedProperty] != true {
		t.Fatal("expected kalman marker property")
	}
}

func TestKalmanDecisionStageDerivesVerticalSpeedIntoProperties(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	stage := newKalmanDecisionStage(NewProcessingState(func() time.Time { return now }), Config{
		LocationTTL:             time.Minute,
		KalmanLocationMaxPoints: 8,
		KalmanLocationMaxAge:    10 * time.Second,
	}, func() time.Time { return now })
	crs := "local"
	trackables := []string{"trackable-a"}

	first := testLocationWithCoordinates3D(t, &crs, "zone-a", [3]float32{0, 0, 1})
	first.Trackables = &trackables
	first.TimestampGenerated = &now
	if _, err := stage.Process(context.Background(), first); err != nil {
		t.Fatalf("first process failed: %v", err)
	}

	now = now.Add(2 * time.Second)
	second := testLocationWithCoordinates3D(t, &crs, "zone-a", [3]float32{0, 0, 5})
	second.Trackables = &trackables
	second.TimestampGenerated = &now
	results, err := stage.Process(context.Background(), second)
	if err != nil {
		t.Fatalf("second process failed: %v", err)
	}
	props := results[0].Location.Properties
	if props == nil {
		t.Fatal("expected kalman properties")
	}
	if _, ok := (*props)[kalmanVerticalSpeedProperty]; !ok {
		t.Fatal("expected vertical speed extension property")
	}
}

func TestKalmanDecisionStageResetsWhenSamplesGoStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	stage := newKalmanDecisionStage(NewProcessingState(func() time.Time { return now }), Config{
		LocationTTL:             time.Minute,
		KalmanLocationMaxPoints: 3,
		KalmanLocationMaxAge:    time.Second,
	}, func() time.Time { return now })
	crs := "local"
	trackables := []string{"trackable-a"}

	first := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{0, 0})
	first.Trackables = &trackables
	first.TimestampGenerated = &now
	if _, err := stage.Process(context.Background(), first); err != nil {
		t.Fatalf("first process failed: %v", err)
	}

	now = now.Add(3 * time.Second)
	second := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{10, 0})
	second.Trackables = &trackables
	second.TimestampGenerated = &now
	results, err := stage.Process(context.Background(), second)
	if err != nil {
		t.Fatalf("second process failed: %v", err)
	}
	if results[0].Location.Speed != nil || results[0].Location.Course != nil {
		t.Fatal("expected stale reset to clear derived motion until a new baseline exists")
	}
}

func TestKalmanDecisionStageAppliesEmitFrequencyOnlyToPublication(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	stage := newKalmanDecisionStage(NewProcessingState(func() time.Time { return now }), Config{
		LocationTTL:              time.Minute,
		KalmanLocationMaxPoints:  8,
		KalmanLocationMaxAge:     10 * time.Second,
		KalmanEmitMaxFrequencyHz: 1,
	}, func() time.Time { return now })
	crs := "local"
	trackables := []string{"trackable-a"}

	first := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{0, 0})
	first.Trackables = &trackables
	first.TimestampGenerated = &now
	if _, err := stage.Process(context.Background(), first); err != nil {
		t.Fatalf("first process failed: %v", err)
	}

	now = now.Add(100 * time.Millisecond)
	second := testLocationWithCoordinates(t, &crs, "zone-a", [2]float32{1, 0})
	second.Trackables = &trackables
	second.TimestampGenerated = &now
	results, err := stage.Process(context.Background(), second)
	if err != nil {
		t.Fatalf("second process failed: %v", err)
	}
	if results[0].Emit {
		t.Fatal("expected second sample to be publication-throttled")
	}
	if results[0].Location.Properties == nil || (*results[0].Location.Properties)[kalmanNormalizedProperty] != true {
		t.Fatal("expected normalized state to continue updating despite throttling")
	}
}
