package model

import (
	"encoding/json"
	"testing"
)

func TestQQGroupRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		want  string
	}{
		{"legacy", "", "null"},
		{"null", `,"group":null`, "null"},
		{"number", `,"group":3333333`, "3333333"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, err := RoomFromItem(UDSRoomItem{Info: json.RawMessage(`{"qq":123456,"nickname":"Player","avatar":""` + tc.field + `}`)})
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(room.Info)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["group"]) != tc.want {
				t.Fatalf("group lost in round trip: %s", data)
			}
		})
	}
}
