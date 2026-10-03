package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
)

func TestSessionValidation(t *testing.T) {
	a := newApp(context.Background())
	a.rooms["ABCD1234"] = &Room{Code: "ABCD1234", Game: newGame(&Player{ID: "player", Name: "P"}), Streams: map[string]map[chan Snapshot]bool{}}
	for _, tt := range []struct {
		name  string
		c     credentials
		valid bool
	}{
		{"restore seat", credentials{"ABCD1234", "player", time.Now().Add(time.Hour).Unix()}, true},
		{"expired", credentials{"ABCD1234", "player", time.Now().Add(-time.Second).Unix()}, false},
		{"missing room", credentials{"NOTFOUND", "player", time.Now().Add(time.Hour).Unix()}, false},
		{"wrong player", credentials{"ABCD1234", "other", time.Now().Add(time.Hour).Unix()}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, seat, err := a.authenticate(a.sign(tt.c))
			if tt.valid {
				if err != nil || r.Code != "ABCD1234" || seat != 0 {
					t.Fatalf("restore failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid session accepted")
			}
		})
	}
	valid := a.sign(credentials{"ABCD1234", "player", time.Now().Add(time.Hour).Unix()})
	for _, token := range []string{"", valid + "x", "tampered." + strings.Split(valid, ".")[1], valid + ".extra"} {
		if _, _, err := a.authenticate(token); err == nil {
			t.Fatal("tampered session accepted")
		}
	}
}

func TestPersonalizedSnapshots(t *testing.T) {
	r := &Room{Code: "ABCD1234", Game: &Game{Revision: 7, Phase: "playing", Turn: 0, Players: []*Player{{ID: "one", Name: "One", Hand: []Card{{ID: 4, Rank: 5}}}, {ID: "two", Name: "Two", Hand: []Card{{ID: 8, Rank: 9}}}}, Pile: []Card{{ID: 2, Rank: 3}, {ID: 3, Rank: 4}}, Deck: []Card{{ID: 30, Rank: 5}}}, Streams: map[string]map[chan Snapshot]bool{}}
	for _, seat := range []int{0, 1} {
		t.Run(string(rune('0'+seat)), func(t *testing.T) {
			s := r.snapshot(seat)
			data, _ := json.Marshal(s)
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(data, &raw)
			if len(s.Hand) != 1 || s.Hand[0].ID != r.Game.Players[seat].Hand[0].ID {
				t.Fatal("wrong personalized hand")
			}
			if _, ok := raw["deck"]; ok {
				t.Fatal("deck exposed")
			}
			if _, ok := raw["pile"]; ok {
				t.Fatal("hidden opening cards exposed")
			}
			if s.Top.ID != 3 || s.PileCount != 2 || s.DeckCount != 1 {
				t.Fatal("missing public information")
			}
			var players []map[string]any
			_ = json.Unmarshal(raw["players"], &players)
			for _, p := range players {
				if _, ok := p["hand"]; ok {
					t.Fatal("other players' cards exposed")
				}
				if _, ok := p["id"]; ok {
					t.Fatal("private player identity exposed")
				}
			}
			if seat == 0 && len(s.Legal) != 1 || seat == 1 && len(s.Legal) != 0 {
				t.Fatal("wrong legal moves")
			}
			s.Hand[0].Rank = 13
			s.Top.Rank = 13
			if r.Game.Players[seat].Hand[0].Rank == 13 || r.Game.Pile[1].Rank == 13 {
				t.Fatal("snapshot aliases game cards")
			}
		})
	}
}

