package main

import (
	"fmt"
	"net/url"
	"strings"
)

func validateConfigInput(algorithm string, params map[string]any) error {
	switch algorithm {
	case AlgoFixedToken:
		if err := requireIntParam(params, "sustainedRps", 1); err != nil {
			return err
		}
		return requireIntParam(params, "burst", 1)
	case AlgoFixedWindow, AlgoSlidingWindow:
		if err := requireIntParam(params, "limit", 1); err != nil {
			return err
		}
		return requireIntParam(params, "windowSeconds", 1)
	default:
		return fmt.Errorf("unknown algorithm %q", algorithm)
	}
}

func requireIntParam(params map[string]any, key string, min int) error {
	if params == nil {
		return fmt.Errorf("params.%s is required", key)
	}
	raw, ok := params[key]
	if !ok {
		return fmt.Errorf("params.%s is required", key)
	}
	n, ok := asInt(raw)
	if !ok {
		return fmt.Errorf("params.%s must be an integer", key)
	}
	if n < min {
		return fmt.Errorf("params.%s must be >= %d", key, min)
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float64:
		if t != float64(int(t)) {
			return 0, false
		}
		return int(t), true
	default:
		return 0, false
	}
}

func validateNamespaceInput(label, upstream, matchMode, configID string) error {
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("label is required")
	}
	if strings.TrimSpace(configID) == "" {
		return fmt.Errorf("configId is required")
	}
	if matchMode != MatchModeAll && matchMode != MatchModeAny {
		return fmt.Errorf("matchMode must be %q or %q", MatchModeAll, MatchModeAny)
	}
	u, err := url.Parse(strings.TrimSpace(upstream))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstreamHost must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstreamHost scheme must be http or https")
	}
	return nil
}

func validateRuleMatch(m RuleMatch) error {
	switch m.Kind {
	case MatchHeader:
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("match.name is required for header rules")
		}
		return validateEqIn(m)
	case MatchPath:
		if strings.TrimSpace(m.Value) == "" {
			return fmt.Errorf("match.value is required for path rules")
		}
		if m.Op != "eq" && m.Op != "prefix" {
			return fmt.Errorf("match.op must be eq or prefix for path rules")
		}
		return nil
	case MatchPathAttr:
		if strings.TrimSpace(m.Pattern) == "" || strings.TrimSpace(m.Attr) == "" {
			return fmt.Errorf("match.pattern and match.attr are required for path_attr rules")
		}
		return validateEqIn(m)
	case MatchMethod:
		return validateEqIn(m)
	case MatchRegex:
		if strings.TrimSpace(m.Pattern) == "" {
			return fmt.Errorf("match.pattern is required for regex rules")
		}
		if m.Field != "path" && m.Field != "full" {
			return fmt.Errorf("match.field must be path or full")
		}
		return nil
	default:
		return fmt.Errorf("unknown match.kind %q", m.Kind)
	}
}

func validateEqIn(m RuleMatch) error {
	switch m.Op {
	case "eq":
		if strings.TrimSpace(m.Value) == "" {
			return fmt.Errorf("match.value is required when op=eq")
		}
	case "in":
		if len(m.Values) == 0 {
			return fmt.Errorf("match.values is required when op=in")
		}
	default:
		return fmt.Errorf("match.op must be eq or in")
	}
	return nil
}
