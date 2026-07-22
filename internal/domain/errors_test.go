package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorWrapSupportsErrorsIsAndAs(t *testing.T) {
	cause := errors.New("dependency failed")
	err := WrapError(CodeStorageProviderError, "Storage is unavailable.", cause)
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not discoverable")
	}
	var appErr *Error
	if !errors.As(fmt.Errorf("operation: %w", err), &appErr) || appErr.Code != CodeStorageProviderError {
		t.Fatalf("typed error is not discoverable: %#v", appErr)
	}
}

func TestErrorJSONExcludesCause(t *testing.T) {
	err := &Error{
		Code:    CodeRouteInvalid,
		Message: "The route is invalid.",
		Cause:   errors.New("database secret"),
		Details: map[string]any{"fields": []string{"route"}},
	}
	body, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(body), "database secret") || !strings.Contains(string(body), `"code":"ROUTE_INVALID"`) {
		t.Fatalf("unexpected JSON: %s", body)
	}
}
