package transform

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	"github.com/google/uuid"
)

const (
	localToleranceMeters = 0.05
	wgsToleranceDegrees  = 1e-5
)

func TestCRSTransformerRoundTripSpecialCases(t *testing.T) {
	transformer := NewCRSTransformer()
	cases := []struct {
		name string
		lat  float64
		lon  float64
	}{
		{name: "equator-prime-meridian", lat: 0.0001, lon: 0.0001},
		{name: "equator-west", lat: -0.0001, lon: -0.0001},
		{name: "southern-western", lat: -33.4489, lon: -70.6693},
		{name: "northern-eastern", lat: 47.3744, lon: 8.5411},
		{name: "near-antimeridian-east", lat: 10, lon: 179.9},
		{name: "near-antimeridian-west", lat: -10, lon: -179.9},
		{name: "high-latitude-north", lat: 83.9, lon: 20},
		{name: "high-latitude-south", lat: -79.9, lon: -45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crs := projectedCRSForLonLat(tc.lon, tc.lat)
			wgs := point2D(t, tc.lon, tc.lat)
			projected, err := transformer.FromWGS84(crs, wgs)
			if err != nil {
				t.Fatalf("to %s failed: %v", crs, err)
			}
			roundTrip, err := transformer.ToWGS84(crs, projected)
			if err != nil {
				t.Fatalf("to wgs84 failed: %v", err)
			}
			assertPointClose(t, wgs, roundTrip, wgsToleranceDegrees)
		})
	}
}

func TestCRSTransformerRandomGlobalRoundTrips(t *testing.T) {
	transformer := NewCRSTransformer()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 250; i++ {
		lat := rng.Float64()*166 - 83
		lon := rng.Float64()*360 - 180
		crs := projectedCRSForLonLat(lon, lat)
		wgs := point2D(t, lon, lat)
		projected, err := transformer.FromWGS84(crs, wgs)
		if err != nil {
			t.Fatalf("sample %d to %s failed: %v", i, crs, err)
		}
		roundTrip, err := transformer.ToWGS84(crs, projected)
		if err != nil {
			t.Fatalf("sample %d to wgs84 failed: %v", i, err)
		}
		assertPointClose(t, wgs, roundTrip, wgsToleranceDegrees)
	}
}

func TestLocalTransformerRoundTripTwoPointSimilarity(t *testing.T) {
	zone := syntheticZone(t, 47.3744, 8.5411, 32, 15, 1.0, 0)
	transformer, err := NewLocalTransformer(zone)
	if err != nil {
		t.Fatalf("new local transformer failed: %v", err)
	}
	local := point2D(t, 12.5, -3.75)
	wgs, err := transformer.LocalToWGS84(local)
	if err != nil {
		t.Fatalf("local to wgs84 failed: %v", err)
	}
	roundTrip, err := transformer.WGS84ToLocal(wgs)
	if err != nil {
		t.Fatalf("wgs84 to local failed: %v", err)
	}
	assertPointClose(t, local, roundTrip, localToleranceMeters)
}

func TestLocalTransformerRoundTripOverdeterminedWithNoise(t *testing.T) {
	zone := syntheticZoneWithNoise(t, -33.4489, -70.6693, 20, -35, 0.95, 0.15, 0.15)
	transformer, err := NewLocalTransformer(zone)
	if err != nil {
		t.Fatalf("new local transformer failed: %v", err)
	}
	local := point2D(t, 8.25, -6.5)
	wgs, err := transformer.LocalToWGS84(local)
	if err != nil {
		t.Fatalf("local to wgs84 failed: %v", err)
	}
	roundTrip, err := transformer.WGS84ToLocal(wgs)
	if err != nil {
		t.Fatalf("wgs84 to local failed: %v", err)
	}
	assertPointClose(t, local, roundTrip, 0.75)
}

func TestLocalTransformerRandomSyntheticZones(t *testing.T) {
	rng := rand.New(rand.NewSource(84))
	for i := 0; i < 100; i++ {
		lat := rng.Float64()*120 - 60
		lon := rng.Float64()*360 - 180
		tx := rng.Float64()*100 - 50
		ty := rng.Float64()*100 - 50
		scale := 0.5 + rng.Float64()*3
		rotation := rng.Float64()*math.Pi - math.Pi/2
		zone := syntheticZoneTransform(t, lat, lon, tx, ty, scale, rotation)
		transformer, err := NewLocalTransformer(zone)
		if err != nil {
			t.Fatalf("sample %d new local transformer failed: %v", i, err)
		}
		local := point2D(t, rng.Float64()*60-30, rng.Float64()*60-30)
		wgs, err := transformer.LocalToWGS84(local)
		if err != nil {
			t.Fatalf("sample %d local to wgs84 failed: %v", i, err)
		}
		roundTrip, err := transformer.WGS84ToLocal(wgs)
		if err != nil {
			t.Fatalf("sample %d wgs84 to local failed: %v", i, err)
		}
		assertPointClose(t, local, roundTrip, 0.2)
	}
}

