package hub

import (
	"context"
	"math"
	"time"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
)

const (
	kalmanDecisionTTLFactor      = 2
	kalmanDefaultAccuracyMeters  = 1.0
	kalmanMinMeasurementVariance = 1e-6
	kalmanBaseProcessNoiseMeters = 0.25
	kalmanVerticalSpeedProperty  = "kalman_vertical_speed"
	kalmanNormalizedProperty     = "kalman_normalized"
)

type decisionLocationResult struct {
	Location gen.Location
	Emit     bool
}

type decisionLocationStage interface {
	Process(context.Context, gen.Location) ([]decisionLocationResult, error)
}

type passthroughDecisionStage struct{}

func (passthroughDecisionStage) Process(_ context.Context, location gen.Location) ([]decisionLocationResult, error) {
	return []decisionLocationResult{{Location: location, Emit: true}}, nil
}

type kalmanDecisionStage struct {
	state                  *ProcessingState
	now                    func() time.Time
	locationTTL            time.Duration
	maxPoints              int
	maxAge                 time.Duration
	emitMinInterval        time.Duration
	baseProcessNoiseMeters float64
}

type kalmanTrackState struct {
	CRS           string              `json:"crs"`
	Samples       []kalmanObservation `json:"samples"`
	HorizontalX   axisKalmanState     `json:"horizontal_x"`
	HorizontalY   axisKalmanState     `json:"horizontal_y"`
	Vertical      axisKalmanState     `json:"vertical"`
	HasVertical   bool                `json:"has_vertical"`
	LastDecision  *kalmanDecision     `json:"last_decision,omitempty"`
	LastEmittedAt time.Time           `json:"last_emitted_at,omitempty"`
}

type kalmanObservation struct {
	At       time.Time    `json:"at"`
	Location gen.Location `json:"location"`
}

type axisKalmanState struct {
	Estimate    float64 `json:"estimate"`
	Covariance  float64 `json:"covariance"`
	Initialized bool    `json:"initialized"`
}

type kalmanDecision struct {
	At        time.Time `json:"at"`
	Location  gen.Location
	VerticalZ *float64 `json:"vertical_z,omitempty"`
}

func newKalmanDecisionStage(state *ProcessingState, cfg Config, now func() time.Time) decisionLocationStage {
	if now == nil {
		now = time.Now
	}
	interval := time.Duration(0)
	if cfg.KalmanEmitMaxFrequencyHz > 0 {
		interval = time.Duration(float64(time.Second) / cfg.KalmanEmitMaxFrequencyHz)
	}
	return &kalmanDecisionStage{
		state:                  state,
		now:                    now,
		locationTTL:            cfg.LocationTTL,
		maxPoints:              cfg.KalmanLocationMaxPoints,
		maxAge:                 cfg.KalmanLocationMaxAge,
		emitMinInterval:        interval,
		baseProcessNoiseMeters: kalmanBaseProcessNoiseMeters,
	}
}

func (s *kalmanDecisionStage) Process(_ context.Context, location gen.Location) ([]decisionLocationResult, error) {
	if s == nil || s.state == nil || location.Trackables == nil || len(*location.Trackables) == 0 {
		return []decisionLocationResult{{Location: location, Emit: true}}, nil
	}
	results := make([]decisionLocationResult, 0, len(*location.Trackables))
	now := s.now().UTC()
	for _, trackableID := range *location.Trackables {
		normalized, emit, err := s.normalizeTrackable(location, trackableID, now)
		if err != nil {
			return nil, err
		}
		results = append(results, decisionLocationResult{Location: normalized, Emit: emit})
	}
	return results, nil
}

