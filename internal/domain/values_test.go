package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIdentityParsersKeepConceptsDistinct(t *testing.T) {
	record, err := ParseRecordID("40000000-0000-4000-8000-000000000001")
	if err != nil || record == "" {
		t.Fatalf("ParseRecordID() = %q, %v", record, err)
	}
	brain, err := ParseBrainID("brain.research")
	if err != nil || brain != "brain.research" {
		t.Fatalf("ParseBrainID() = %q, %v", brain, err)
	}
	service, err := ParseServiceID("service.pii-scan")
	if err != nil || service != "service.pii-scan" {
		t.Fatalf("ParseServiceID() = %q, %v", service, err)
	}
	for name, parse := range map[string]func() error{
		"record":  func() error { _, err := ParseRecordID("brain.research"); return err },
		"brain":   func() error { _, err := ParseBrainID("service.research"); return err },
		"service": func() error { _, err := ParseServiceID("brain.research"); return err },
	} {
		if err := parse(); err == nil {
			t.Errorf("%s parser accepted another identity kind", name)
		}
	}
}

func TestBoundaryValueParsersNormalizeAndValidate(t *testing.T) {
	key, err := ParseObjectKey(`\research\notes.md`)
	if err != nil || key != "research/notes.md" {
		t.Fatalf("ParseObjectKey() = %q, %v", key, err)
	}
	path, err := ParseQueryPath("/research/notes")
	if err != nil || path != "/research/notes" {
		t.Fatalf("ParseQueryPath() = %q, %v", path, err)
	}
	idempotencyKey, err := ParseIdempotencyKey("  command-123  ")
	if err != nil || idempotencyKey != "command-123" {
		t.Fatalf("ParseIdempotencyKey() = %q, %v", idempotencyKey, err)
	}
	for _, invalid := range []string{"", "../notes", "a//b", "a/\x00b"} {
		if _, err := ParseObjectKey(invalid); err == nil {
			t.Errorf("ParseObjectKey(%q) succeeded", invalid)
		}
	}
	for _, invalid := range []string{"research", "/api/private", "/a//b", "/a/../b"} {
		if _, err := ParseQueryPath(invalid); err == nil {
			t.Errorf("ParseQueryPath(%q) succeeded", invalid)
		}
	}
}

func TestNamedValuesPreserveJSONRepresentation(t *testing.T) {
	value := struct {
		Brain     BrainID        `json:"brain"`
		Service   ServiceID      `json:"service"`
		ObjectKey ObjectKey      `json:"object_key"`
		Path      QueryPathValue `json:"path"`
	}{
		Brain: "brain.maya", Service: "service.notion",
		ObjectKey: "notes/a.md", Path: "/research",
	}
	got, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"brain":"brain.maya","service":"service.notion","object_key":"notes/a.md","path":"/research"}`
	if string(got) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestNamedExecutionTransitions(t *testing.T) {
	started := time.Unix(10, 0).UTC()
	completed := time.Unix(20, 0).UTC()
	if transition := BeginExecutionRead(started); transition.State() != ExecutionStateReading || !transition.StartedAt().Equal(started) {
		t.Fatalf("read transition = %#v", transition)
	}
	delivered := DeliverExecution(json.RawMessage(`{"rows":1}`), started, completed)
	if delivered.State() != ExecutionStateDelivered || string(delivered.ResultMetadata()) != `{"rows":1}` || !delivered.CompletedAt().Equal(completed) {
		t.Fatalf("delivered transition = %#v", delivered)
	}
	failed := FailExecution(CodeAssetUnavailable, "unavailable", completed)
	if failed.State() != ExecutionStateFailed || failed.ErrorCode() == nil || *failed.ErrorCode() != CodeAssetUnavailable {
		t.Fatalf("failed transition = %#v", failed)
	}
}
