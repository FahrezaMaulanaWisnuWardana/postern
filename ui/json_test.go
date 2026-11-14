package ui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/widget"
)

func TestJSONFoldNestedObjectsAndArrays(t *testing.T) {
	source := `{"status":"ok","checks":{"database":"ok","items":[1,{"redis":"ok"}]},"id":9007199254740993}`
	var v jsonView
	v.update(source, false)
	if !v.valid || !v.tree {
		t.Fatal("response JSON should default to the tree")
	}
	all := len(v.visibleLines())
	checks := -1
	for i, line := range v.lines {
		if strings.Contains(line.text, `"checks"`) {
			checks = i
		}
	}
	if checks < 0 || v.lines[checks].end < 0 {
		t.Fatal("nested object has no matching closing line")
	}
	v.collapsed[checks] = true
	if len(v.visibleLines()) >= all || !strings.Contains(v.lineText(checks), "{ … },") {
		t.Fatal("collapsing should hide children and preserve closing punctuation")
	}
	v.collapsed[0] = true
	if len(v.visibleLines()) != 1 {
		t.Fatal("collapsed root should occupy one line")
	}
	v.collapsed = map[int]bool{}
	if len(v.visibleLines()) != all || v.source != source {
		t.Fatal("expansion changed the source or lost lines")
	}
	var text []string
	for _, line := range v.lines {
		text = append(text, line.text)
	}
	if !strings.Contains(strings.Join(text, "\n"), "9007199254740993") {
		t.Fatal("formatting lost integer precision")
	}
}

func TestJSONViewCacheAndInvalidInput(t *testing.T) {
	var v jsonView
	v.update(`{"items":[{},[],{"text":"braces { [ } ]"}]}`, true)
	if !v.valid || v.tree {
		t.Fatal("request JSON should start in editable mode")
	}
	v.collapsed[0] = true
	v.update(v.source, true)
	if !v.collapsed[0] {
		t.Fatal("unchanged input should retain folding")
	}
	v.update(`{"new":true}`, true)
	if len(v.collapsed) != 0 {
		t.Fatal("changed input should reset stale folds")
	}
	for _, source := range []string{"", "not JSON", `{"unfinished":`, `{"a":1} {"b":2}`} {
		v.update(source, false)
		if v.valid {
			t.Fatalf("invalid input accepted: %q", source)
		}
	}
}

func TestJSONFoldingNeverChangesEditor(t *testing.T) {
	a := &App{th: newTheme(), ic: loadIcons()}
	source := `{"checks":{"database":"ok","redis":"ok"}}`
	for _, editable := range []bool{true, false} {
		var ed widget.Editor
		ed.ReadOnly = !editable
		ed.SetText(source)
		var v jsonView
		v.update(source, editable)
		v.tree = true
		v.collapsed[0] = true
		gtx := testContext(image.Pt(600, 300), 1)
		a.layoutJSONBody(gtx, &v, &ed, editable)
		if ed.Text() != source {
			t.Fatal("folding modified the underlying JSON")
		}
	}
}
