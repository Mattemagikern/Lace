package files

import (
	"fmt"
	"path"
	"strings"
)

type rule struct {
	pat      string
	anchored bool
	dirOnly  bool
}

type Matcher struct {
	rules []rule
}

func parseRule(pat string) (rule, error) {
	r := rule{pat: pat}
	if strings.HasSuffix(r.pat, "/") {
		r.dirOnly = true
		r.pat = strings.TrimSuffix(r.pat, "/")
	}
	if strings.HasPrefix(r.pat, "/") {
		r.anchored = true
		r.pat = strings.TrimPrefix(r.pat, "/")
	}
	if r.pat == "" {
		return r, fmt.Errorf("empty ignore pattern: %q", pat)
	}
	if strings.Contains(r.pat, "/") {
		r.anchored = true
	}
	if _, err := path.Match(r.pat, ""); err != nil {
		return r, fmt.Errorf("invalid ignore pattern %q: %w", pat, err)
	}
	return r, nil
}

func NewMatcher(pats []string) (*Matcher, error) {
	if len(pats) == 0 {
		return nil, nil
	}
	m := &Matcher{rules: make([]rule, 0, len(pats))}
	for _, p := range pats {
		r, err := parseRule(p)
		if err != nil {
			return nil, err
		}
		m.rules = append(m.rules, r)
	}
	return m, nil
}

func (m *Matcher) Match(rel string, isDir bool) bool {
	if m == nil || rel == "." {
		return false
	}
	rel = path.Clean(rel)
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		name := rel
		if !r.anchored {
			name = path.Base(rel)
		}
		if ok, _ := path.Match(r.pat, name); ok {
			return true
		}
	}
	return false
}
