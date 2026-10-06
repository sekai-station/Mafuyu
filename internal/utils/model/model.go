// Package model defines the domain types and wire-format structs shared across
// the entire application. It has no dependencies on other internal packages.
package model

import (
	"encoding/json"
	"fmt"
)

// Room is the canonical in-memory representation of a game room.
// Info holds either XInfo or QQInfo depending on the data source.
type Room struct {
	Time   int64  `json:"time"`
	ID     string `json:"id"`
	Msg    string `json:"msg"`
	Name   string `json:"name"`
	Source string `json:"source"` // optional on input; missing/null normalize to ""
	Info   any    `json:"info"`   // union[XInfo, QQInfo]
}

type XInfo struct {
	TID        string `json:"tid"`
	UserName   string `json:"userName"`
	ScreenName string `json:"screenName"`
	Avatar     string `json:"avatar"`
}

// MarshalJSON adds a "type" discriminator so clients can tell the Info union
// members apart without probing for fields. ParseInfo ignores it on input.
func (x XInfo) MarshalJSON() ([]byte, error) {
	type fields XInfo
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"XInfo", fields(x)})
}

type QQInfo struct {
	QQ       *int64 `json:"qq"`    // nullable
	Group    *int64 `json:"group"` // nullable
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// MarshalJSON adds a "type" discriminator; see XInfo.MarshalJSON.
func (q QQInfo) MarshalJSON() ([]byte, error) {
	type fields QQInfo
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"QQInfo", fields(q)})
}

// RoomSkills carries numeric skill data attached to an existing room, delivered
// via UDS after the initial room submission (e.g. score arrays).
type RoomSkills struct {
	ID   string `json:"id"`
	Time int64  `json:"time"`
	Data []int  `json:"data"`
}

// UDSMessage is the top-level envelope read from the Unix domain socket.
// Type defaults to "room" when absent; Data is type-specific payload.
type UDSMessage struct {
	Type    string          `json:"type,omitempty"`
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// UDSRoomItem is a single room within a UDS "room" message's data array.
// Info is kept as raw JSON until ParseInfo resolves it to XInfo or QQInfo.
type UDSRoomItem struct {
	Time   int64           `json:"time"`
	ID     string          `json:"id"`
	Msg    string          `json:"msg"`
	Name   string          `json:"name"`
	Source string          `json:"source"` // optional; missing/null normalize to ""
	Info   json.RawMessage `json:"info"`
}

// SubmitRequest is the body of a v2 HTTP POST /submit request.
type SubmitRequest struct {
	Data []UDSRoomItem `json:"data"`
}

// V1SubmitRequest is the body of a legacy v1 HTTP POST /station/api/v1/ request.
// Field names follow the original Python client convention.
type V1SubmitRequest struct {
	CreateTime    float64 `json:"create_time"`
	Username      string  `json:"username"`
	UserSendScene string  `json:"user_send_scene"`
	UserGameID    string  `json:"user_game_id"`
	RoomID        string  `json:"room_id"`
	Description   string  `json:"description"`
}

// Envelope is the standard v2 JSON response wrapper: {"code":200,"data":{...}}.
// Error is omitted on success.
type Envelope struct {
	Code  int    `json:"code"`
	Data  any    `json:"data"`
	Error string `json:"error,omitempty"`
}

// V1Message is the legacy WebSocket / v1-HTTP response envelope used by the
// original Python server: {"status":"success","action":"...","response":{...}}.
type V1Message struct {
	Status   string `json:"status"`
	Action   string `json:"action"`
	Response any    `json:"response"`
}

// Statistic is returned by GET /station/api/v2/statistic.
type Statistic struct {
	Online  int `json:"online"`
	Past15m int `json:"past15m"`
}

// PastCount groups room submission counts for the last 15 min, 1 h, and 24 h.
type PastCount struct {
	Past15m int `json:"past15m"`
	Past1h  int `json:"past1h"`
	Past24h int `json:"past24h"`
}

// ChannelHealth describes per-minute submission counts for one channel over the
// last 60 minutes. Tick[0] is the oldest minute.
type ChannelHealth struct {
	Name string `json:"name"`
	Tick []int  `json:"tick"`
}

// ParseInfo detects the info type from raw JSON by probing for discriminating
// keys: "tid" → XInfo, "qq" → QQInfo. Returns the typed value and its name.
func ParseInfo(raw json.RawMessage) (any, string, error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("empty info")
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, "", fmt.Errorf("parse info: %w", err)
	}

	if _, ok := probe["tid"]; ok {
		var info XInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			return nil, "", fmt.Errorf("parse XInfo: %w", err)
		}
		return info, "XInfo", nil
	}

	if _, ok := probe["qq"]; ok {
		var info QQInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			return nil, "", fmt.Errorf("parse QQInfo: %w", err)
		}
		return info, "QQInfo", nil
	}

	return nil, "", fmt.Errorf("unknown info type")
}

// RoomFromItem converts a UDSRoomItem (with raw JSON info) into a Room by
// calling ParseInfo to resolve the typed Info value.
func RoomFromItem(item UDSRoomItem) (*Room, error) {
	info, _, err := ParseInfo(item.Info)
	if err != nil {
		return nil, err
	}
	return &Room{
		Time:   item.Time,
		ID:     item.ID,
		Msg:    item.Msg,
		Name:   item.Name,
		Source: item.Source,
		Info:   info,
	}, nil
}

// RoomFromV1Submit maps legacy v1 field names to Room fields.
// create_time → Time, room_id → ID, description → Msg,
// username → Name, user_send_scene → Source. Info is left empty.
func RoomFromV1Submit(req V1SubmitRequest) *Room {
	return &Room{
		Time:   int64(req.CreateTime),
		ID:     req.RoomID,
		Msg:    req.Description,
		Name:   req.Username,
		Source: req.UserSendScene,
		Info:   XInfo{},
	}
}