func TestNewLocalTransformerRejectsInvalidGroundControlPoints(t *testing.T) {
	cases := []struct {
		name string
		zone gen.Zone
	}{
		{
			name: "too-few-points",
			zone: zoneWithGCPs(t, 47, 8, []localFixturePoint{{localX: 0, localY: 0, east: 0, north: 0}}),
		},
		{
			name: "duplicate-local-points",
			zone: zoneWithGCPs(t, 47, 8, []localFixturePoint{
				{localX: 0, localY: 0, east: 0, north: 0},
				{localX: 0, localY: 0, east: 10, north: 0},
			}),
		},
		{
			name: "duplicate-wgs84-points",
			zone: zoneWithDuplicateWGS84(t),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLocalTransformer(tc.zone); err == nil {
				t.Fatal("expected invalid ground control points to fail")
			}
		})
	}
}

func TestCacheInvalidatesByZoneID(t *testing.T) {
	cache := NewCache()
	zone := syntheticZone(t, 47.3744, 8.5411, 0, 0, 1, 0)
	first, err := cache.Get(zone)
	if err != nil {
		t.Fatalf("cache get failed: %v", err)
	}
	second, err := cache.Get(zone)
	if err != nil {
		t.Fatalf("cache get failed: %v", err)
	}
	if first != second {
		t.Fatal("expected cache hit to return same transformer")
	}
	cache.Invalidate(zone.Id.String())
	third, err := cache.Get(zone)
	if err != nil {
		t.Fatalf("cache get failed: %v", err)
	}
	if first == third {
		t.Fatal("expected invalidate to rebuild transformer")
	}
}

func TestProjectedCRSForLonLatUsesUPSAtPoles(t *testing.T) {
	if got := projectedCRSForLonLat(20, 85); got != "EPSG:32661" {
		t.Fatalf("expected north UPS, got %s", got)
	}
	if got := projectedCRSForLonLat(-20, -81); got != "EPSG:32761" {
		t.Fatalf("expected south UPS, got %s", got)
	}
}

func syntheticZone(t *testing.T, lat, lon, tx, ty, scale, rotation float64) gen.Zone {
	return syntheticZoneTransform(t, lat, lon, tx, ty, scale, rotation*math.Pi/180)
}

func syntheticZoneTransform(t *testing.T, lat, lon, tx, ty, scale, rotationRad float64) gen.Zone {
	t.Helper()
	cosTheta := math.Cos(rotationRad)
	sinTheta := math.Sin(rotationRad)
	points := []localFixturePoint{
		{localX: 0, localY: 0, east: tx, north: ty},
		{localX: 10, localY: 0, east: tx + scale*10*cosTheta, north: ty + scale*10*sinTheta},
		{localX: 0, localY: 10, east: tx - scale*10*sinTheta, north: ty + scale*10*cosTheta},
	}
	return zoneWithGCPs(t, lat, lon, points)
}

func syntheticZoneWithNoise(t *testing.T, lat, lon, tx, ty, scale, rotationDeg, noiseMeters float64) gen.Zone {
	t.Helper()
	rotationRad := rotationDeg * math.Pi / 180
	cosTheta := math.Cos(rotationRad)
	sinTheta := math.Sin(rotationRad)
	noise := []Point2D{
		{X: 0.02, Y: -0.03},
		{X: -0.04, Y: 0.01},
		{X: 0.05, Y: 0.02},
		{X: -0.03, Y: -0.05},
	}
	base := []localFixturePoint{
		{localX: 0, localY: 0, east: tx, north: ty},
		{localX: 12, localY: 0, east: tx + scale*12*cosTheta, north: ty + scale*12*sinTheta},
		{localX: 0, localY: 12, east: tx - scale*12*sinTheta, north: ty + scale*12*cosTheta},
		{localX: 10, localY: 8, east: tx + scale*(10*cosTheta-8*sinTheta), north: ty + scale*(10*sinTheta+8*cosTheta)},
	}
	for i := range base {
		base[i].east += noise[i].X * noiseMeters
		base[i].north += noise[i].Y * noiseMeters
	}
	return zoneWithGCPs(t, lat, lon, base)
}

