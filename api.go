package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

type Room struct {
	mu          sync.Mutex
	Code        string
	Game        *Game
	Sequence    uint64
	Streams     map[string]map[chan Snapshot]bool
	Updated     time.Time
	botRevision uint64
}

type App struct {
	mu    sync.Mutex
	rooms map[string]*Room
	key   []byte
	ctx   context.Context
}

type PublicPlayer struct {
	Name      string `json:"name"`
	Bot       bool   `json:"bot"`
	Online    bool   `json:"online"`
	HandCount int    `json:"handCount"`
	Captured  int    `json:"captured"`
	Bonus     int    `json:"bonus"`
	Score     int    `json:"score"`
}

type Snapshot struct {
	Room        string         `json:"room"`
	Sequence    uint64         `json:"sequence"`
	Revision    uint64         `json:"revision"`
	Phase       string         `json:"phase"`
	Seat        int            `json:"seat"`
	Players     []PublicPlayer `json:"players"`
	Hand        []Card         `json:"hand"`
	Legal       []int          `json:"legal"`
	Turn        int            `json:"turn"`
	Dealer      int            `json:"dealer"`
	Round       int            `json:"round"`
	DeckCount   int            `json:"deckCount"`
	Bottom      Card           `json:"bottom"`
	PileCount   int            `json:"pileCount"`
	Top         *Card          `json:"top"`
	LastCapture *Capture       `json:"lastCapture"`
	History     []Move         `json:"history"`
	Results     []RoundScore   `json:"results"`
	Ready       [2]bool        `json:"ready"`
	Winner      int            `json:"winner"`
}

func newApp(ctx context.Context) *App {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &App{rooms: make(map[string]*Room), key: key, ctx: ctx}
}

func (r *Room) snapshot(seat int) Snapshot {
	g := r.Game
	s := Snapshot{Room: r.Code, Sequence: r.Sequence, Revision: g.Revision, Phase: g.Phase, Seat: seat, Turn: g.Turn, Dealer: g.Dealer, Round: g.Round, DeckCount: len(g.Deck), Bottom: g.Bottom, PileCount: len(g.Pile), Ready: g.Ready, Winner: g.Winner, Hand: append([]Card{}, g.Players[seat].Hand...), Legal: []int{}, History: append([]Move{}, g.History...), Results: append([]RoundScore{}, g.Results...)}
	if len(g.Pile) > 0 {
		c := g.Pile[len(g.Pile)-1]
		s.Top = &c
	}
	if g.LastCapture != nil {
		c := *g.LastCapture
		s.LastCapture = &c
	}
	for i, p := range g.Players {
		s.Players = append(s.Players, PublicPlayer{Name: p.Name, Bot: p.Bot, Online: p.Bot || len(r.Streams[p.ID]) > 0, HandCount: len(p.Hand), Captured: len(p.Captured), Bonus: p.Bonus, Score: p.Score})
		if i == seat && g.Phase == "playing" && g.Turn == seat {
			for _, c := range p.Hand {
				s.Legal = append(s.Legal, c.ID)
			}
		}
	}
	return s
}

// All callers hold the room lock; slow subscribers receive the newest state.
func (r *Room) publish() {
	r.Sequence++
	r.Updated = time.Now()
	for seat, p := range r.Game.Players {
		for ch := range r.Streams[p.ID] {
			s := r.snapshot(seat)
			select {
			case ch <- s:
			default:
				select {
				case <-ch:
				default:
				}
				ch <- s
			}
		}
	}
}

type credentials struct {
	Room    string `json:"room"`
	Player  string `json:"player"`
	Expires int64  `json:"expires"`
}

func (a *App) sign(c credentials) string {
	data, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) authenticate(token string) (*Room, int, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, 0, errors.New("expired")
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, 0, errors.New("expired")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	var c credentials
	if err != nil || json.Unmarshal(body, &c) != nil || c.Expires <= time.Now().Unix() {
		return nil, 0, errors.New("expired")
	}
	a.mu.Lock()
	r := a.rooms[c.Room]
	a.mu.Unlock()
	if r == nil {
		return nil, 0, errors.New("expired")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.Game.Players {
		if p.ID == c.Player && !p.Bot {
			return r, i, nil
		}
	}
	return nil, 0, errors.New("expired")
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"error": code})
}
func bearer(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(value, "Bearer ")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "bad_request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "bad_request")
		return false
	}
	return true
}

