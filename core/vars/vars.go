// Package vars implements {{variable}} interpolation with Postman-compatible
// scoping and dynamic values.
package vars

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"
)

const maxDepth = 8

var pattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// dynamic resolves built-in {{$name}} values.
func dynamic(name string) (string, bool) {
	switch name {
	case "$timestamp":
		return fmt.Sprintf("%d", time.Now().Unix()), true
	case "$isoTimestamp":
		return time.Now().UTC().Format(time.RFC3339), true
	case "$guid", "$randomUUID":
		return randomUUID(), true
	case "$randomInt":
		return fmt.Sprintf("%d", rand.Intn(1000)), true
	case "$randomBoolean":
		if rand.Intn(2) == 0 {
			return "false", true
		}
		return "true", true
	case "$randomFirstName":
		return pick([]string{"Ada", "Grace", "Linus", "Ken", "Barbara", "Alan", "Margaret", "Dennis"}), true
	case "$randomLastName":
		return pick([]string{"Lovelace", "Hopper", "Torvalds", "Thompson", "Liskov", "Turing", "Hamilton", "Ritchie"}), true
	case "$randomEmail":
		return strings.ToLower(pick([]string{"Ada", "Grace", "Linus", "Ken", "Alan"})) + "@example.com", true
	case "$randomWord":
		return pick([]string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}), true
	case "$randomColor":
		return pick([]string{"red", "green", "blue", "amber", "violet", "teal"}), true
	case "$randomPort":
		return fmt.Sprintf("%d", 1024+rand.Intn(60000)), true
	}
	return "", false
}

func pick(xs []string) string { return xs[rand.Intn(len(xs))] }

func randomUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Lookup resolves a variable name to a value.
type Lookup func(name string) (string, bool)

// Interpolate replaces all {{name}} occurrences using lookup, resolving
// nested references (a variable whose value contains another variable).
// Unknown variables are left untouched, matching Postman's behaviour.
func Interpolate(s string, lookup Lookup) string {
	return interpolate(s, lookup, 0)
}

func interpolate(s string, lookup Lookup, depth int) string {
	if depth >= maxDepth || !strings.Contains(s, "{{") {
		return s
	}
	return pattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := pattern.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		name := sub[1]
		if name == "" {
			return m
		}
		if strings.HasPrefix(name, "$") {
			if v, ok := dynamic(name); ok {
				return v
			}
		}
		if v, ok := lookup(name); ok {
			return interpolate(v, lookup, depth+1)
		}
		return m
	})
}

// MapLookup adapts a map to a Lookup.
func MapLookup(m map[string]string) Lookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// Merge builds the effective variable map. Later sources win, so callers
// should pass values in order of increasing precedence.
func Merge(sources ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, src := range sources {
		for k, v := range src {
			out[k] = v
		}
	}
	return out
}