type localFixturePoint struct {
	localX float64
	localY float64
	east   float64
	north  float64
}

func zoneWithGCPs(t *testing.T, lat, lon float64, points []localFixturePoint) gen.Zone {
	t.Helper()
	gcps := make([]gen.GroundControlPoint, 0, len(points))
	transformer := NewCRSTransformer()
	projectedCRS := projectedCRSForLonLat(lon, lat)
	centerProjected, err := transformer.FromWGS84(projectedCRS, point2D(t, lon, lat))
	if err != nil {
		t.Fatalf("center projection failed: %v", err)
	}
	centerXY := pointXY(t, centerProjected)
	for _, point := range points {
		projectedPoint := point2D(t, centerXY.X+point.east, centerXY.Y+point.north)
		wgs84Point, err := transformer.ToWGS84(projectedCRS, projectedPoint)
		if err != nil {
			t.Fatalf("projected to wgs84 failed: %v", err)
		}
		gcps = append(gcps, gen.GroundControlPoint{
			Local: point2D(t, point.localX, point.localY),
			Wgs84: wgs84Point,
		})
	}
	zoneID := uuid.New()
	return gen.Zone{
		Id:                  [16]byte(zoneID),
		Type:                "uwb",
		GroundControlPoints: &gcps,
	}
}

func zoneWithDuplicateWGS84(t *testing.T) gen.Zone {
	t.Helper()
	wgs := point2D(t, 8, 47)
	gcps := []gen.GroundControlPoint{
		{Local: point2D(t, 0, 0), Wgs84: wgs},
		{Local: point2D(t, 10, 0), Wgs84: wgs},
	}
	zoneID := uuid.New()
	return gen.Zone{
		Id:                  [16]byte(zoneID),
		Type:                "uwb",
		GroundControlPoints: &gcps,
	}
}

func point2D(t *testing.T, x, y float64) gen.Point {
	t.Helper()
	point := gen.Point{Type: "Point"}
	if err := point.Coordinates.FromGeoJsonPosition2D([]float32{float32(x), float32(y)}); err != nil {
		t.Fatalf("point setup failed: %v", err)
	}
	return point
}

func pointXY(t *testing.T, point gen.Point) Point2D {
	t.Helper()
	xy, _, _, err := pointTo2D(point)
	if err != nil {
		t.Fatalf("point decode failed: %v", err)
	}
	return xy
}

func assertPointClose(t *testing.T, want, got gen.Point, tolerance float64) {
	t.Helper()
	wantXY, _, _, err := pointTo2D(want)
	if err != nil {
		t.Fatalf("decode want failed: %v", err)
	}
	gotXY, _, _, err := pointTo2D(got)
	if err != nil {
		t.Fatalf("decode got failed: %v", err)
	}
	if dx := math.Abs(wantXY.X - gotXY.X); dx > tolerance {
		t.Fatalf("x mismatch: want %.9f got %.9f tolerance %.9f", wantXY.X, gotXY.X, tolerance)
	}
	if dy := math.Abs(wantXY.Y - gotXY.Y); dy > tolerance {
		t.Fatalf("y mismatch: want %.9f got %.9f tolerance %.9f", wantXY.Y, gotXY.Y, tolerance)
	}
}

func TestCRSTransformerRejectsUnsupportedCRS(t *testing.T) {
	transformer := NewCRSTransformer()
	if _, err := transformer.ToWGS84("EPSG:999999", point2D(t, 0, 0)); err == nil {
		t.Fatal("expected unsupported CRS to fail")
	}
}

func TestZoneFixtureProjectedCRSStableAcrossHemispheres(t *testing.T) {
	tests := []struct {
		lon  float64
		lat  float64
		want string
	}{
		{lon: 8.5411, lat: 47.3744, want: "EPSG:32632"},
		{lon: -70.6693, lat: -33.4489, want: "EPSG:32719"},
	}
	for _, tc := range tests {
		if got := projectedCRSForLonLat(tc.lon, tc.lat); got != tc.want {
			t.Fatalf("for lon=%v lat=%v expected %s, got %s", tc.lon, tc.lat, tc.want, got)
		}
	}
}

func Example_projectedCRSForLonLat() {
	fmt.Println(projectedCRSForLonLat(8.5411, 47.3744))
	// Output: EPSG:32632
}
