// Package v2 provides the encoding functions for v2 SSE event payloads.
//
// Payloads are plain JSON objects with the same shapes the REST endpoints
// return: a "room" event carries exactly what /recent returns for one room.
package v2

import (
	"encoding/json"
	"strconv"
	"strings"

	"mafuyu/internal/utils/model"
)

// RoomOptions controls how a room is encoded for clients.
type RoomOptions struct {
	HideAvatar bool // every avatar is null, inside info.extra too (config v2.send_avatar: false)
	Extra      bool // add info.extra with the full original info (?extra=1)
}

// publicRoom is a room as v2 clients see it. Info has the same fields whatever
// the platform the room came from; the stored XInfo / QQInfo is only exposed
// through info.extra.
type publicRoom struct {
	Time   int64      `json:"time"`
	ID     string     `json:"id"`
	Msg    string     `json:"msg"`
	Name   string     `json:"name"`
	Source string     `json:"source"`
	Info   publicInfo `json:"info"`
}

type publicInfo struct {
	Handle string          `json:"handle"`          // "@user" on X, nickname or "QQ:<number>" on QQ; may be empty
	URL    string          `json:"url"`             // the original post, empty when there is none (QQ)
	Avatar *string         `json:"avatar"`          // null when empty or hidden
	Extra  json.RawMessage `json:"extra,omitempty"` // full stored info, only with RoomOptions.Extra
}

// EncodeHeartbeat returns a heartbeat payload: {"time": <unix_millis>}.
func EncodeHeartbeat(t int64) []byte {
	return mustMarshal(struct {
		Time int64 `json:"time"`
	}{t})
}

// EncodeStatistic returns a statistic payload: {"online": n, "past15m": n}.
func EncodeStatistic(online, past15m int) []byte {
	return mustMarshal(model.Statistic{Online: online, Past15m: past15m})
}

// EncodeRoom returns a room payload, identical to one element of /recent.
func EncodeRoom(room *model.Room, opts RoomOptions) ([]byte, error) {
	pub, err := toPublic(room, opts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pub)
}

// encodeRooms returns the /recent payload. Rooms that cannot be encoded are
// skipped, as they are in the SSE stream.
func encodeRooms(rooms []*model.Room, opts RoomOptions) []publicRoom {
	out := make([]publicRoom, 0, len(rooms))
	for _, room := range rooms {
		if pub, err := toPublic(room, opts); err == nil {
			out = append(out, pub)
		}
	}
	return out
}

// EncodeRoomSkills returns a roomSkills payload: {"id", "time", "data"}.
func EncodeRoomSkills(skills *model.RoomSkills) []byte {
	return mustMarshal(skills)
}

func toPublic(room *model.Room, opts RoomOptions) (publicRoom, error) {
	info := publicInfo{}
	avatar := ""
	switch v := room.Info.(type) {
	case model.XInfo:
		user := strings.TrimLeft(strings.TrimSpace(v.UserName), "@")
		if user != "" {
			info.Handle = "@" + user
		} else {
			info.Handle = v.ScreenName
		}
		if v.TID != "" {
			if user == "" {
				user = "i" // x.com/i/status/<id> opens a post without knowing its author
			}
			info.URL = "https://x.com/" + user + "/status/" + v.TID
		}
		avatar = v.Avatar
	case model.QQInfo:
		info.Handle = v.Nickname
		if info.Handle == "" && v.QQ != nil {
			info.Handle = "QQ:" + strconv.FormatInt(*v.QQ, 10)
		}
		avatar = v.Avatar
	}
	if avatar != "" && !opts.HideAvatar {
		info.Avatar = &avatar
	}
	if opts.Extra {
		extra, err := extraInfo(room.Info, opts.HideAvatar)
		if err != nil {
			return publicRoom{}, err
		}
		info.Extra = extra
	}
	return publicRoom{Time: room.Time, ID: room.ID, Msg: room.Msg, Name: room.Name, Source: room.Source, Info: info}, nil
}

// extraInfo is the stored info as JSON (XInfo / QQInfo with their "type"), with
// the avatar set to null when avatars are hidden.
func extraInfo(info any, hideAvatar bool) (json.RawMessage, error) {
	data, err := json.Marshal(info)
	if err != nil || !hideAvatar {
		return data, err
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil {
		return data, nil // not an object; nothing to hide
	}
	if _, ok := fields["avatar"]; ok {
		fields["avatar"] = nil
	}
	return json.Marshal(fields)
}

// mustMarshal is only used for payloads made of numbers, strings and slices of
// them, which encoding/json cannot fail on.
func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic("v2: marshal SSE payload: " + err.Error())
	}
	return data
}
