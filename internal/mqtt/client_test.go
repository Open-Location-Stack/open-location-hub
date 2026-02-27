package mqtt

import "testing"

func TestTopicMapping(t *testing.T) {
	if got := TopicLocationPub("abc"); got != "/omlox/json/location_updates/pub/abc" {
		t.Fatalf("unexpected topic: %s", got)
	}
	if got := TopicProximity("src", "abc"); got != "/omlox/json/proximity_updates/src/abc" {
		t.Fatalf("unexpected topic: %s", got)
	}
}
