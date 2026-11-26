package stream

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	streampb "github.com/formation-res/open-location-hub/internal/stream/pb"
	"github.com/formation-res/open-location-hub/internal/transform"
)

const (
	earthRadiusMeters       = 6_371_008.8
	maxEstimateGap          = 60 * time.Second
	defaultPositionAccuracy = 5.0
	minimumPositionAccuracy = 0.1
	accelerationVariance    = 16.0
)

type motionEstimator struct {
	projection *transform.CRSTransformer
}

func (e motionEstimator) estimate(input *streampb.AssetInput, previous *streampb.EstimateCheckpoint) (*streampb.MotionEstimate, *streampb.EstimateCheckpoint, string, error) {
	observation := input.GetObservation()
	var raw []byte
	switch observation.GetKind() {
	case Location:
		raw = observation.GetPayloadJson()
	case Proximity:
		raw = observation.GetResolvedProximityLocationJson()
	default:
		return nil, nil, "", fmt.Errorf("unknown observation kind")
	}
	var loc gen.Location
	if err := json.Unmarshal(raw, &loc); err != nil {
		return nil, nil, "", fmt.Errorf("decode estimation location: %w", err)
	}
	at := observation.GetObservedAtUnixNano()
	if observation.ObservedAtUnixNano == nil {
		at = observation.GetReceivedAtUnixNano()
	}
	if at == 0 && observation.ObservedAtUnixNano == nil {
		return nil, nil, "", fmt.Errorf("no source or ingest time on input")
	}
	if previous != nil && at <= previous.GetObservedAtUnixNano() {
		return nil, nil, "out_of_order", nil
	}

	crs := "local"
	if loc.Crs != nil && strings.TrimSpace(*loc.Crs) != "" {
		crs = strings.TrimSpace(*loc.Crs)
	}
	geographic := crs != "local"
	if geographic && crs != "EPSG:4326" {
		projected, err := e.projection.ToWGS84(crs, loc.Position)
		if err != nil {
			return nil, nil, "", fmt.Errorf("project estimate from %s: %w", crs, err)
		}
		loc.Position = projected
	}
	coordinates, err := estimateCoordinates(loc.Position)
	if err != nil {
		return nil, nil, "", err
	}
	frame := appendKeyPart(nil, loc.ProviderId)
	frame = appendKeyPart(frame, loc.Source)
	frame = appendKeyPart(frame, crs)
	if loc.ElevationRef != nil {
		frame = appendKeyPart(frame, string(*loc.ElevationRef))
	} else {
		frame = appendKeyPart(frame, "")
	}
	if loc.Floor != nil {
		frame = fmt.Appendf(frame, ":%g", *loc.Floor)
	}
	measurement := coordinates[:2]
	if geographic {
		lon, lat := coordinates[0], coordinates[1]
		if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			return nil, nil, "", fmt.Errorf("geographic position is outside WGS84 bounds")
		}
		sinLon, cosLon := math.Sincos(lon * math.Pi / 180)
		sinLat, cosLat := math.Sincos(lat * math.Pi / 180)
		measurement = []float64{earthRadiusMeters * cosLat * cosLon, earthRadiusMeters * cosLat * sinLon, earthRadiusMeters * sinLat}
	}
	var dt time.Duration
	if previous != nil {
		dt = time.Unix(0, at).Sub(time.Unix(0, previous.GetObservedAtUnixNano()))
	}
	reset := previous == nil || !bytes.Equal(frame, previous.GetFrame()) || dt > maxEstimateGap
	accuracy := defaultPositionAccuracy
	if loc.Accuracy != nil && *loc.Accuracy > 0 {
		accuracy = max(*loc.Accuracy, minimumPositionAccuracy)
	}
	variance := accuracy * accuracy
	checkpoint := &streampb.EstimateCheckpoint{
		SchemaVersion:      1,
		Sequence:           previous.GetSequence() + 1,
		ObservedAtUnixNano: at,
		Frame:              frame,
		Axes:               make([]*streampb.EstimateCheckpoint_Axis, len(measurement)),
	}
	for i, position := range measurement {
		if reset {
			checkpoint.Axes[i] = &streampb.EstimateCheckpoint_Axis{
				Position: position, PositionVariance: variance, VelocityVariance: 625,
			}
			continue
		}
		if len(previous.GetAxes()) != len(measurement) {
			return nil, nil, "", fmt.Errorf("checkpoint filter dimensions do not match frame")
		}
		checkpoint.Axes[i] = filterAxis(previous.Axes[i], position, dt.Seconds(), variance)
	}
	east, north := checkpoint.Axes[0].GetVelocity(), checkpoint.Axes[1].GetVelocity()
	uncertainty := math.Sqrt((checkpoint.Axes[0].GetPositionVariance() + checkpoint.Axes[1].GetPositionVariance()) / 2)
	if geographic {
		x, y, z := checkpoint.Axes[0].GetPosition(), checkpoint.Axes[1].GetPosition(), checkpoint.Axes[2].GetPosition()
		lon, lat := math.Atan2(y, x), math.Atan2(z, math.Hypot(x, y))
		sinLon, cosLon := math.Sincos(lon)
		sinLat, cosLat := math.Sincos(lat)
		east, north = -east*sinLon+north*cosLon, -east*sinLat*cosLon-north*sinLat*sinLon+checkpoint.Axes[2].GetVelocity()*cosLat
		varianceX, varianceY, varianceZ := checkpoint.Axes[0].PositionVariance, checkpoint.Axes[1].PositionVariance, checkpoint.Axes[2].PositionVariance
		varianceEast := sinLon*sinLon*varianceX + cosLon*cosLon*varianceY
		varianceNorth := sinLat*sinLat*(cosLon*cosLon*varianceX+sinLon*sinLon*varianceY) + cosLat*cosLat*varianceZ
		uncertainty = math.Sqrt((varianceEast + varianceNorth) / 2)
		coordinates[0], coordinates[1] = lon*180/math.Pi, lat*180/math.Pi
		crs = "EPSG:4326"
	} else {
		coordinates[0], coordinates[1] = checkpoint.Axes[0].GetPosition(), checkpoint.Axes[1].GetPosition()
	}
	encoded, err := json.Marshal(coordinates)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encode estimated position: %w", err)
	}
	if err := loc.Position.Coordinates.UnmarshalJSON(encoded); err != nil {
		return nil, nil, "", fmt.Errorf("assign estimated position: %w", err)
	}
	loc.Crs = &crs
	loc.Accuracy = &uncertainty
	loc.Trackables = &gen.StringIdList{input.TrackableId}
	associated := true
	loc.Associated = &associated
	t := time.Unix(0, at).UTC()
	loc.TimestampGenerated = &t
	if !reset {
		speed := math.Hypot(east, north)
		loc.Speed = &speed
		loc.Course = nil
		if speed >= 0.01 {
			course := math.Mod(math.Atan2(east, north)*180/math.Pi+360, 360)
			loc.Course = &course
		}
	}
	location, err := json.Marshal(loc)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encode estimated location: %w", err)
	}
	checkpoint.Position = coordinates
	identity := append([]byte("motion-estimate:v1:"), input.GetEventId()...)
	id := sha256.Sum256(binary.AppendUvarint(identity, checkpoint.Sequence))
	result := &streampb.MotionEstimate{
		SchemaVersion: 1, EventId: id[:], InputEventId: input.EventId, OriginHubId: observation.OriginHubId,
		TrackableId: input.TrackableId, Sequence: checkpoint.Sequence, LocationJson: location,
		HorizontalAccuracyMeters: uncertainty, FilterReset: reset,
	}
	if !reset {
		result.PreviousPosition = previous.GetPosition()
		result.HorizontalVelocityMetersPerSecond = []float64{east, north}
	}
	return result, checkpoint, "estimated", nil
}

