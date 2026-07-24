package domain

import (
	"encoding/json"
	"time"
)

// ExecutionTransition is created only through the named transition functions
// below. Infrastructure can persist its values, but cannot invent a lifecycle
// target by assigning an arbitrary state string.
type ExecutionTransition struct {
	state          ExecutionState
	resultMetadata json.RawMessage
	errorCode      *Code
	errorMessage   *string
	startedAt      *time.Time
	completedAt    *time.Time
}

func BeginExecutionRead(startedAt time.Time) ExecutionTransition {
	return ExecutionTransition{state: ExecutionStateReading, startedAt: &startedAt}
}

func BeginExecutionProcessing(startedAt time.Time) ExecutionTransition {
	return ExecutionTransition{state: ExecutionStateProcessing, startedAt: &startedAt}
}

func DeliverExecution(resultMetadata json.RawMessage, startedAt, completedAt time.Time) ExecutionTransition {
	return ExecutionTransition{
		state:          ExecutionStateDelivered,
		resultMetadata: append(json.RawMessage(nil), resultMetadata...),
		startedAt:      &startedAt,
		completedAt:    &completedAt,
	}
}

func FailExecution(code Code, message string, completedAt time.Time) ExecutionTransition {
	return ExecutionTransition{
		state:        ExecutionStateFailed,
		errorCode:    &code,
		errorMessage: &message,
		completedAt:  &completedAt,
	}
}

// FailExecutionWithResult preserves a result snapshot for workflows that had
// already produced diagnostic metadata before failing.
func FailExecutionWithResult(code Code, message string, resultMetadata json.RawMessage, startedAt, completedAt time.Time) ExecutionTransition {
	transition := FailExecution(code, message, completedAt)
	transition.resultMetadata = append(json.RawMessage(nil), resultMetadata...)
	transition.startedAt = &startedAt
	return transition
}

func (t ExecutionTransition) State() ExecutionState { return t.state }

func (t ExecutionTransition) ResultMetadata() json.RawMessage {
	return append(json.RawMessage(nil), t.resultMetadata...)
}

func (t ExecutionTransition) ErrorCode() *Code {
	if t.errorCode == nil {
		return nil
	}
	value := *t.errorCode
	return &value
}

func (t ExecutionTransition) ErrorMessage() *string {
	if t.errorMessage == nil {
		return nil
	}
	value := *t.errorMessage
	return &value
}

func (t ExecutionTransition) StartedAt() *time.Time {
	if t.startedAt == nil {
		return nil
	}
	value := *t.startedAt
	return &value
}

func (t ExecutionTransition) CompletedAt() *time.Time {
	if t.completedAt == nil {
		return nil
	}
	value := *t.completedAt
	return &value
}