func (s *kalmanDecisionStage) normalizeTrackable(location gen.Location, trackableID string, now time.Time) (gen.Location, bool, error) {
	out, err := cloneLocation(location)
	if err != nil {
		return gen.Location{}, false, err
	}
	out.Trackables = &gen.StringIdList{trackableID}
	out.Course = nil
	out.Speed = nil

	decisionAt := locationTime(location)
	if decisionAt.IsZero() {
		decisionAt = now
	}

	state, ok := s.state.GetKalmanTrackState(trackableID)
	if !ok {
		state = kalmanTrackState{}
	}
	if state.CRS != "" && state.CRS != locationCRS(location) {
		state = kalmanTrackState{}
	}
	state.CRS = locationCRS(location)
	if state.LastDecision != nil && !state.LastDecision.At.IsZero() && decisionAt.Sub(state.LastDecision.At) > s.maxAge {
		state = kalmanTrackState{CRS: state.CRS}
	}
	state.Samples = trimKalmanSamples(state.Samples, decisionAt, s.maxAge, s.maxPoints)
	if len(state.Samples) > 0 && decisionAt.Sub(state.Samples[len(state.Samples)-1].At) > s.maxAge {
		state = kalmanTrackState{CRS: state.CRS}
	}

	point, z, hasZ, err := point3D(location.Position)
	if err != nil {
		return gen.Location{}, false, err
	}
	measurementVarX, measurementVarY := kalmanMeasurementVariance(location, point)
	state.HorizontalX = updateAxisKalman(state.HorizontalX, point[0], measurementVarX, kalmanProcessVariance(location, point, decisionAt, state, s.baseProcessNoiseMeters, true))
	state.HorizontalY = updateAxisKalman(state.HorizontalY, point[1], measurementVarY, kalmanProcessVariance(location, point, decisionAt, state, s.baseProcessNoiseMeters, false))
	if hasZ {
		measurementVarZ := math.Max(kalmanMeasurementVarianceMeters(location), kalmanMinMeasurementVariance)
		state.Vertical = updateAxisKalman(state.Vertical, z, measurementVarZ, measurementVarZ)
		state.HasVertical = true
	} else {
		state.Vertical = axisKalmanState{}
		state.HasVertical = false
	}

	normalizedPosition, normalizedZ, err := kalmanPosition(location.Position, state.HorizontalX.Estimate, state.HorizontalY.Estimate, state.Vertical, state.HasVertical)
	if err != nil {
		return gen.Location{}, false, err
	}
	out.Position = normalizedPosition

	if state.LastDecision != nil && !state.LastDecision.At.IsZero() && decisionAt.After(state.LastDecision.At) {
		if course, speed, ok := derivedHorizontalMotion(*state.LastDecision, out, decisionAt); ok {
			out.Course = float32Value(float32(course))
			out.Speed = float32Value(float32(speed))
		}
		if verticalSpeed, ok := derivedVerticalSpeed(*state.LastDecision, normalizedZ, decisionAt); ok {
			out.Properties = mergeKalmanProperties(out.Properties, verticalSpeed)
		} else {
			out.Properties = mergeKalmanProperties(out.Properties, nil)
		}
	} else {
		out.Properties = mergeKalmanProperties(out.Properties, nil)
	}

	state.Samples = append(state.Samples, kalmanObservation{At: decisionAt, Location: out})
	state.Samples = trimKalmanSamples(state.Samples, decisionAt, s.maxAge, s.maxPoints)
	state.LastDecision = &kalmanDecision{At: decisionAt, Location: out, VerticalZ: normalizedZ}

	emit := true
	if s.emitMinInterval > 0 && !state.LastEmittedAt.IsZero() && decisionAt.Sub(state.LastEmittedAt) < s.emitMinInterval {
		emit = false
	}
	if emit {
		state.LastEmittedAt = decisionAt
	}

	ttl := s.locationTTL
	if ttl <= 0 {
		ttl = s.maxAge * kalmanDecisionTTLFactor
	}
	s.state.SetKalmanTrackState(trackableID, state, ttl)
	return out, emit, nil
}

func trimKalmanSamples(samples []kalmanObservation, now time.Time, maxAge time.Duration, maxPoints int) []kalmanObservation {
	if len(samples) == 0 {
		return nil
	}
	start := 0
	for start < len(samples) && now.Sub(samples[start].At) > maxAge {
		start++
	}
	if start > 0 {
		samples = append([]kalmanObservation(nil), samples[start:]...)
	}
	if len(samples) > maxPoints-1 {
		samples = append([]kalmanObservation(nil), samples[len(samples)-(maxPoints-1):]...)
	}
	return samples
}

func updateAxisKalman(state axisKalmanState, measurement, measurementVariance, processVariance float64) axisKalmanState {
	if !state.Initialized {
		return axisKalmanState{
			Estimate:    measurement,
			Covariance:  math.Max(measurementVariance, kalmanMinMeasurementVariance),
			Initialized: true,
		}
	}
	state.Covariance += math.Max(processVariance, kalmanMinMeasurementVariance)
	denom := state.Covariance + math.Max(measurementVariance, kalmanMinMeasurementVariance)
	if denom <= 0 {
		return state
	}
	gain := state.Covariance / denom
	state.Estimate += gain * (measurement - state.Estimate)
	state.Covariance = math.Max((1-gain)*state.Covariance, kalmanMinMeasurementVariance)
	return state
}

func kalmanMeasurementVariance(location gen.Location, point [2]float64) (float64, float64) {
	metersVariance := kalmanMeasurementVarianceMeters(location)
	if locationCRS(location) != "EPSG:4326" {
		return metersVariance, metersVariance
	}
	latRadians := degreesToRadians(point[1])
	latVariance := math.Pow(math.Sqrt(metersVariance)/metersPerLatitudeDegree, 2)
	lonVariance := math.Pow(math.Sqrt(metersVariance)/metersPerLongitudeDegreeAtLatitude(latRadians), 2)
	return math.Max(lonVariance, kalmanMinMeasurementVariance), math.Max(latVariance, kalmanMinMeasurementVariance)
}

func kalmanMeasurementVarianceMeters(location gen.Location) float64 {
	accuracy := kalmanDefaultAccuracyMeters
	if location.Accuracy != nil && *location.Accuracy > 0 {
		accuracy = float64(*location.Accuracy)
	}
	return math.Max(accuracy*accuracy, kalmanMinMeasurementVariance)
}

