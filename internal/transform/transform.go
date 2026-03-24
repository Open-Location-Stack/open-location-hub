package transform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
	proj "github.com/twpayne/go-proj/v11"
	"gonum.org/v1/gonum/mat"
)

const wgs84 = "EPSG:4326"

var (
	errInsufficientGroundControlPoints = errors.New("zone requires at least 2 valid ground control points")
	errDegenerateTransform             = errors.New("zone ground control points produce a degenerate transform")
)

// Point2D represents a planar point in meters.
type Point2D struct {
	X float64
	Y float64
}

// CRSTransformer converts points between WGS84 and projected EPSG coordinate
// reference systems.
type CRSTransformer struct {
	mu      sync.Mutex
	forward map[string]*proj.PJ
}

// NewCRSTransformer constructs a reusable CRS transformer cache.
func NewCRSTransformer() *CRSTransformer {
	return &CRSTransformer{forward: map[string]*proj.PJ{}}
}

// ToWGS84 converts a point from the supplied CRS into WGS84.
func (t *CRSTransformer) ToWGS84(crs string, point gen.Point) (gen.Point, error) {
	source := strings.TrimSpace(crs)
	if source == "" || source == wgs84 {
		return clonePoint(point)
	}
	pj, err := t.transformer(source, wgs84)
	if err != nil {
		return gen.Point{}, err
	}
	return applyPJ(pj, point)
}

// FromWGS84 converts a WGS84 point into the supplied CRS.
func (t *CRSTransformer) FromWGS84(crs string, point gen.Point) (gen.Point, error) {
	target := strings.TrimSpace(crs)
	if target == "" || target == wgs84 {
		return clonePoint(point)
	}
	pj, err := t.transformer(wgs84, target)
	if err != nil {
		return gen.Point{}, err
	}
	return applyPJ(pj, point)
}

func (t *CRSTransformer) transformer(source, target string) (*proj.PJ, error) {
	key := source + "->" + target
	t.mu.Lock()
	defer t.mu.Unlock()
	if pj, ok := t.forward[key]; ok {
		return pj, nil
	}
	pj, err := proj.NewCRSToCRS(source, target, nil)
	if err != nil {
		return nil, err
	}
	pj, err = pj.NormalizeForVisualization()
	if err != nil {
		return nil, err
	}
	t.forward[key] = pj
	return pj, nil
}

// LocalTransformer converts between OMLOX local zone coordinates and WGS84
// using zone ground control points.
type LocalTransformer struct {
	projectedCRS string
	toWGS84      *proj.PJ
	fromWGS84    *proj.PJ
	a            float64
	b            float64
	tx           float64
	ty           float64
}

// ProjectedCRS returns the intermediate projected CRS used for local
// georeferencing.
func (t *LocalTransformer) ProjectedCRS() string {
	return t.projectedCRS
}

// NewLocalTransformer fits a local-to-WGS84 transform from the zone's ground
// control points.
func NewLocalTransformer(zone gen.Zone) (*LocalTransformer, error) {
	if zone.GroundControlPoints == nil || len(*zone.GroundControlPoints) < 2 {
		return nil, errInsufficientGroundControlPoints
	}
	gcps, err := validGroundControlPoints(*zone.GroundControlPoints)
	if err != nil {
		return nil, err
	}
	projectedCRS, err := projectedCRSForGroundControlPoints(gcps)
	if err != nil {
		return nil, err
	}
	toProjected, err := proj.NewCRSToCRS(wgs84, projectedCRS, nil)
	if err != nil {
		return nil, err
	}
	toProjected, err = toProjected.NormalizeForVisualization()
	if err != nil {
		return nil, err
	}
	toWGS84, err := proj.NewCRSToCRS(projectedCRS, wgs84, nil)
	if err != nil {
		return nil, err
	}
	toWGS84, err = toWGS84.NormalizeForVisualization()
	if err != nil {
		return nil, err
	}
	a, b, tx, ty, err := fitSimilarity(gcps, toProjected)
	if err != nil {
		return nil, err
	}
	return &LocalTransformer{
		projectedCRS: projectedCRS,
		toWGS84:      toWGS84,
		fromWGS84:    toProjected,
		a:            a,
		b:            b,
		tx:           tx,
		ty:           ty,
	}, nil
}

// LocalToWGS84 converts a local zone point to WGS84.
func (t *LocalTransformer) LocalToWGS84(point gen.Point) (gen.Point, error) {
	local, z, hasZ, err := pointTo2D(point)
	if err != nil {
		return gen.Point{}, err
	}
	projected := Point2D{
		X: t.a*local.X - t.b*local.Y + t.tx,
		Y: t.b*local.X + t.a*local.Y + t.ty,
	}
	return projectToPoint(t.toWGS84, projected, z, hasZ)
}

