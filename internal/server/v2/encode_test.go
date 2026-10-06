package v2

import (
	"encoding/json"
	"strings"
	"testing"

	"mafuyu/internal/utils/model"
)

func TestEncodeHeartbeat(t *testing.T) {
	got := string(EncodeHeartbeat(1777083784000))
	want := `{"time":1777083784000}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeStatistic(t *testing.T) {
	got := string(EncodeStatistic(12, 31))
	want := `{"online":12,"past15m":31}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeRoom_XInfo(t *testing.T) {
	room := &model.Room{
		Time:   1777083784,
		ID:     "01234",
		Msg:    "sample_message",
		Name:   "用户名",
		Source: "x",
		Info:   model.XInfo{TID: "1234567654321", UserName: "@1231412", ScreenName: "screenname", Avatar: ""},
	}
	got, err := EncodeRoom(room, RoomOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"time":1777083784,"id":"01234","msg":"sample_message","name":"用户名","source":"x",` +
		`"info":{"handle":"@1231412","url":"https://x.com/1231412/status/1234567654321","avatar":null}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeRoom_XInfoWithoutUserName(t *testing.T) {
	room := &model.Room{ID: "01234", Info: model.XInfo{TID: "99", ScreenName: "screen", Avatar: "https://a/b.png"}}
	got, err := EncodeRoom(room, RoomOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `"info":{"handle":"screen","url":"https://x.com/i/status/99","avatar":"https://a/b.png"}}`
	if !strings.HasSuffix(string(got), want) {
		t.Fatalf("got %s, want suffix %s", got, want)
	}
}

func TestEncodeRoom_XHandlePrefix(t *testing.T) {
	for _, username := range []string{"username", "@username", "@@username", " username "} {
		pub, err := toPublic(&model.Room{Info: model.XInfo{UserName: username, TID: "123"}}, RoomOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if pub.Info.Handle != "@username" || pub.Info.URL != "https://x.com/username/status/123" {
			t.Fatalf("username %q: got handle %q and URL %q", username, pub.Info.Handle, pub.Info.URL)
		}
	}
}

func TestEncodeRoom_QQInfo(t *testing.T) {
	qq := int64(12344124)
	room := &model.Room{
		Time:   1777083784,
		ID:     "01234",
		Msg:    "msg",
		Name:   "name",
		Source: "qq",
		Info:   model.QQInfo{QQ: &qq, Nickname: "nick", Avatar: "av"},
	}
	got, err := EncodeRoom(room, RoomOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"time":1777083784,"id":"01234","msg":"msg","name":"name","source":"qq",` +
		`"info":{"handle":"nick","url":"","avatar":"av"}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeRoom_QQInfoFallbacks(t *testing.T) {
	qq := int64(12344124)
	for _, tc := range []struct {
		info model.QQInfo
		want string
	}{
		{model.QQInfo{QQ: &qq}, `"info":{"handle":"QQ:12344124","url":"","avatar":null}}`},
		{model.QQInfo{QQ: nil, Nickname: "nick"}, `"info":{"handle":"nick","url":"","avatar":null}}`},
		{model.QQInfo{}, `"info":{"handle":"","url":"","avatar":null}}`},
	} {
		got, err := EncodeRoom(&model.Room{ID: "01234", Source: "qq", Info: tc.info}, RoomOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(got), tc.want) {
			t.Fatalf("got %s, want suffix %s", got, tc.want)
		}
	}
}

func TestEncodeRoom_Extra(t *testing.T) {
	qq := int64(12344124)
	group := int64(3333333)
	room := &model.Room{ID: "01234", Source: "qq", Info: model.QQInfo{QQ: &qq, Group: &group, Nickname: "nick", Avatar: "av"}}
	got, err := EncodeRoom(room, RoomOptions{Extra: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `"info":{"handle":"nick","url":"","avatar":"av","extra":{"type":"QQInfo","qq":12344124,"group":3333333,"nickname":"nick","avatar":"av"}}}`
	if !strings.HasSuffix(string(got), want) {
		t.Fatalf("got %s, want suffix %s", got, want)
	}
}

func TestEncodeRoom_HideAvatar(t *testing.T) {
	room := &model.Room{ID: "01234", Source: "x", Info: model.XInfo{TID: "1", UserName: "@u", Avatar: "https://a/b.png"}}
	got, err := EncodeRoom(room, RoomOptions{HideAvatar: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), `"avatar":null}}`) {
		t.Fatalf("avatar not hidden: %s", got)
	}
	got, err = EncodeRoom(room, RoomOptions{HideAvatar: true, Extra: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "b.png") || !strings.Contains(string(got), `"extra":{"avatar":null,`) {
		t.Fatalf("avatar not hidden inside extra: %s", got)
	}
}

func TestEncodeRoom_UnknownStoredInfo(t *testing.T) {
	// A room restored from the database whose info matched neither type
	room := &model.Room{ID: "01234", Info: json.RawMessage(`{"foo":1}`)}
	got, err := EncodeRoom(room, RoomOptions{Extra: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `"info":{"handle":"","url":"","avatar":null,"extra":{"foo":1}}}`
	if !strings.HasSuffix(string(got), want) {
		t.Fatalf("got %s, want suffix %s", got, want)
	}
}

func TestEncodeRoomSkills(t *testing.T) {
	skills := &model.RoomSkills{ID: "03212", Time: 1777083784, Data: []int{123, 134, 123, 412}}
	got := string(EncodeRoomSkills(skills))
	want := `{"id":"03212","time":1777083784,"data":[123,134,123,412]}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
