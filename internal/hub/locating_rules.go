package hub

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/formation-res/open-location-hub/internal/httpapi/gen"
)

// Rules are compiled on metadata changes, never evaluated as executable code.
// Comparison binds more tightly than AND; parentheses permit explicit grouping.
type ruleValue struct {
	kind    byte
	number  float64
	text    string
	boolean bool
	present bool
}
type ruleContext struct {
	location gen.Location
	provider gen.LocationProvider
	now      time.Time
}
type ruleNode struct {
	kind        byte
	literal     ruleValue
	name        string
	left, right *ruleNode
}
type compiledLocatingRule struct {
	expression *ruleNode
	priority   float64
}
type compiledLocatingRules struct {
	signature string
	rules     []compiledLocatingRule
}

func compileLocatingRules(rules *[]gen.LocatingRule) ([]compiledLocatingRule, error) {
	if rules == nil {
		return nil, nil
	}
	out := make([]compiledLocatingRule, 0, len(*rules))
	for i, rule := range *rules {
		if rule.Priority < 0 || math.IsNaN(rule.Priority) || math.IsInf(rule.Priority, 0) {
			return nil, fmt.Errorf("locating_rules[%d]: priority must be finite and nonnegative", i)
		}
		node, err := parseLocatingExpression(rule.Expression)
		if err != nil {
			return nil, fmt.Errorf("locating_rules[%d]: %w", i, err)
		}
		out = append(out, compiledLocatingRule{expression: node, priority: rule.Priority})
	}
	return out, nil
}

func (s *Service) rulesForTrackable(trackable gen.Trackable) ([]compiledLocatingRule, error) {
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
	rules, err := compileLocatingRules(trackable.LocatingRules)
	if err == nil {
		s.locatingRuleCache.Store(key, compiledLocatingRules{signature: signature.String(), rules: rules})
	}
	return rules, err
}

func locationRulePriority(rules []compiledLocatingRule, ctx ruleContext) float64 {
	priority := 0.0
	for _, r := range rules {
		value := r.expression.evaluate(ctx)
		if value.present && value.boolean && r.priority > priority {
			priority = r.priority
		}
	}
	return priority
}

func (n *ruleNode) evaluate(ctx ruleContext) ruleValue {
	switch n.kind {
	case 'l':
		return n.literal
	case 'p':
		return ruleProperty(n.name, ctx)
	case '&':
		left := n.left.evaluate(ctx)
		if !left.present || !left.boolean {
			return booleanRuleValue(false)
		}
		right := n.right.evaluate(ctx)
		return booleanRuleValue(right.present && right.boolean)
	case 'c':
		a, b := n.left.evaluate(ctx), n.right.evaluate(ctx)
		if !a.present || !b.present {
			return booleanRuleValue(false)
		}
		compare := 0
		switch a.kind {
		case 'n':
			if a.number < b.number {
				compare = -1
			} else if a.number > b.number {
				compare = 1
			}
		case 's':
			compare = strings.Compare(a.text, b.text)
		case 'b':
			if a.boolean != b.boolean {
				compare = 1
			}
		}
		switch n.name {
		case "=":
			return booleanRuleValue(compare == 0)
		case "!=":
			return booleanRuleValue(compare != 0)
		case "<":
			return booleanRuleValue(compare < 0)
		case "<=":
			return booleanRuleValue(compare <= 0)
		case ">":
			return booleanRuleValue(compare > 0)
		case ">=":
			return booleanRuleValue(compare >= 0)
		}
	}
	return ruleValue{}
}
func booleanRuleValue(value bool) ruleValue {
	return ruleValue{kind: 'b', boolean: value, present: true}
}
func numericRuleValue(value *float64) ruleValue {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return ruleValue{kind: 'n'}
	}
	return ruleValue{kind: 'n', number: *value, present: true}
}
func stringRuleValue(value string) ruleValue { return ruleValue{kind: 's', text: value, present: true} }
func ruleProperty(name string, ctx ruleContext) ruleValue {
	loc := ctx.location
	switch name {
	case "type":
		return stringRuleValue(loc.ProviderType)
	case "provider_id":
		return stringRuleValue(loc.ProviderId)
	case "source":
		return stringRuleValue(loc.Source)
	case "name":
		if ctx.provider.Name != nil {
			return stringRuleValue(*ctx.provider.Name)
		}
		return ruleValue{kind: 's'}
	case "accuracy":
		return numericRuleValue(loc.Accuracy)
	case "floor":
		if loc.Floor == nil {
			return ruleValue{kind: 'n', present: true}
		}
		return numericRuleValue(loc.Floor)
	case "speed":
		return numericRuleValue(loc.Speed)
	case "timestamp_diff":
		if loc.TimestampGenerated == nil {
			return ruleValue{kind: 'n'}
		}
		age := math.Max(0, float64(ctx.now.Sub(*loc.TimestampGenerated))/float64(time.Millisecond))
		return numericRuleValue(&age)
	}
	return ruleValue{}
}

type ruleToken struct {
	text   string
	quoted bool
}
type locatingParser struct {
	tokens    []ruleToken
	at, depth int
}