// WGS84ToLocal converts a WGS84 point to the zone's local coordinate space.
func (t *LocalTransformer) WGS84ToLocal(point gen.Point) (gen.Point, error) {
	projectedPoint, z, hasZ, err := pointToProjected(t.fromWGS84, point)
	if err != nil {
		return gen.Point{}, err
	}
	denom := t.a*t.a + t.b*t.b
	if denom <= 1e-12 {
		return gen.Point{}, errDegenerateTransform
	}
	dx := projectedPoint.X - t.tx
	dy := projectedPoint.Y - t.ty
	local := Point2D{
		X: (t.a*dx + t.b*dy) / denom,
		Y: (-t.b*dx + t.a*dy) / denom,
	}
	return pointFrom2D(local, z, hasZ)
}

// Cache memoizes LocalTransformer instances by zone identifier and ground
// control point signature.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cachedTransformer
}

type cachedTransformer struct {
	signature string
	value     *LocalTransformer
}

// NewCache constructs an empty LocalTransformer cache.
func NewCache() *Cache {
	return &Cache{entries: map[string]cachedTransformer{}}
}

// Get returns a cached LocalTransformer for the zone or rebuilds it when the
// zone geometry has changed.
func (c *Cache) Get(zone gen.Zone) (*LocalTransformer, error) {
	signature, err := zoneSignature(zone)
	if err != nil {
		return nil, err
	}
	id := zone.Id.String()

	c.mu.RLock()
	entry, ok := c.entries[id]
	c.mu.RUnlock()
	if ok && entry.signature == signature {
		return entry.value, nil
	}

	value, err := NewLocalTransformer(zone)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[id] = cachedTransformer{signature: signature, value: value}
	c.mu.Unlock()
	return value, nil
}

// Invalidate removes the cached transformer for the supplied zone identifier.
func (c *Cache) Invalidate(zoneID string) {
	c.mu.Lock()
	delete(c.entries, zoneID)
	c.mu.Unlock()
}

// ClonePoint deep-copies a generated point value.
func ClonePoint(point gen.Point) (gen.Point, error) {
	return clonePoint(point)
}

