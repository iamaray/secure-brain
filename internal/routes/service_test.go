package routes

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
)

var routeTestClock = application.ClockFunc(func() time.Time { return time.Time{} })

type sequenceClock struct {
	values []time.Time
	next   int
}

func (clock *sequenceClock) Now() time.Time {
	value := clock.values[clock.next]
	clock.next++
	return value
}

func TestIdentityServiceExecutorReturnsExactPayload(t *testing.T) {
	bytes := []byte("exact payload")
	metadata := map[string]any{"ordered": []string{"a", "b"}}
	in := domain.Payload{Bytes: bytes, MediaType: "application/custom", SuggestedFilename: "x.bin", Metadata: metadata}
	out, err := (IdentityServiceExecutor{}).Execute(context.Background(), domain.Service{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) || &out.Bytes[0] != &in.Bytes[0] {
		t.Fatalf("identity transform changed or copied payload: %#v", out)
	}
	out.Metadata["same-map"] = true
	if in.Metadata["same-map"] != true {
		t.Fatal("identity transform copied the metadata map")
	}
}

type recordingExecutor struct {
	calls []string
	fn    func(int, domain.Payload) (domain.Payload, error)
}

func (e *recordingExecutor) Execute(_ context.Context, service domain.Service, in domain.Payload) (domain.Payload, error) {
	e.calls = append(e.calls, string(service.CanonicalID))
	if e.fn == nil {
		return in, nil
	}
	return e.fn(len(e.calls)-1, in)
}

func TestExecuteHopsRecordsRepeatedServicesInOrder(t *testing.T) {
	services := []domain.Service{
		{ID: "one-id", CanonicalID: "service.one"},
		{ID: "two-id", CanonicalID: "service.two"},
		{ID: "one-id", CanonicalID: "service.one"},
	}
	executor := &recordingExecutor{}
	in := domain.Payload{Bytes: []byte{0, 1, 2}, Metadata: map[string]any{"x": 1}}
	out, hops, err := ExecuteHops(context.Background(), executor, services, in, application.Limits{}, routeTestClock)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("payload changed: %#v", out)
	}
	if !reflect.DeepEqual(executor.calls, []string{"service.one", "service.two", "service.one"}) {
		t.Fatalf("calls = %#v", executor.calls)
	}
	if len(hops) != 3 {
		t.Fatalf("hop count = %d", len(hops))
	}
	for i, hop := range hops {
		if hop.HopIndex != i || hop.Status != domain.HopStatusCompleted || hop.InputSHA256 != hop.OutputSHA256 || hop.ErrorCode != nil {
			t.Fatalf("hop %d = %#v", i, hop)
		}
	}
}