func estimateCoordinates(point gen.Point) ([]float64, error) {
	if point.Type != gen.PointTypePoint {
		return nil, fmt.Errorf("position must be a GeoJSON Point")
	}
	encoded, err := point.Coordinates.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("read point coordinates: %w", err)
	}
	var coords []float64
	if err := json.Unmarshal(encoded, &coords); err != nil {
		return nil, fmt.Errorf("decode point coordinates: %w", err)
	}
	if len(coords) < 2 || len(coords) > 3 {
		return nil, fmt.Errorf("position requires two or three coordinates")
	}
	return coords, nil
}

func filterAxis(previous *streampb.EstimateCheckpoint_Axis, sample, dt, variance float64) *streampb.EstimateCheckpoint_Axis {
	// Constant velocity with continuous white acceleration. The scalar update
	// observes position, with the position/velocity covariance retained.
	p := previous.PositionVariance + 2*dt*previous.Covariance + dt*dt*previous.VelocityVariance + accelerationVariance*dt*dt*dt/3
	c := previous.Covariance + dt*previous.VelocityVariance + accelerationVariance*dt*dt/2
	v := previous.VelocityVariance + accelerationVariance*dt
	position := previous.Position + dt*previous.Velocity
	residual := sample - position
	return &streampb.EstimateCheckpoint_Axis{
		Position:         position + p/(p+variance)*residual,
		Velocity:         previous.Velocity + c/(p+variance)*residual,
		PositionVariance: p * variance / (p + variance),
		Covariance:       c * variance / (p + variance),
		VelocityVariance: max(0, v-c*c/(p+variance)),
	}
}
