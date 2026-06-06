package input

import (
	"testing"
	"time"
)

func TestMarshalReady(t *testing.T) {
	line, err := MarshalReady()
	if err != nil {
		t.Fatalf("MarshalReady() error = %v", err)
	}

	ready, err := IsReady(line)
	if err != nil {
		t.Fatalf("IsReady() error = %v", err)
	}
	if !ready {
		t.Fatalf("IsReady() = false, want true")
	}
}

func TestMarshalUnmarshalEventRoundTrip(t *testing.T) {
	timestamp := time.Date(2026, 6, 6, 12, 34, 56, 789000000, time.UTC)
	want := KeyEvent{
		Code:      KEY_A,
		Name:      "A",
		State:     KeyPressed,
		Timestamp: timestamp,
	}

	line, err := MarshalEvent(want)
	if err != nil {
		t.Fatalf("MarshalEvent() error = %v", err)
	}

	ready, err := IsReady(line)
	if err != nil {
		t.Fatalf("IsReady() error = %v", err)
	}
	if ready {
		t.Fatalf("IsReady() = true, want false for event")
	}

	got, err := UnmarshalEvent(line)
	if err != nil {
		t.Fatalf("UnmarshalEvent() error = %v", err)
	}

	if got.Code != want.Code {
		t.Fatalf("Code = %d, want %d", got.Code, want.Code)
	}
	if got.Name != want.Name {
		t.Fatalf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.State != want.State {
		t.Fatalf("State = %d, want %d", got.State, want.State)
	}
	if !got.Timestamp.Equal(timestamp) {
		t.Fatalf("Timestamp = %s, want %s", got.Timestamp, timestamp)
	}
}
