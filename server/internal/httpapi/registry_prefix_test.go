package httpapi

import (
	"net/http"
	"testing"
)

func TestPrefixLookupUsesLongestMatchAndExactPrecedence(t *testing.T) {
	reg := NewRegistry()
	short := func(*Ctx) error { return nil }
	long := func(*Ctx) error { return nil }
	exact := func(*Ctx) error { return nil }
	if !reg.HandlePrefixWithStatus(http.MethodPost, "/quests/", "short", ModelProbed, short) {
		t.Fatal("short prefix was not registered")
	}
	if !reg.HandlePrefixWithStatus(http.MethodPost, "/quests/execute/", "long", ModelProbed, long) {
		t.Fatal("long prefix was not registered")
	}
	if !reg.HandleWithStatus(http.MethodPost, "/quests/execute/1.json", "exact", ModelConfirmed, exact) {
		t.Fatal("exact route was not registered")
	}

	route, ok := reg.Lookup(http.MethodPost, "/quests/execute/1.json")
	if !ok || route.Path != "/quests/execute/1.json" || route.Status != ModelConfirmed {
		t.Fatalf("exact lookup=%#v ok=%v", route, ok)
	}
	route, ok = reg.Lookup(http.MethodPost, "/quests/execute/2.json")
	if !ok || route.Path != "/quests/execute/" || route.Status != ModelProbed {
		t.Fatalf("long prefix lookup=%#v ok=%v", route, ok)
	}
	route, ok = reg.Lookup(http.MethodPost, "/quests/result/2.json")
	if !ok || route.Path != "/quests/" {
		t.Fatalf("short prefix lookup=%#v ok=%v", route, ok)
	}
	if _, ok := reg.Lookup(http.MethodGet, "/quests/execute/2.json"); ok {
		t.Fatal("wrong method unexpectedly matched prefix")
	}
}
