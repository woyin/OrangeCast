package server

import "testing"

func TestLineDiff(t *testing.T) {
	got := lineDiff("keep\nremove\nlast", "keep\nadd\nlast\nmore")
	want := []revisionDiffLine{{"unchanged", "keep"}, {"removed", "remove"}, {"added", "add"}, {"unchanged", "last"}, {"added", "more"}}
	if len(got) != len(want) {
		t.Fatalf("diff=%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("diff[%d]=%#v", i, got[i])
		}
	}
	if got := lineDiff("", "add"); len(got) != 1 || got[0].Kind != "added" {
		t.Fatalf("empty before=%#v", got)
	}
	if got := lineDiff("remove", ""); len(got) != 1 || got[0].Kind != "removed" {
		t.Fatalf("empty after=%#v", got)
	}
}
