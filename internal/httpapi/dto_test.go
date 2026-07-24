package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"secure-brain/internal/application"
	"secure-brain/internal/domain"
	"secure-brain/internal/routes"
)

func TestTypedDTOsPreserveLegacyJSON(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	user := domain.User{ID: "user-1", Handle: "maya", DisplayName: "Maya", CreatedAt: now}
	asset := domain.Asset{
		ID: "asset-1", BrainID: "brain-1", ObjectKey: "notes.txt",
		OriginalFilename: "notes.txt", MediaType: "text/plain", ByteSize: 5,
		SHA256: "abc", Format: domain.AssetFormatText,
		ProcessingState: domain.AssetStateReady, CreatedAt: now, UpdatedAt: now,
	}
	transfer := domain.Transfer{
		ID: "transfer-1", ExecutionID: "execution-1",
		SourceCanonicalID: "brain.maya", DestinationCanonicalID: "brain.atlas",
		Status: domain.TransferStatusPending, SuggestedObjectKey: "inbox/notes.txt",
		SuggestedFilename: "notes.txt", MediaType: "text/plain", ByteSize: 5,
		SHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	text := "hello"
	resultMetadata := application.ExecutionResultSnapshot{
		MediaType: "text/plain", ByteSize: 5, SuggestedFilename: "notes.txt", SHA256: "abc",
	}

	tests := []struct {
		name   string
		typed  any
		legacy any
	}{
		{
			name: "session",
			typed: sessionDTO{
				User: userResponse(user), MockAuth: true,
				Disclosure: "Local demo authentication only.",
			},
			legacy: map[string]any{
				"user": user, "mock_auth": true,
				"disclosure": "Local demo authentication only.",
			},
		},
		{
			name:  "asset",
			typed: assetResponse(asset), legacy: asset,
		},
		{
			name: "query path validation",
			typed: queryPathValidationDTO{
				Valid: false,
				Fields: fieldErrorListResponse([]routes.FieldError{{
					Field: "route", Code: "required", Message: "A route is required.",
				}}),
			},
			legacy: map[string]any{
				"valid": false,
				"fields": []routes.FieldError{{
					Field: "route", Code: "required", Message: "A route is required.",
				}},
			},
		},
		{
			name: "transfer detail",
			typed: transferDetailDTO{
				Transfer: transferResponse(transfer),
				Preview:  &transferPreviewDTO{Truncated: false, Text: &text},
			},
			legacy: map[string]any{
				"transfer": transfer,
				"preview":  map[string]any{"truncated": false, "text": "hello"},
			},
		},
		{
			name: "transfer resolution",
			typed: transferResolutionResponse(application.TransferResolutionResult{
				TransferID: transfer.ID, Status: domain.TransferStatusAccepted, Asset: &asset,
			}),
			legacy: map[string]any{
				"transfer_id": transfer.ID, "status": "accepted", "asset": asset,
			},
		},
		{
			name: "pull result",
			typed: routeExecutionResultResponse(application.RouteExecutionResult{
				ExecutionID: "execution-1", RouteID: "route-1", Source: "brain.maya",
				SourcePath: "/notes", Destination: "brain.maya", Outcome: "delivered",
				Result: &resultMetadata, Text: &text,
			}),
			legacy: map[string]any{
				"execution_id": "execution-1", "route_id": "route-1",
				"source": "brain.maya", "source_path": "/notes",
				"destination": "brain.maya", "outcome": "delivered",
				"result": map[string]any{
					"media_type": "text/plain", "byte_size": 5,
					"suggested_filename": "notes.txt", "sha256": "abc",
				},
				"text": "hello",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.typed)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(test.legacy)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("typed JSON changed the response bytes:\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestSnapshotPayloadCopiesMutableInput(t *testing.T) {
	payload := domain.Payload{
		Bytes: []byte("hello"),
		Metadata: domain.PayloadMetadata{
			"operation": "raw_read",
		},
	}
	snapshot := application.SnapshotPayload(payload)
	payload.Bytes[0] = 'j'
	payload.Metadata["operation"] = "csv_query"

	if string(snapshot.Bytes) != "hello" || snapshot.Metadata["operation"] != "raw_read" {
		t.Fatalf("snapshot changed with source payload: %#v", snapshot)
	}
}
