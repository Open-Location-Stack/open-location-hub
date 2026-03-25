package auth

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Permission is a route-level authorization capability granted to a role.
type Permission string

const (
	// CreateAny grants create access without ownership constraints.
	CreateAny Permission = "CREATE_ANY"
	// ReadAny grants read access without ownership constraints.
	ReadAny Permission = "READ_ANY"
	// UpdateAny grants update access without ownership constraints.
	UpdateAny Permission = "UPDATE_ANY"
	// DeleteAny grants delete access without ownership constraints.
	DeleteAny Permission = "DELETE_ANY"
	// CreateOwn grants create access for resources owned by the principal.
	CreateOwn Permission = "CREATE_OWN"
	// ReadOwn grants read access for resources owned by the principal.
	ReadOwn Permission = "READ_OWN"
	// UpdateOwn grants update access for resources owned by the principal.
	UpdateOwn Permission = "UPDATE_OWN"
	// DeleteOwn grants delete access for resources owned by the principal.
	DeleteOwn Permission = "DELETE_OWN"
)

// Rule binds a route pattern to the permissions granted for that pattern.
type Rule struct {
	Pattern     string
	Permissions map[Permission]struct{}
}

// RPCPolicy holds per-role RPC discovery and invocation permissions.
type RPCPolicy struct {
	Discover bool
	Invoke   []MethodRule
}

// MethodRule grants invocation permission for a single method or wildcard.
type MethodRule struct {
	Pattern string
}

type rolePolicy struct {
	Routes []Rule
	RPC    RPCPolicy
}

// Registry holds the role-to-route authorization rules loaded from YAML.
type Registry struct {
	roles map[string]rolePolicy
}

// LoadRegistry reads the authorization registry from the configured YAML file.
func LoadRegistry(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	registry := &Registry{roles: make(map[string]rolePolicy, len(doc))}
	for role, entries := range doc {
		rules := make([]Rule, 0, len(entries))
		rpcPolicy := RPCPolicy{}
		for pattern, v := range entries {
			if pattern == "description" {
				continue
			}
			if pattern == "rpc" {
				parsed, err := parseRPCPolicy(v)
				if err != nil {
					return nil, fmt.Errorf("role %q rpc: %w", role, err)
				}
				rpcPolicy = parsed
				continue
			}
			perms, err := parsePermissions(v)
			if err != nil {
				return nil, fmt.Errorf("role %q pattern %q: %w", role, pattern, err)
			}
			if err := validateRule(pattern, perms); err != nil {
				return nil, fmt.Errorf("role %q pattern %q: %w", role, pattern, err)
			}
			rules = append(rules, Rule{Pattern: pattern, Permissions: perms})
		}
		sort.SliceStable(rules, func(i, j int) bool {
			return compareSpecificity(rules[i].Pattern, rules[j].Pattern) > 0
		})
		sort.SliceStable(rpcPolicy.Invoke, func(i, j int) bool {
			return compareMethodSpecificity(rpcPolicy.Invoke[i].Pattern, rpcPolicy.Invoke[j].Pattern) > 0
		})
		registry.roles[role] = rolePolicy{Routes: rules, RPC: rpcPolicy}
	}
	return registry, nil
}

func parseRPCPolicy(v any) (RPCPolicy, error) {
	body, ok := v.(map[string]any)
	if !ok {
		return RPCPolicy{}, fmt.Errorf("rpc policy must be a map")
	}
	out := RPCPolicy{}
	if discover, ok := body["discover"]; ok {
		flag, ok := discover.(bool)
		if !ok {
			return RPCPolicy{}, fmt.Errorf("rpc discover must be a boolean")
		}
		out.Discover = flag
	}
	if invoke, ok := body["invoke"]; ok {
		methods, ok := invoke.([]any)
		if ok {
			for _, item := range methods {
				s, ok := item.(string)
				if !ok {
					return RPCPolicy{}, fmt.Errorf("rpc invoke list must contain strings")
				}
				out.Invoke = append(out.Invoke, MethodRule{Pattern: strings.TrimSpace(s)})
			}
			return out, nil
		}
		methodMap, ok := invoke.(map[string]any)
		if !ok {
			return RPCPolicy{}, fmt.Errorf("rpc invoke must be a list or map")
		}
		for pattern, raw := range methodMap {
			allowed, ok := raw.(bool)
			if !ok {
				return RPCPolicy{}, fmt.Errorf("rpc invoke entry %q must be a boolean", pattern)
			}
			if allowed {
				out.Invoke = append(out.Invoke, MethodRule{Pattern: strings.TrimSpace(pattern)})
			}
		}
	}
	return out, nil
}

func parsePermissions(v any) (map[Permission]struct{}, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("permissions must be a list")
	}
	out := make(map[Permission]struct{}, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("permission must be a string")
		}
		p := Permission(strings.TrimSpace(strings.ToUpper(s)))
		switch p {
		case CreateAny, ReadAny, UpdateAny, DeleteAny, CreateOwn, ReadOwn, UpdateOwn, DeleteOwn:
			out[p] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported permission %q", s)
		}
	}
	return out, nil
}

func validateRule(pattern string, perms map[Permission]struct{}) error {
	if !supportsOwnPermissions(pattern) {
		for perm := range perms {
			if strings.HasSuffix(string(perm), "_OWN") {
				return fmt.Errorf("%s requires at least one path parameter in the route pattern", perm)
			}
		}
	}
	return nil
}