func TestExecuteHopsUsesInjectedClock(t *testing.T) {
	started := time.Unix(10, 0)
	clock := &sequenceClock{values: []time.Time{started, started.Add(7 * time.Millisecond)}}
	_, hops, err := ExecuteHops(
		context.Background(),
		IdentityServiceExecutor{},
		[]domain.Service{{CanonicalID: "service.one"}},
		domain.Payload{Bytes: []byte("payload")},
		application.Limits{},
		clock,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := hops[0].DurationMS; got != 7 {
		t.Fatalf("duration = %dms, want 7ms", got)
	}
}

func TestExecuteHopsDetectsMutationAndStops(t *testing.T) {
	executor := &recordingExecutor{fn: func(index int, in domain.Payload) (domain.Payload, error) {
		if index == 1 {
			out := in
			out.Bytes = append([]byte(nil), in.Bytes...)
			out.Bytes[0]++
			return out, nil
		}
		return in, nil
	}}
	services := []domain.Service{{CanonicalID: "service.one"}, {CanonicalID: "service.bad"}, {CanonicalID: "service.never"}}
	out, hops, err := ExecuteHops(context.Background(), executor, services, domain.Payload{Bytes: []byte("abc")}, application.Limits{}, routeTestClock)
	if out.Bytes != nil || !IsServiceHopFailure(err) || routeErrorCode(t, err) != domain.CodeServiceHopFailed {
		t.Fatalf("unexpected failure: out=%#v err=%v", out, err)
	}
	if len(hops) != 2 || hops[1].Status != domain.HopStatusFailed || hops[1].InputSHA256 == hops[1].OutputSHA256 || len(executor.calls) != 2 {
		t.Fatalf("unexpected hop failure trace: hops=%#v calls=%#v", hops, executor.calls)
	}
}

func TestExecuteHopsDetectsEnvelopeAndInPlaceMetadataMutation(t *testing.T) {
	tests := []struct {
		name string
		fn   func(domain.Payload) domain.Payload
	}{
		{"media type", func(in domain.Payload) domain.Payload { in.MediaType = "application/changed"; return in }},
		{"suggested filename", func(in domain.Payload) domain.Payload { in.SuggestedFilename = "changed.txt"; return in }},
		{"in-place metadata", func(in domain.Payload) domain.Payload { in.Metadata["role"] = "changed"; return in }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingExecutor{fn: func(_ int, in domain.Payload) (domain.Payload, error) { return tt.fn(in), nil }}
			input := domain.Payload{Bytes: []byte("same"), MediaType: "text/plain", SuggestedFilename: "x.txt", Metadata: map[string]any{"role": "original"}}
			_, hops, err := ExecuteHops(context.Background(), executor, []domain.Service{{CanonicalID: "service.one"}}, input, application.Limits{}, routeTestClock)
			if !IsServiceHopFailure(err) || len(hops) != 1 || hops[0].Status != domain.HopStatusFailed || hops[0].InputSHA256 != hops[0].OutputSHA256 {
				t.Fatalf("envelope mutation was not rejected: hops=%#v err=%v", hops, err)
			}
		})
	}
}

func TestExecuteHopsWrapsExecutorFailureAndStops(t *testing.T) {
	dependencyErr := errors.New("offline")
	executor := &recordingExecutor{fn: func(_ int, _ domain.Payload) (domain.Payload, error) { return domain.Payload{}, dependencyErr }}
	out, hops, err := ExecuteHops(context.Background(), executor, []domain.Service{{CanonicalID: "service.one"}, {CanonicalID: "service.two"}}, domain.Payload{Bytes: []byte("abc")}, application.Limits{}, routeTestClock)
	if out.Bytes != nil || !errors.Is(err, dependencyErr) || !IsServiceHopFailure(err) {
		t.Fatalf("unexpected error chain: out=%#v err=%v", out, err)
	}
	if len(hops) != 1 || hops[0].Status != domain.HopStatusFailed || hops[0].InputSHA256 == hops[0].OutputSHA256 {
		t.Fatalf("unexpected failed record: %#v", hops)
	}
}

func TestExecuteHopsHonorsCancellationAndZeroHops(t *testing.T) {
	in := domain.Payload{Bytes: []byte("unchanged")}
	out, hops, err := ExecuteHops(context.Background(), nil, nil, in, application.Limits{}, routeTestClock)
	if err != nil || len(hops) != 0 || !reflect.DeepEqual(out, in) {
		t.Fatalf("zero-hop route failed: out=%#v hops=%#v err=%v", out, hops, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, hops, err = ExecuteHops(ctx, IdentityServiceExecutor{}, []domain.Service{{CanonicalID: "service.one"}}, in, application.Limits{}, routeTestClock)
	if len(hops) != 0 || !IsServiceHopFailure(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not honored: hops=%#v err=%v", hops, err)
	}
}

func TestExecuteHopsRejectsMoreThanSQLHopLimit(t *testing.T) {
	services := make([]domain.Service, application.MaxRouteHops+1)
	_, hops, err := ExecuteHops(context.Background(), IdentityServiceExecutor{}, services, domain.Payload{Bytes: []byte("x")}, application.Limits{}, routeTestClock)
	if len(hops) != 0 || routeErrorCode(t, err) != domain.CodeRouteTooLong {
		t.Fatalf("unexpected over-limit result: hops=%#v err=%v", hops, err)
	}
}