func zoneSignature(zone gen.Zone) (string, error) {
	payload, err := json.Marshal(zone.GroundControlPoints)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func validGroundControlPoints(gcps []gen.GroundControlPoint) ([]gen.GroundControlPoint, error) {
	if len(gcps) < 2 {
		return nil, errInsufficientGroundControlPoints
	}
	out := make([]gen.GroundControlPoint, 0, len(gcps))
	seenLocal := map[string]struct{}{}
	seenWGS84 := map[string]struct{}{}
	for _, gcp := range gcps {
		local, _, _, err := pointTo2D(gcp.Local)
		if err != nil {
			return nil, fmt.Errorf("invalid local ground control point: %w", err)
		}
		wgs, _, _, err := pointTo2D(gcp.Wgs84)
		if err != nil {
			return nil, fmt.Errorf("invalid wgs84 ground control point: %w", err)
		}
		localKey := fmt.Sprintf("%.9f:%.9f", local.X, local.Y)
		if _, ok := seenLocal[localKey]; ok {
			return nil, errDegenerateTransform
		}
		seenLocal[localKey] = struct{}{}
		wgsKey := fmt.Sprintf("%.9f:%.9f", wgs.X, wgs.Y)
		if _, ok := seenWGS84[wgsKey]; ok {
			return nil, errDegenerateTransform
		}
		seenWGS84[wgsKey] = struct{}{}
		out = append(out, gcp)
	}
	return out, nil
}

func projectedCRSForGroundControlPoints(gcps []gen.GroundControlPoint) (string, error) {
	if len(gcps) == 0 {
		return "", errInsufficientGroundControlPoints
	}
	var sumLat float64
	var sumSin float64
	var sumCos float64
	for _, gcp := range gcps {
		wgs, _, _, err := pointTo2D(gcp.Wgs84)
		if err != nil {
			return "", err
		}
		sumLat += wgs.Y
		lonRad := wgs.X * math.Pi / 180
		sumSin += math.Sin(lonRad)
		sumCos += math.Cos(lonRad)
	}
	lat := sumLat / float64(len(gcps))
	lon := math.Atan2(sumSin/float64(len(gcps)), sumCos/float64(len(gcps))) * 180 / math.Pi
	return projectedCRSForLonLat(lon, lat), nil
}

func projectedCRSForLonLat(lon, lat float64) string {
	if lat >= 84 {
		return "EPSG:32661"
	}
	if lat <= -80 {
		return "EPSG:32761"
	}
	zone := int(math.Floor((lon+180)/6)) + 1
	if zone < 1 {
		zone = 1
	}
	if zone > 60 {
		zone = 60
	}
	if lat >= 0 {
		return fmt.Sprintf("EPSG:%d", 32600+zone)
	}
	return fmt.Sprintf("EPSG:%d", 32700+zone)
}

func fitSimilarity(gcps []gen.GroundControlPoint, toProjected *proj.PJ) (float64, float64, float64, float64, error) {
	rows := len(gcps) * 2
	design := mat.NewDense(rows, 4, nil)
	observation := mat.NewDense(rows, 1, nil)
	for i, gcp := range gcps {
		local, _, _, err := pointTo2D(gcp.Local)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		projected, _, _, err := pointToProjected(toProjected, gcp.Wgs84)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		row := i * 2
		design.Set(row, 0, local.X)
		design.Set(row, 1, -local.Y)
		design.Set(row, 2, 1)
		design.Set(row, 3, 0)
		observation.Set(row, 0, projected.X)

		design.Set(row+1, 0, local.Y)
		design.Set(row+1, 1, local.X)
		design.Set(row+1, 2, 0)
		design.Set(row+1, 3, 1)
		observation.Set(row+1, 0, projected.Y)
	}

	var qr mat.QR
	qr.Factorize(design)
	var solution mat.Dense
	if err := qr.SolveTo(&solution, false, observation); err != nil {
		return 0, 0, 0, 0, err
	}
	a := solution.At(0, 0)
	b := solution.At(1, 0)
	tx := solution.At(2, 0)
	ty := solution.At(3, 0)
	if math.Hypot(a, b) <= 1e-9 {
		return 0, 0, 0, 0, errDegenerateTransform
	}
	return a, b, tx, ty, nil
}

func applyPJ(pj *proj.PJ, point gen.Point) (gen.Point, error) {
	projected, z, hasZ, err := pointToProjected(pj, point)
	if err != nil {
		return gen.Point{}, err
	}
	return pointFrom2D(projected, z, hasZ)
}

func pointToProjected(pj *proj.PJ, point gen.Point) (Point2D, float64, bool, error) {
	xy, z, hasZ, err := pointTo2D(point)
	if err != nil {
		return Point2D{}, 0, false, err
	}
	coord, err := pj.Forward(proj.NewCoord(xy.X, xy.Y, z, 0))
	if err != nil {
		return Point2D{}, 0, false, err
	}
	return Point2D{X: coord.X(), Y: coord.Y()}, coord.Z(), hasZ, nil
}

func projectToPoint(pj *proj.PJ, point Point2D, z float64, hasZ bool) (gen.Point, error) {
	coord, err := pj.Forward(proj.NewCoord(point.X, point.Y, z, 0))
	if err != nil {
		return gen.Point{}, err
	}
	return pointFrom2D(Point2D{X: coord.X(), Y: coord.Y()}, coord.Z(), hasZ)
}

func pointTo2D(point gen.Point) (Point2D, float64, bool, error) {
	coords3d, err := point.Coordinates.AsGeoJsonPosition3D()
	if err == nil && len(coords3d) >= 3 {
		return Point2D{X: float64(coords3d[0]), Y: float64(coords3d[1])}, float64(coords3d[2]), true, nil
	}
	coords2d, err := point.Coordinates.AsGeoJsonPosition2D()
	if err == nil && len(coords2d) >= 2 {
		return Point2D{X: float64(coords2d[0]), Y: float64(coords2d[1])}, 0, false, nil
	}
	return Point2D{}, 0, false, errors.New("point must contain 2D or 3D coordinates")
}

func pointFrom2D(point Point2D, z float64, hasZ bool) (gen.Point, error) {
	out := gen.Point{Type: "Point"}
	if hasZ {
		if err := out.Coordinates.FromGeoJsonPosition3D([]float32{float32(point.X), float32(point.Y), float32(z)}); err != nil {
			return gen.Point{}, err
		}
		return out, nil
	}
	if err := out.Coordinates.FromGeoJsonPosition2D([]float32{float32(point.X), float32(point.Y)}); err != nil {
		return gen.Point{}, err
	}
	return out, nil
}

func clonePoint(point gen.Point) (gen.Point, error) {
	xy, z, hasZ, err := pointTo2D(point)
	if err != nil {
		return gen.Point{}, err
	}
	return pointFrom2D(xy, z, hasZ)
}
