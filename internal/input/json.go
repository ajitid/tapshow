package input

import (
	"encoding/json"
	"time"
)

const wireReadyType = "ready"

type WireReadyMessage struct {
	Type string `json:"type"`
}

type WireKeyEvent struct {
	Code      uint16   `json:"code"`
	Name      string   `json:"name"`
	State     KeyState `json:"state"`
	Timestamp int64    `json:"timestamp_unix_ms"`
}

func ToWire(ev KeyEvent) WireKeyEvent {
	return WireKeyEvent{Code: ev.Code, Name: ev.Name, State: ev.State, Timestamp: ev.Timestamp.UnixMilli()}
}

func FromWire(ev WireKeyEvent) KeyEvent {
	return KeyEvent{Code: ev.Code, Name: ev.Name, State: ev.State, Timestamp: time.UnixMilli(ev.Timestamp)}
}

func MarshalReady() ([]byte, error) {
	return json.Marshal(WireReadyMessage{Type: wireReadyType})
}

func IsReady(line []byte) (bool, error) {
	var wire WireReadyMessage
	if err := json.Unmarshal(line, &wire); err != nil {
		return false, err
	}
	return wire.Type == wireReadyType, nil
}

func MarshalEvent(ev KeyEvent) ([]byte, error) {
	return json.Marshal(ToWire(ev))
}

func UnmarshalEvent(line []byte) (KeyEvent, error) {
	var wire WireKeyEvent
	if err := json.Unmarshal(line, &wire); err != nil {
		return KeyEvent{}, err
	}
	return FromWire(wire), nil
}
