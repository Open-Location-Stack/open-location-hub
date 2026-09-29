package hub

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

func validTechnology(value string) bool {
	switch value {
	case "uwb", "gps", "wifi", "rfid", "ibeacon", "virtual", "unknown":
		return true
	}
	return false
}
func nonnegative(name string, value *float64) error {
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
		return badRequest(name + " must be a finite nonnegative number")
	}
	return nil
}
func validDuration(name string, value *gen.PositiveOrMinusOne) error {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return badRequest(name + " must be nonnegative or -1")
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 && n != -1 {
		return badRequest(name + " must be nonnegative or -1")
	}
	return nil
}
func validateExitSettings(tolerance *float64, timeout, toleranceTimeout, delay *gen.PositiveOrMinusOne) error {
	if err := nonnegative("exit_tolerance", tolerance); err != nil {
		return err
	}
	for name, value := range map[string]*gen.PositiveOrMinusOne{"timeout": timeout, "tolerance_timeout": toleranceTimeout, "exit_delay": delay} {
		if err := validDuration(name, value); err != nil {
			return err
		}
	}
	return nil
}
func validatePoint(point gen.Point, geographic bool) error {
	if point.Type != "Point" {
		return badRequest("geometry must be a Point")
	}
	var coords []float64
	raw, err := json.Marshal(point.Coordinates)
	if err != nil || json.Unmarshal(raw, &coords) != nil || (len(coords) != 2 && len(coords) != 3) {
		return badRequest("point must contain two or three coordinates")
	}
	for _, v := range coords {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return badRequest("coordinates must be finite")
		}
	}
	if geographic && (coords[0] < -180 || coords[0] > 180 || coords[1] < -90 || coords[1] > 90) {
		return badRequest("geographic coordinates must be longitude/latitude within world bounds")
	}
	return nil
}
func validatePolygon(polygon gen.Polygon, geographic bool) error {
	if polygon.Type != "Polygon" || len(polygon.Coordinates) == 0 {
		return badRequest("geometry must be a nonempty Polygon")
	}
	for _, ring := range polygon.Coordinates {
		if len(ring) < 4 {
			return badRequest("polygon rings require at least three vertices and a closing vertex")
		}
		for _, coord := range ring {
			if err := validatePoint(gen.Point{Type: "Point", Coordinates: coord}, geographic); err != nil {
				return err
			}
		}
		first, _ := geoPoint(ring[0])
		last, _ := geoPoint(ring[len(ring)-1])
		if first != last {
			return badRequest("polygon rings must be closed")
		}
		area := 0.0
		for i := 1; i < len(ring); i++ {
			a, _ := geoPoint(ring[i-1])
			b, _ := geoPoint(ring[i])
			area += (a[0]-first[0])*(b[1]-first[1]) - (b[0]-first[0])*(a[1]-first[1])
		}
		if area == 0 {
			return badRequest("polygon rings must enclose an area")
		}
	}
	return nil
}
func validateElevation[T ~string](ref *T) error {
	if ref != nil && *ref != "floor" && *ref != "wgs84" {
		return badRequest("elevation_ref must be floor or wgs84")
	}
	return nil
}
func validateSupportedCRS(crs *string) error {
	if crs == nil {
		return nil
	}
	value := strings.TrimSpace(*crs)
	if value == "" || value == "local" || value == "EPSG:4326" {
		return nil
	}
	code, err := strconv.Atoi(strings.TrimPrefix(value, "EPSG:"))
	if strings.HasPrefix(value, "EPSG:") && err == nil && (code >= 32601 && code <= 32661 || code >= 32701 && code <= 32761) {
		return nil
	}
	return badRequest("crs must be local, EPSG:4326, or a WGS84 UTM/UPS code (EPSG:32601-32661 or EPSG:32701-32761)")
}
