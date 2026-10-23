package main

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

type matchHit struct {
	ns         CompiledNamespace
	specificity int
	headerSub  string
	pathAttrSub string
}

// MatchResult is the winning namespace for a request, plus Redis subject.
type MatchResult struct {
	Namespace CompiledNamespace
	Subject   string
}

func matchRequest(snap *PolicySnapshot, r *http.Request) (MatchResult, bool) {
	if snap == nil || len(snap.Namespaces) == 0 {
		return MatchResult{}, false
	}

	var hits []matchHit
	for _, ns := range snap.Namespaces {
		ok, spec, hdr, attr := namespaceMatches(ns, r)
		if !ok {
			continue
		}
		hits = append(hits, matchHit{ns: ns, specificity: spec, headerSub: hdr, pathAttrSub: attr})
	}
	if len(hits) == 0 {
		return MatchResult{}, false
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].ns.Namespace.Priority != hits[j].ns.Namespace.Priority {
			return hits[i].ns.Namespace.Priority < hits[j].ns.Namespace.Priority
		}
		return hits[i].specificity > hits[j].specificity
	})

	best := hits[0]
	return MatchResult{
		Namespace: best.ns,
		Subject:   buildSubject(r, best),
	}, true
}

func namespaceMatches(ns CompiledNamespace, r *http.Request) (ok bool, specificity int, headerSub, pathAttrSub string) {
	rules := ns.Rules
	if len(rules) == 0 {
		// No rules: never match (must opt-in via matchers).
		return false, 0, "", ""
	}

	mode := ns.Namespace.MatchMode
	if mode == "" {
		mode = MatchModeAll
	}

	var maxSpec int
	var anyMatched bool
	for _, rule := range rules {
		matched, spec, hdr, attr := ruleMatches(rule.Match, r)
		if matched {
			anyMatched = true
			if spec > maxSpec {
				maxSpec = spec
			}
			if hdr != "" && headerSub == "" {
				headerSub = hdr
			}
			if attr != "" && pathAttrSub == "" {
				pathAttrSub = attr
			}
			if mode == MatchModeAny {
				return true, maxSpec, headerSub, pathAttrSub
			}
		} else if mode == MatchModeAll {
			return false, 0, "", ""
		}
	}
	if mode == MatchModeAll {
		return true, maxSpec, headerSub, pathAttrSub
	}
	return anyMatched, maxSpec, headerSub, pathAttrSub
}

func ruleMatches(m RuleMatch, r *http.Request) (ok bool, specificity int, headerSub, pathAttrSub string) {
	switch m.Kind {
	case MatchHeader:
		val := headerValue(r, m.Name)
		if !compareOp(m.Op, val, m.Value, m.Values) {
			return false, 0, "", ""
		}
		return true, specificityOf(MatchHeader), val, ""
	case MatchPath:
		path := r.URL.Path
		switch m.Op {
		case "eq":
			if path != m.Value {
				return false, 0, "", ""
			}
		case "prefix":
			if !strings.HasPrefix(path, m.Value) {
				return false, 0, "", ""
			}
		default:
			return false, 0, "", ""
		}
		return true, specificityOf(MatchPath), "", ""
	case MatchPathAttr:
		re, err := regexp.Compile(m.Pattern)
		if err != nil {
			return false, 0, "", ""
		}
		sub := re.FindStringSubmatch(r.URL.Path)
		if sub == nil {
			return false, 0, "", ""
		}
		idx := re.SubexpIndex(m.Attr)
		if idx < 0 || idx >= len(sub) {
			return false, 0, "", ""
		}
		attrVal := sub[idx]
		if !compareOp(m.Op, attrVal, m.Value, m.Values) {
			return false, 0, "", ""
		}
		return true, specificityOf(MatchPathAttr), "", attrVal
	case MatchMethod:
		method := strings.ToUpper(r.Method)
		want := m.Value
		vals := m.Values
		if want != "" {
			want = strings.ToUpper(want)
		}
		if len(vals) > 0 {
			up := make([]string, len(vals))
			for i, v := range vals {
				up[i] = strings.ToUpper(v)
			}
			vals = up
		}
		if !compareOp(m.Op, method, want, vals) {
			return false, 0, "", ""
		}
		return true, specificityOf(MatchMethod), "", ""
	case MatchRegex:
		field := r.URL.Path
		if m.Field == "full" {
			field = strings.ToUpper(r.Method) + " " + r.URL.Path
		}
		re, err := regexp.Compile(m.Pattern)
		if err != nil {
			return false, 0, "", ""
		}
		if !re.MatchString(field) {
			return false, 0, "", ""
		}
		return true, specificityOf(MatchRegex), "", ""
	default:
		return false, 0, "", ""
	}
}

func specificityOf(kind string) int {
	switch kind {
	case MatchHeader:
		return 5
	case MatchPathAttr:
		return 4
	case MatchPath:
		return 3
	case MatchRegex:
		return 2
	case MatchMethod:
		return 1
	default:
		return 0
	}
}

func compareOp(op, got, value string, values []string) bool {
	switch op {
	case "eq":
		return got == value
	case "in":
		for _, v := range values {
			if got == v {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func headerValue(r *http.Request, name string) string {
	if name == "" {
		return ""
	}
	// Canonical lookup first, then case-insensitive scan.
	if v := r.Header.Get(name); v != "" {
		return v
	}
	want := strings.ToLower(name)
	for k, vals := range r.Header {
		if strings.ToLower(k) == want && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

func buildSubject(r *http.Request, hit matchHit) string {
	if hit.headerSub != "" {
		// Prefer the header name from the matching header rule.
		name := firstHeaderRuleName(hit.ns.Rules)
		if name == "" {
			name = "header"
		}
		return "hdr:" + name + ":" + hit.headerSub
	}
	if hit.pathAttrSub != "" {
		return "tenant:" + hit.pathAttrSub
	}
	return "path:" + r.URL.Path
}

func firstHeaderRuleName(rules []Rule) string {
	for _, rule := range rules {
		if rule.Match.Kind == MatchHeader && rule.Match.Name != "" {
			return rule.Match.Name
		}
	}
	return ""
}
