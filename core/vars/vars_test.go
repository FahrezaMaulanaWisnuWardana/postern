package vars

import (
	"strings"
	"testing"
)

func lookup(m map[string]string) Lookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestInterpolate(t *testing.T) {
	l := lookup(map[string]string{"host": "example.com", "url": "https://{{host}}/v1", "empty": ""})
	cases := map[string]string{
		"{{host}}":            "example.com",
		"{{ host }}":          "example.com",
		"https://{{host}}/a":  "https://example.com/a",
		"{{url}}/users":       "https://example.com/v1/users",
		"{{missing}}":         "{{missing}}",
		"{{empty}}":           "",
		"no variables here":   "no variables here",
		"{{host}}{{host}}":    "example.comexample.com",
		"pre {{ host }} post": "pre example.com post",
	}
	for in, want := range cases {
		if got := Interpolate(in, l); got != want {
			t.Errorf("Interpolate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNestedAndRecursiveLimit(t *testing.T) {
	l := lookup(map[string]string{
		"a": "{{b}}",
		"b": "{{c}}",
		"c": "end",
	})
	if got := Interpolate("{{a}}", l); got != "end" {
		t.Fatalf("nested interpolation = %q", got)
	}
	loop := lookup(map[string]string{"x": "{{y}}", "y": "{{x}}"})
	got := Interpolate("{{x}}", loop)
	if got == "" || strings.Contains(got, "{{x}}{{y}}") {
		t.Fatalf("recursive interpolation should stop, got %q", got)
	}
}

func TestDynamicValues(t *testing.T) {
	l := lookup(nil)
	if got := Interpolate("{{$guid}}", l); len(got) != 36 || strings.Contains(got, "{{") {
		t.Fatalf("guid = %q", got)
	}
	if got := Interpolate("{{$randomInt}}", l); got == "" || strings.Contains(got, "{{") {
		t.Fatalf("randomInt = %q", got)
	}
	if got := Interpolate("{{$randomBoolean}}", l); got != "true" && got != "false" {
		t.Fatalf("randomBoolean = %q", got)
	}
	if got := Interpolate("{{$unknown}}", l); got != "{{$unknown}}" {
		t.Fatalf("unknown dynamic = %q", got)
	}
}
