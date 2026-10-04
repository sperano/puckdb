package draftrecommend

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestStoredRunAcceptsResolvedDefaultScenarioAndReplays(t *testing.T) {
	input := recommendationInput(t)
	result, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStoredRun(input, result); err != nil {
		t.Fatalf("validate stored run: %v", err)
	}
	inputRaw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	resultRaw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeStoredRun(uuid.New(), inputRaw, resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	if run.Result.Scenario != result.Scenario || run.Input.Session.Version != input.Session.Version {
		t.Fatalf("decoded run lost versions: %+v", run)
	}
}

func TestStoredRunRejectsVersionMismatch(t *testing.T) {
	input := recommendationInput(t)
	result, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	result.RulesVersion = "different"
	if err := validateStoredRun(input, result); err == nil {
		t.Fatal("expected source version mismatch")
	}
}

func TestDatabaseVersionRejectsOverflow(t *testing.T) {
	if _, err := databaseVersion(maxDatabaseVersion + 1); err == nil {
		t.Fatal("expected PostgreSQL bigint overflow error")
	}
}
