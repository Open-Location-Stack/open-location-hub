package stream

import (
	"bytes"
	"crypto/sha256"
	"testing"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

func TestConfigurationRevisionFragmentsReassembleIntoOneTransaction(t *testing.T) {
	t.Parallel()
	change := &streampb.ConfigurationRevision_Change{
		ResourceKind: "fence", Operation: "UPDATE", PreviousJson: []byte(`{"size":1}`), CurrentJson: []byte(`{"size":2}`),
	}
	bundle := &streampb.ConfigurationRevision{SchemaVersion: 1, OriginHubId: "hub", Revision: 101, Changes: []*streampb.ConfigurationRevision_Change{change}}
	records, err := configRecords("config", bundle, 16)
	if err != nil || len(records) < 2 {
		t.Fatalf("expected fragmented revision: %v, %v", records, err)
	}
	var payload []byte
	var digest []byte
	for index, record := range records {
		part := new(streampb.ConfigurationRevisionPart)
		if err := proto.Unmarshal(record.Value, part); err != nil {
			t.Fatal(err)
		}
		if string(record.Key) != bundle.OriginHubId || part.Revision != bundle.Revision || int(part.PartIndex) != index ||
			int(part.PartCount) != len(records) || len(part.Payload) > 16 {
			t.Fatalf("unexpected fragment: %v", part)
		}
		if index == 0 {
			digest = part.BundleSha256
		} else if !bytes.Equal(digest, part.BundleSha256) {
			t.Fatal("fragments disagree about bundle digest")
		}
		payload = append(payload, part.Payload...)
	}
	wantDigest := sha256.Sum256(payload)
	got := new(streampb.ConfigurationRevision)
	if err := proto.Unmarshal(payload, got); err != nil || !proto.Equal(got, bundle) || !bytes.Equal(digest, wantDigest[:]) {
		t.Fatalf("incorrect assembled revision: %v, %v", got, err)
	}
}