// Authorize checks whether the principal attached to the request is allowed to
// access the requested route and HTTP method.
func (r *Registry) Authorize(req *http.Request, principal *Principal) error {
	requiredAny, requiredOwn, err := permissionsForMethod(req.Method)
	if err != nil {
		return forbidden(err.Error())
	}
	if principal == nil {
		return unauthorized("missing authenticated principal")
	}

	matched := false
	for _, role := range principal.Roles {
		policy := r.roles[role]
		rule, params, ok := bestMatchingRule(policy.Routes, req.URL.Path)
		if !ok {
			continue
		}
		matched = true
		if _, ok := rule.Permissions[requiredAny]; ok {
			return nil
		}
		if _, ok := rule.Permissions[requiredOwn]; ok && ownsAll(principal, params) {
			return nil
		}
	}
	if !matched {
		return forbidden("token does not grant access to this endpoint")
	}
	return forbidden("token does not grant access to this endpoint")
}

// AuthorizeRPCDiscover checks whether the principal may list reachable RPC methods.
func (r *Registry) AuthorizeRPCDiscover(principal *Principal) error {
	if principal == nil {
		return unauthorized("missing authenticated principal")
	}
	for _, role := range principal.Roles {
		if r.roles[role].RPC.Discover {
			return nil
		}
	}
	return forbidden("token does not grant access to rpc method discovery")
}

// AuthorizeRPCInvoke checks whether the principal may invoke the supplied RPC method.
func (r *Registry) AuthorizeRPCInvoke(principal *Principal, method string) error {
	if principal == nil {
		return unauthorized("missing authenticated principal")
	}
	for _, role := range principal.Roles {
		if methodAllowed(r.roles[role].RPC.Invoke, method) {
			return nil
		}
	}
	return forbidden("token does not grant access to rpc method invocation")
}

func permissionsForMethod(method string) (Permission, Permission, error) {
	switch method {
	case http.MethodGet, http.MethodHead:
		return ReadAny, ReadOwn, nil
	case http.MethodPost:
		return CreateAny, CreateOwn, nil
	case http.MethodPut, http.MethodPatch:
		return UpdateAny, UpdateOwn, nil
	case http.MethodDelete:
		return DeleteAny, DeleteOwn, nil
	default:
		return "", "", fmt.Errorf("unsupported authorization method %s", method)
	}
}

func matchPattern(pattern, path string) (map[string]string, bool) {
	patternParts := splitPath(pattern)
	pathParts := splitPath(path)
	params := map[string]string{}

	for i := 0; i < len(patternParts); i++ {
		if i >= len(pathParts) {
			return nil, false
		}
		switch seg := patternParts[i]; {
		case seg == "*":
			return params, true
		case strings.HasPrefix(seg, ":"):
			params[strings.TrimPrefix(seg, ":")] = pathParts[i]
		case seg != pathParts[i]:
			return nil, false
		}
	}
	return params, len(patternParts) == len(pathParts)
}

func bestMatchingRule(rules []Rule, path string) (Rule, map[string]string, bool) {
	var (
		bestRule   Rule
		bestParams map[string]string
		found      bool
	)
	for _, rule := range rules {
		params, ok := matchPattern(rule.Pattern, path)
		if !ok {
			continue
		}
		if !found || compareSpecificity(rule.Pattern, bestRule.Pattern) > 0 {
			bestRule = rule
			bestParams = params
			found = true
		}
	}
	return bestRule, bestParams, found
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func supportsOwnPermissions(pattern string) bool {
	for _, part := range splitPath(pattern) {
		if strings.HasPrefix(part, ":") {
			return true
		}
	}
	return false
}

func compareSpecificity(left, right string) int {
	leftScore := specificityScore(left)
	rightScore := specificityScore(right)
	switch {
	case leftScore > rightScore:
		return 1
	case leftScore < rightScore:
		return -1
	default:
		return strings.Compare(left, right)
	}
}

func specificityScore(pattern string) int {
	score := 0
	for _, part := range splitPath(pattern) {
		switch {
		case part == "*":
			score += 1
		case strings.HasPrefix(part, ":"):
			score += 10
		default:
			score += 100
		}
	}
	return score
}

func compareMethodSpecificity(left, right string) int {
	leftScore := methodSpecificityScore(left)
	rightScore := methodSpecificityScore(right)
	switch {
	case leftScore > rightScore:
		return 1
	case leftScore < rightScore:
		return -1
	default:
		return strings.Compare(left, right)
	}
}

func methodSpecificityScore(pattern string) int {
	switch {
	case pattern == "*":
		return 1
	case strings.HasSuffix(pattern, "*"):
		return len(pattern)
	default:
		return len(pattern) + 1000
	}
}

func methodAllowed(rules []MethodRule, method string) bool {
	for _, rule := range rules {
		switch {
		case rule.Pattern == "*":
			return true
		case strings.HasSuffix(rule.Pattern, "*") && strings.HasPrefix(method, strings.TrimSuffix(rule.Pattern, "*")):
			return true
		case rule.Pattern == method:
			return true
		}
	}
	return false
}

func ownsAll(principal *Principal, params map[string]string) bool {
	if len(params) == 0 {
		return false
	}
	for name, value := range params {
		key := ownedResourceKey(name)
		values, ok := principal.OwnedResources[key]
		if !ok {
			return false
		}
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func ownedResourceKey(param string) string {
	param = strings.TrimSpace(param)
	if param == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range param {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	s := strings.ToLower(b.String())
	if strings.HasSuffix(s, "_id") {
		return s + "s"
	}
	return s + "s"
}
