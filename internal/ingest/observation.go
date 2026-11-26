// Package ingest describes observations accepted by the Hub.
package ingest

import (
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"time"
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