func kalmanProcessVariance(location gen.Location, point [2]float64, at time.Time, state kalmanTrackState, baseNoiseMeters float64, horizontalX bool) float64 {
	if state.LastDecision == nil || state.LastDecision.At.IsZero() || !at.After(state.LastDecision.At) {
		if locationCRS(location) == "EPSG:4326" {
			latRadians := degreesToRadians(point[1])
			if horizontalX {
				return math.Pow(baseNoiseMeters/metersPerLongitudeDegreeAtLatitude(latRadians), 2)
			}
			return math.Pow(baseNoiseMeters/metersPerLatitudeDegree, 2)
		}
		return baseNoiseMeters * baseNoiseMeters
	}
	dt := at.Sub(state.LastDecision.At).Seconds()
	if dt <= 0 {
		dt = 1
	}
	if locationCRS(location) == "EPSG:4326" {
		latRadians := degreesToRadians(point[1])
		if horizontalX {
			return math.Pow((baseNoiseMeters*dt)/metersPerLongitudeDegreeAtLatitude(latRadians), 2)
		}
		return math.Pow((baseNoiseMeters*dt)/metersPerLatitudeDegree, 2)
	}
	return math.Pow(baseNoiseMeters*dt, 2)
}

func kalmanPosition(base gen.Point, x, y float64, vertical axisKalmanState, hasVertical bool) (gen.Point, *float64, error) {
	point := gen.Point{Type: base.Type}
	if hasVertical && vertical.Initialized {
		z := vertical.Estimate
		if err := point.Coordinates.FromGeoJsonPosition3D([]float32{float32(x), float32(y), float32(z)}); err != nil {
			return gen.Point{}, nil, err
		}
		return point, &z, nil
	}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{float32(x), float32(y)}); err != nil {
		return gen.Point{}, nil, err
	}
	return point, nil, nil
}

func point3D(point gen.Point) ([2]float64, float64, bool, error) {
	coords3d, err := point.Coordinates.AsGeoJsonPosition3D()
	if err == nil && len(coords3d) >= 3 {
		return [2]float64{float64(coords3d[0]), float64(coords3d[1])}, float64(coords3d[2]), true, nil
	}
	coords2d, err := point.Coordinates.AsGeoJsonPosition2D()
	if err == nil && len(coords2d) >= 2 {
		return [2]float64{float64(coords2d[0]), float64(coords2d[1])}, 0, false, nil
	}
	return [2]float64{}, 0, false, err
}

func derivedHorizontalMotion(previous kalmanDecision, current gen.Location, at time.Time) (float64, float64, bool) {
	currentPoint, err := point2D(current.Position)
	if err != nil {
		return 0, 0, false
	}
	previousPoint, err := point2D(previous.Location.Position)
	if err != nil {
		return 0, 0, false
	}
	dt := at.Sub(previous.At).Seconds()
	if dt <= 0 {
		return 0, 0, false
	}
	distanceMeters := math.Sqrt(collisionDistanceSquaredMeters(previous.Location, current, previousPoint, currentPoint))
	speed := distanceMeters / dt
	course := planarCourse(previous.Location, previousPoint, currentPoint)
	return course, speed, true
}

func planarCourse(location gen.Location, from, to [2]float64) float64 {
	if locationCRS(location) == "EPSG:4326" {
		return normalizeHeading(wgs84Bearing(from, to))
	}
	dx := to[0] - from[0]
	dy := to[1] - from[1]
	return normalizeHeading(math.Atan2(dx, dy) * 180 / math.Pi)
}

func wgs84Bearing(from, to [2]float64) float64 {
	lon1 := degreesToRadians(from[0])
	lat1 := degreesToRadians(from[1])
	lon2 := degreesToRadians(to[0])
	lat2 := degreesToRadians(to[1])
	y := math.Sin(lon2-lon1) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(lon2-lon1)
	return math.Atan2(y, x) * 180 / math.Pi
}

func normalizeHeading(value float64) float64 {
	for value < 0 {
		value += 360
	}
	for value >= 360 {
		value -= 360
	}
	return value
}

func derivedVerticalSpeed(previous kalmanDecision, currentZ *float64, at time.Time) (*float64, bool) {
	if previous.VerticalZ == nil || currentZ == nil {
		return nil, false
	}
	dt := at.Sub(previous.At).Seconds()
	if dt <= 0 {
		return nil, false
	}
	value := (*currentZ - *previous.VerticalZ) / dt
	return &value, true
}

func mergeKalmanProperties(props *gen.ExtensionProperties, verticalSpeed *float64) *gen.ExtensionProperties {
	out := gen.ExtensionProperties{}
	if props != nil {
		for key, value := range *props {
			out[key] = value
		}
	}
	out[kalmanNormalizedProperty] = true
	if verticalSpeed != nil {
		out[kalmanVerticalSpeedProperty] = *verticalSpeed
	} else {
		delete(out, kalmanVerticalSpeedProperty)
	}
	return &out
}

func float32Value(value float32) *float32 {
	return &value
}
