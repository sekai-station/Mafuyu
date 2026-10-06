package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOptionalRoomSource(t *testing.T) {
	for _, field := range []string{"", `,"source":""`, `,"source":null`, `,"source":"qq"`} {
		var item UDSRoomItem
		if err := json.Unmarshal([]byte(`{"id":"12345","info":{"tid":"1"}`+field+`}`), &item); err != nil {
			t.Fatal(err)
		}
		item.Time = time.Now().Unix()
		room, err := RoomFromItem(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateRoom(room); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(room)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]any
		if err := json.Unmarshal(encoded, &output); err != nil {
			t.Fatal(err)
		}
		if output["source"] != item.Source {
			t.Fatalf("source changed in response: %s", encoded)
		}
	}
}
