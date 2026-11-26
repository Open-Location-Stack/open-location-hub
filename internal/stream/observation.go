package stream

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"google.golang.org/protobuf/proto"
)

// Observation is a validated location or proximity received by the Hub.
type Observation struct {
	Kind                      streampb.Observation_Kind
	ProviderID                string
	Source                    string
	ObservedAt                *time.Time
	Payload                   any
	ResolvedTrackableIDs      []string
	ResolvedProximityLocation any
}

const (
	// Location represents an OMLOX location observation.
	Location = streampb.Observation_KIND_LOCATION
	// Proximity represents an OMLOX proximity observation.
	Proximity = streampb.Observation_KIND_PROXIMITY
)

func (o Observation) encode(hubID, transport string, receivedAt time.Time) (key, value []byte, err error) {
	switch o.Kind {
	case streampb.Observation_KIND_LOCATION, streampb.Observation_KIND_PROXIMITY:
	default:
		return nil, nil, fmt.Errorf("invalid observation kind %s", o.Kind)
	}
	if hubID == "" || o.ProviderID == "" || o.Source == "" {
		return nil, nil, fmt.Errorf("observation requires origin hub, provider and source")
	}

	payload, err := json.Marshal(o.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode observation payload: %w", err)
	}
	key = appendKeyPart(key, hubID)
	key = appendKeyPart(key, o.ProviderID)
	key = appendKeyPart(key, o.Source)
	identity := sha256.New()
	_, _ = identity.Write(key)
	_, _ = identity.Write([]byte{byte(o.Kind)})
	_, _ = identity.Write(payload)

	record := &streampb.Observation{
		SchemaVersion:        1,
		EventId:              identity.Sum(nil),
		OriginHubId:          hubID,
		ProviderId:           o.ProviderID,
		Source:               o.Source,
		Kind:                 o.Kind,
		ReceivedAtUnixNano:   receivedAt.UnixNano(),
		Transport:            transport,
		PayloadJson:          payload,
		ResolvedTrackableIds: o.ResolvedTrackableIDs,
	}
	if o.ResolvedProximityLocation != nil {
		resolved, err := json.Marshal(o.ResolvedProximityLocation)
		if err != nil {
			return nil, nil, fmt.Errorf("encode resolved proximity location: %w", err)
		}
		record.ResolvedProximityLocationJson = resolved
	}
	if o.ObservedAt != nil {
		record.ObservedAtUnixNano = proto.Int64(o.ObservedAt.UnixNano())
	}
	value, err = proto.Marshal(record)
	return key, value, err
}

func appendKeyPart(key []byte, value string) []byte {
	key = binary.AppendUvarint(key, uint64(len(value)))
	return append(key, value...)
}
