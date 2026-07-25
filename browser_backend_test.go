package main

import (
	"fmt"
	"testing"
)

func TestBrowserProtectedOriginsIncludeAPIAndDevelopmentFrontend(t *testing.T) {
	got := browserProtectedOrigins("0.0.0.0:18080", 15173)
	want := []string{
		"http://127.0.0.1:18080", "http://localhost:18080", "http://[::1]:18080",
		"http://127.0.0.1:15173", "http://localhost:15173", "http://[::1]:15173",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("browserProtectedOrigins() = %#v, want %#v", got, want)
	}
}
