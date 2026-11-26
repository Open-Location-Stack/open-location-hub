package hub

import (
	"fmt"
	"strings"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
	"github.com/formation-res/open-location-hub/internal/locating"
)

type compiledLocatingRules struct {
	signature string
	rules     locating.Rules
}

func (s *Service) rulesForTrackable(trackable gen.Trackable) (locating.Rules, error) {
	var signature strings.Builder
	if trackable.LocatingRules != nil {
		for _, r := range *trackable.LocatingRules {
			fmt.Fprintf(&signature, "%g:%d:%s;", r.Priority, len(r.Expression), r.Expression)
		}
	}
	key := trackable.Id.String()
	if cached, ok := s.locatingRuleCache.Load(key); ok {
		entry := cached.(compiledLocatingRules)
		if entry.signature == signature.String() {
			return entry.rules, nil
		}
	}
	rules, err := locating.Compile(trackable.LocatingRules)
	if err == nil {
		s.locatingRuleCache.Store(key, compiledLocatingRules{signature: signature.String(), rules: rules})
	}
	return rules, err
}