func parseLocatingExpression(input string) (*ruleNode, error) {
	if len(input) > 4096 {
		return nil, fmt.Errorf("expression exceeds 4096 bytes")
	}
	tokens, err := tokenizeLocatingRule(input)
	if err != nil {
		return nil, err
	}
	p := locatingParser{tokens: tokens}
	n, err := p.and()
	if err != nil {
		return nil, err
	}
	if p.at != len(tokens) {
		return nil, fmt.Errorf("unexpected token %q", tokens[p.at].text)
	}
	if ruleNodeType(n) != 'b' {
		return nil, fmt.Errorf("expression must evaluate to a boolean")
	}
	return n, nil
}
func tokenizeLocatingRule(input string) ([]ruleToken, error) {
	out := []ruleToken{}
	for i := 0; i < len(input); {
		if unicode.IsSpace(rune(input[i])) {
			i++
			continue
		}
		start := i
		c := input[i]
		if c == '\'' || c == '"' {
			i++
			var value strings.Builder
			closed := false
			for i < len(input) {
				if input[i] == c {
					i++
					closed = true
					break
				}
				if input[i] == '\\' {
					i++
					if i >= len(input) {
						break
					}
				}
				value.WriteByte(input[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}
			out = append(out, ruleToken{text: value.String(), quoted: true})
			continue
		}
		if strings.ContainsRune("()=<>!", rune(c)) {
			i++
			if i < len(input) && input[i] == '=' && c != '(' && c != ')' && c != '=' {
				i++
			}
			out = append(out, ruleToken{text: input[start:i]})
			continue
		}
		for i < len(input) && !unicode.IsSpace(rune(input[i])) && !strings.ContainsRune("()=<>!\"'", rune(input[i])) {
			i++
		}
		out = append(out, ruleToken{text: input[start:i]})
	}
	return out, nil
}
func (p *locatingParser) take(value string) bool {
	if p.at < len(p.tokens) && !p.tokens[p.at].quoted && p.tokens[p.at].text == value {
		p.at++
		return true
	}
	return false
}
func (p *locatingParser) and() (*ruleNode, error) {
	left, err := p.comparison()
	if err != nil {
		return nil, err
	}
	for p.take("AND") {
		right, err := p.comparison()
		if err != nil {
			return nil, err
		}
		if ruleNodeType(left) != 'b' || ruleNodeType(right) != 'b' {
			return nil, fmt.Errorf("AND requires boolean operands")
		}
		left = &ruleNode{kind: '&', left: left, right: right}
	}
	return left, nil
}
func (p *locatingParser) comparison() (*ruleNode, error) {
	left, err := p.atom()
	if err != nil {
		return nil, err
	}
	if p.at >= len(p.tokens) || p.tokens[p.at].quoted {
		return left, nil
	}
	op := p.tokens[p.at].text
	switch op {
	case "=", "!=", "<", "<=", ">", ">=":
	default:
		return left, nil
	}
	p.at++
	right, err := p.atom()
	if err != nil {
		return nil, err
	}
	a, b := ruleNodeType(left), ruleNodeType(right)
	if a != b {
		return nil, fmt.Errorf("comparison operands must have the same type")
	}
	if a == 'b' && op != "=" && op != "!=" {
		return nil, fmt.Errorf("booleans only support = and !=")
	}
	return &ruleNode{kind: 'c', name: op, left: left, right: right}, nil
}
func (p *locatingParser) atom() (*ruleNode, error) {
	if p.take("(") {
		p.depth++
		if p.depth > 64 {
			return nil, fmt.Errorf("expression nesting exceeds 64")
		}
		node, err := p.and()
		p.depth--
		if err != nil {
			return nil, err
		}
		if !p.take(")") {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return node, nil
	}
	if p.at >= len(p.tokens) {
		return nil, fmt.Errorf("expected expression")
	}
	token := p.tokens[p.at]
	p.at++
	if token.quoted {
		return &ruleNode{kind: 'l', literal: stringRuleValue(token.text)}, nil
	}
	if token.text == "TRUE" || token.text == "FALSE" {
		return &ruleNode{kind: 'l', literal: booleanRuleValue(token.text == "TRUE")}, nil
	}
	switch token.text {
	case "accuracy", "name", "type", "source", "floor", "speed", "provider_id", "timestamp_diff":
		return &ruleNode{kind: 'p', name: token.text}, nil
	case "uwb", "gps", "wifi", "rfid", "ibeacon", "virtual", "unknown":
		return &ruleNode{kind: 'l', literal: stringRuleValue(token.text)}, nil
	}
	if value, err := strconv.ParseFloat(token.text, 64); err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
		return &ruleNode{kind: 'l', literal: numericRuleValue(&value)}, nil
	}
	return nil, fmt.Errorf("unknown token %q", token.text)
}
func ruleNodeType(n *ruleNode) byte {
	switch n.kind {
	case 'l':
		return n.literal.kind
	case 'p':
		switch n.name {
		case "name", "type", "source", "provider_id":
			return 's'
		default:
			return 'n'
		}
	default:
		return 'b'
	}
}