func (a *App) handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/join", a.join)
	mux.HandleFunc("POST /api/action", a.action)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("GET /api/live", a.live)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"status": "ok"}) })
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					fail(w, 403, "forbidden")
					return
				}
			}
		} else if r.URL.Path == "/" || r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Room string `json:"room"`
		Bot  bool   `json:"bot"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 24 {
		fail(w, 400, "invalid_name")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Room))
	p := &Player{ID: rand.Text(), Name: name}
	a.mu.Lock()
	var room *Room
	if code == "" {
		// Remove abandoned rooms, while keeping active seats across brief outages.
		for k, old := range a.rooms {
			old.mu.Lock()
			streams := 0
			for _, playerStreams := range old.Streams {
				streams += len(playerStreams)
			}
			if streams == 0 && time.Since(old.Updated) > 24*time.Hour {
				delete(a.rooms, k)
			}
			old.mu.Unlock()
		}
		if len(a.rooms) >= 2048 {
			a.mu.Unlock()
			fail(w, 503, "busy")
			return
		}
		for {
			code = rand.Text()[:8]
			if a.rooms[code] == nil {
				break
			}
		}
		room = &Room{Code: code, Game: newGame(p), Streams: map[string]map[chan Snapshot]bool{}, Updated: time.Now()}
		a.rooms[code] = room
		room.mu.Lock()
	} else {
		room = a.rooms[code]
		if room == nil {
			a.mu.Unlock()
			fail(w, 404, "room_not_found")
			return
		}
		room.mu.Lock()
		if len(room.Game.Players) >= 2 || room.Game.Phase != "lobby" {
			room.mu.Unlock()
			a.mu.Unlock()
			fail(w, 409, "room_full")
			return
		}
		room.Game.Players = append(room.Game.Players, p)
		room.Game.Revision++
	}
	a.mu.Unlock()
	if req.Bot && len(room.Game.Players) == 1 {
		_ = room.Game.action(0, room.Game.Revision, "bot", 0)
	}
	room.publish()
	a.scheduleBot(room)
	room.mu.Unlock()
	token := a.sign(credentials{Room: code, Player: p.ID, Expires: time.Now().Add(7 * 24 * time.Hour).Unix()})
	reply(w, 200, map[string]string{"room": code, "token": token})
}

func (a *App) session(w http.ResponseWriter, r *http.Request) {
	room, seat, err := a.authenticate(bearer(r))
	if err != nil {
		fail(w, 401, "expired")
		return
	}
	room.mu.Lock()
	s := room.snapshot(seat)
	room.mu.Unlock()
	reply(w, 200, s)
}

func (a *App) action(w http.ResponseWriter, r *http.Request) {
	room, seat, err := a.authenticate(bearer(r))
	if err != nil {
		fail(w, 401, "expired")
		return
	}
	var req struct {
		Kind     string `json:"kind"`
		Revision uint64 `json:"revision"`
		Card     int    `json:"card"`
	}
	if !decode(w, r, &req) {
		return
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if err := room.Game.action(seat, req.Revision, req.Kind, req.Card); err != nil {
		fail(w, 409, err.Error())
		return
	}
	room.publish()
	a.scheduleBot(room)
	reply(w, 200, map[string]uint64{"revision": room.Game.Revision})
}

func (a *App) scheduleBot(r *Room) {
	g := r.Game
	if g.Phase != "playing" || !g.Players[g.Turn].Bot || r.botRevision == g.Revision {
		return
	}
	revision := g.Revision
	r.botRevision = revision
	go func() {
		timer := time.NewTimer(850 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if g.Revision != revision || g.Phase != "playing" || !g.Players[g.Turn].Bot {
			return
		}
		if g.action(g.Turn, revision, "play", g.botCard()) == nil {
			r.publish()
			a.scheduleBot(r)
		}
	}()
}

func (a *App) live(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	room, seat, err := a.authenticate(token)
	if err != nil {
		fail(w, 401, "expired")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	// authenticate already verified this signed payload. Bound an established
	// stream by the same expiry as HTTP actions and restoration requests.
	body, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	var credential credentials
	_ = json.Unmarshal(body, &credential)
	ctx, cancel := context.WithDeadline(a.ctx, time.Unix(credential.Expires, 0))
	defer cancel()
	ctx = c.CloseRead(ctx)
	ch := make(chan Snapshot, 1)
	room.mu.Lock()
	id := room.Game.Players[seat].ID
	if room.Streams[id] == nil {
		room.Streams[id] = map[chan Snapshot]bool{}
	}
	room.Streams[id][ch] = true
	room.publish()
	room.mu.Unlock()
	defer func() { room.mu.Lock(); delete(room.Streams[id], ch); room.publish(); room.mu.Unlock() }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		var msg any
		select {
		case <-ctx.Done():
			return
		case s := <-ch:
			msg = struct {
				Kind     string   `json:"kind"`
				Snapshot Snapshot `json:"snapshot"`
			}{"state", s}
		case <-ticker.C:
			ping, done := context.WithTimeout(ctx, 3*time.Second)
			err := c.Ping(ping)
			done()
			if err != nil {
				return
			}
			msg = map[string]string{"kind": "heartbeat"}
		}
		data, _ := json.Marshal(msg)
		write, done := context.WithTimeout(ctx, 3*time.Second)
		err := c.Write(write, websocket.MessageText, data)
		done()
		if err != nil {
			return
		}
	}
}
