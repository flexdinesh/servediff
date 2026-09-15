package main

import (
	"reflect"
	"testing"
)

func TestNormalizeArgumentsAllowsFlagsAfterPath(t *testing.T) {
	actual := normalizeArguments([]string{".", "--port", "4000", "--no-browser"})
	expected := []string{"--port", "4000", "--no-browser", "."}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %v, want %v", actual, expected)
	}
}
