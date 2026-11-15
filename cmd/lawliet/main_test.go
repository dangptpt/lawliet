package main

import (
	"reflect"
	"testing"

	"github.com/tranbaohieu/lawliet/internal/snapshot"
)

func TestSaveRules(t *testing.T) {
	rules, err := saveRules(nil)
	if err != nil || !reflect.DeepEqual(rules, snapshot.DefaultRules) {
		t.Fatalf("no --save: want defaults, got %v %v", rules, err)
	}
	if rules, err := saveRules([]string{""}); err != nil || rules != nil {
		t.Fatalf(`--save "": want no rules, got %v %v`, rules, err)
	}
	rules, err = saveRules([]string{"60 10", "5 1"})
	if err != nil || !reflect.DeepEqual(rules, []snapshot.Rule{{Seconds: 60, Changes: 10}, {Seconds: 5, Changes: 1}}) {
		t.Fatalf("custom rules: %v %v", rules, err)
	}
	if _, err := saveRules([]string{"60"}); err == nil {
		t.Fatal("invalid rule should fail")
	}
}