func TestJoinAndActions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx)
	server := httptest.NewServer(a.handler(fstest.MapFS{"index.html": {Data: []byte("shell")}}))
	defer server.Close()
	r, err := http.Post(server.URL+"/api/join", "application/json", strings.NewReader(`{"name":"One"}`))
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]string
	_ = json.NewDecoder(r.Body).Decode(&first)
	r.Body.Close()
	if r.StatusCode != 200 || len(first["room"]) != 8 {
		t.Fatal("room creation failed")
	}
	for _, c := range first["room"] {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			t.Fatal("invalid room alphabet")
		}
	}
	r, err = http.Post(server.URL+"/api/join", "application/json", strings.NewReader(`{"name":"Two","room":"`+strings.ToLower(first["room"])+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]string
	_ = json.NewDecoder(r.Body).Decode(&second)
	r.Body.Close()
	if r.StatusCode != 200 || second["room"] != first["room"] {
		t.Fatal("case insensitive join failed")
	}
	r, err = http.Post(server.URL+"/api/join", "application/json", strings.NewReader(`{"name":"Third","room":"`+first["room"]+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 409 {
		t.Fatal("full room accepted a third player")
	}
	for _, tt := range []struct {
		name, token, body string
		status            int
	}{
		{"guest cannot start", second["token"], `{"kind":"start","revision":2}`, 409},
		{"reject stale", first["token"], `{"kind":"start","revision":1}`, 409},
		{"start", first["token"], `{"kind":"start","revision":2}`, 200},
		{"stale after start", first["token"], `{"kind":"start","revision":2}`, 409},
		{"unsigned action", "bad", `{"kind":"play","revision":3}`, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", server.URL+"/api/action", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+tt.token)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tt.status {
				b, _ := io.ReadAll(response.Body)
				t.Fatalf("status %d, want %d: %s", response.StatusCode, tt.status, b)
			}
		})
	}
	request, _ := http.NewRequest("GET", server.URL+"/api/session", nil)
	request.Header.Set("Authorization", "Bearer "+first["token"])
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("session can be cached")
	}
	var s Snapshot
	_ = json.NewDecoder(response.Body).Decode(&s)
	if s.Phase != "playing" || s.Seat != 0 || len(s.Hand) != 4 {
		t.Fatal("seat restoration failed")
	}
}

func TestLivePresenceAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx)
	r := &Room{Code: "ABCD1234", Game: newGame(&Player{ID: "player", Name: "Player"}), Streams: map[string]map[chan Snapshot]bool{}}
	a.rooms[r.Code] = r
	server := httptest.NewServer(a.handler(fstest.MapFS{"index.html": {Data: []byte("shell")}}))
	defer server.Close()
	token := a.sign(credentials{r.Code, "player", time.Now().Add(time.Hour).Unix()})
	readCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/live?token=" + token
	c1, _, err := websocket.Dial(readCtx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	_, data, err := c1.Read(readCtx)
	if err != nil {
		t.Fatal("initial state was not immediate:", err)
	}
	var message struct {
		Kind     string
		Snapshot Snapshot
	}
	_ = json.Unmarshal(data, &message)
	if message.Kind != "state" || !message.Snapshot.Players[0].Online {
		t.Fatal("stream not ready on subscription")
	}
	firstSequence := message.Snapshot.Sequence
	c2, _, err := websocket.Dial(readCtx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, data, err = c2.Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(data, &message)
	if message.Snapshot.Sequence <= firstSequence {
		t.Fatal("presence did not advance publication sequence")
	}
	c2.CloseNow()
	for {
		_, data, err = c1.Read(readCtx)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(data, &message)
		r.mu.Lock()
		streams := len(r.Streams["player"])
		revision := r.Game.Revision
		r.mu.Unlock()
		if streams == 1 {
			if !message.Snapshot.Players[0].Online || revision != 1 {
				t.Fatal("closing second tab changed online state or game revision")
			}
			break
		}
	}
	cancel()
	for {
		if _, _, err = c1.Read(readCtx); err != nil {
			break
		}
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		streams := len(r.Streams["player"])
		r.mu.Unlock()
		if streams == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("shutdown left stream subscribed")
}

func TestHTTPGuards(t *testing.T) {
	a := newApp(context.Background())
	handler := a.handler(fstest.MapFS{"index.html": {Data: []byte("shell")}, "sw.js": {Data: []byte("worker")}})
	for _, tt := range []struct {
		name, method, path, body, origin string
		status                           int
	}{
		{"root with room", "GET", "/?room=ABCD1234", "", "", 200},
		{"bad name", "POST", "/api/join", `{"name":""}`, "", 400},
		{"unknown field", "POST", "/api/join", `{"name":"One","auction":true}`, "", 400},
		{"trailing malformed input", "POST", "/api/join", `{"name":"One"}x`, "", 400},
		{"unknown room", "POST", "/api/join", `{"name":"One","room":"NOTFOUND"}`, "", 404},
		{"cross origin", "POST", "/api/join", `{"name":"One"}`, "https://evil.example", 403},
		{"unauthenticated live", "GET", "/api/live?token=bad", "", "", 401},
		{"expired inspection", "GET", "/api/session", "", "", 401},
		{"worker", "GET", "/sw.js", "", "", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if (tt.path == "/sw.js" || strings.HasPrefix(tt.path, "/?")) && w.Header().Get("Cache-Control") != "no-cache" {
				t.Fatal("shell must revalidate")
			}
		})
	}
}

func TestLiveSessionExpires(t *testing.T) {
	a := newApp(context.Background())
	r := &Room{Code: "ABCD1234", Game: newGame(&Player{ID: "player", Name: "Player"}), Streams: map[string]map[chan Snapshot]bool{}}
	a.rooms[r.Code] = r
	server := httptest.NewServer(a.handler(fstest.MapFS{"index.html": {Data: []byte("shell")}}))
	defer server.Close()
	token := a.sign(credentials{r.Code, "player", time.Now().Add(2 * time.Second).Unix()})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/live?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err = c.Read(ctx); err != nil {
		t.Fatal("no initial state", err)
	}
	if _, _, err = c.Read(ctx); err == nil {
		t.Fatal("expired stream remained open")
	}
	if ctx.Err() != nil {
		t.Fatal("stream closed from test timeout, not session expiry")
	}
	if _, _, err = a.authenticate(token); err == nil {
		t.Fatal("expired session still authenticates")
	}
}

func TestRoomCleanupPreservesConnectedSeats(t *testing.T) {
	a := newApp(context.Background())
	active := &Room{Code: "ACTIVE12", Game: newGame(&Player{ID: "active", Name: "Active"}), Streams: map[string]map[chan Snapshot]bool{"active": {make(chan Snapshot, 1): true}}, Updated: time.Now().Add(-25 * time.Hour)}
	abandoned := &Room{Code: "OLDROOM1", Game: newGame(&Player{ID: "old", Name: "Old"}), Streams: map[string]map[chan Snapshot]bool{}, Updated: time.Now().Add(-25 * time.Hour)}
	a.rooms[active.Code] = active
	a.rooms[abandoned.Code] = abandoned
	req := httptest.NewRequest("POST", "/api/join", strings.NewReader(`{"name":"New"}`))
	w := httptest.NewRecorder()
	a.join(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.rooms[active.Code] != active {
		t.Fatal("connected room was removed")
	}
	if a.rooms[abandoned.Code] != nil {
		t.Fatal("abandoned room was retained")
	}
}

func TestBotTimersSerializeAndRejectStaleRevisions(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "one timer per turn", true: "ignore old revision"}[stale], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := newApp(ctx)
			g := newGame(&Player{ID: "human", Name: "Human"})
			if err := g.action(0, g.Revision, "bot", 0); err != nil {
				t.Fatal(err)
			}
			r := &Room{Code: "BOTROOM1", Game: g, Streams: map[string]map[chan Snapshot]bool{}}
			r.mu.Lock()
			a.scheduleBot(r)
			a.scheduleBot(r)
			if stale {
				if err := g.action(g.Turn, g.Revision, "play", g.Players[g.Turn].Hand[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			r.mu.Unlock()
			time.Sleep(time.Second)
			r.mu.Lock()
			defer r.mu.Unlock()
			if len(g.History) != 1 || g.Revision != 3 || g.Turn != 0 {
				t.Fatal("bot timer advanced more than the expected move")
			}
		})
	}
}
