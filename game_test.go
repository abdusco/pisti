package main

import (
	"reflect"
	"testing"
)

func TestCaptures(t *testing.T) {
	tests := []struct {
		name         string
		pile         []Card
		card         Card
		final        bool
		count, bonus int
	}{
		{"matching pile", []Card{{ID: 0, Rank: 1}, {ID: 5, Rank: 6}}, Card{ID: 18, Rank: 6}, false, 3, 0},
		{"mismatched rank", []Card{{ID: 5, Rank: 6}}, Card{ID: 19, Rank: 7}, false, 0, 0},
		{"pişti", []Card{{ID: 5, Rank: 6}}, Card{ID: 18, Rank: 6}, false, 2, 10},
		{"jack clears pile", []Card{{ID: 5, Rank: 6}, {ID: 3, Rank: 4}}, Card{ID: 10, Rank: 11}, false, 3, 0},
		{"jack on lone non-jack", []Card{{ID: 5, Rank: 6}}, Card{ID: 10, Rank: 11}, false, 2, 0},
		{"double pişti", []Card{{ID: 23, Rank: 11}}, Card{ID: 10, Rank: 11}, false, 2, 20},
		{"jack on empty pile", nil, Card{ID: 10, Rank: 11}, false, 0, 0},
		{"initial four-card pile", []Card{{ID: 0, Rank: 1}, {ID: 1, Rank: 2}, {ID: 2, Rank: 3}, {ID: 5, Rank: 6}}, Card{ID: 18, Rank: 6}, false, 5, 0},
		{"final card forbids pişti", []Card{{ID: 5, Rank: 6}}, Card{ID: 18, Rank: 6}, true, 2, 0},
		{"final jack forbids double pişti", []Card{{ID: 23, Rank: 11}}, Card{ID: 10, Rank: 11}, true, 2, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Game{Phase: "playing", Revision: 3, Turn: 0, Pile: tt.pile, LastSeat: -1, Players: []*Player{{Hand: []Card{tt.card}}, {}}}
			if !tt.final {
				g.Players[1].Hand = []Card{{ID: 51, Rank: 13}}
			}
			if err := g.action(0, 3, "play", tt.card.ID); err != nil {
				t.Fatal(err)
			}
			move := g.History[0]
			if move.Capture != tt.count || move.Bonus != tt.bonus {
				t.Fatalf("move = %+v; want count %d, bonus %d", move, tt.count, tt.bonus)
			}
			if g.Revision != 4 {
				t.Fatalf("revision = %d", g.Revision)
			}
		})
	}
}

func TestCardPoints(t *testing.T) {
	for _, tt := range []struct {
		name string
		card Card
		want int
	}{
		{"ace", Card{Rank: 1, Suit: "hearts"}, 1}, {"jack", Card{Rank: 11, Suit: "spades"}, 1}, {"two clubs", Card{Rank: 2, Suit: "clubs"}, 2}, {"ten diamonds", Card{Rank: 10, Suit: "diamonds"}, 3}, {"other two", Card{Rank: 2, Suit: "hearts"}, 0}, {"other ten", Card{Rank: 10, Suit: "clubs"}, 0}, {"king", Card{Rank: 13, Suit: "diamonds"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardPoints(tt.card); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInvalidMovesDoNotMutate(t *testing.T) {
	for _, tt := range []struct {
		name, phase, kind, err string
		seat, card             int
		revision               uint64
	}{
		{"stale", "playing", "play", "stale", 0, 5, 6}, {"wrong turn", "playing", "play", "not_your_turn", 1, 8, 7}, {"not in hand", "playing", "play", "invalid_card", 0, 8, 7}, {"wrong phase", "round_over", "play", "not_your_turn", 0, 5, 7}, {"unknown action", "playing", "auction", "invalid_action", 0, 5, 7}, {"unauthorized", "playing", "play", "unauthorized", 2, 5, 7}, {"guest starts", "lobby", "start", "invalid_action", 1, 0, 7}, {"bot at full table", "lobby", "bot", "invalid_action", 0, 0, 7}, {"next during play", "playing", "next", "invalid_action", 0, 0, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := &Game{Phase: tt.phase, Revision: 7, Players: []*Player{{Hand: []Card{{ID: 5, Rank: 6}}}, {Hand: []Card{{ID: 8, Rank: 9}}}}}
			before0 := append([]Card{}, g.Players[0].Hand...)
			before1 := append([]Card{}, g.Players[1].Hand...)
			err := g.action(tt.seat, tt.revision, tt.kind, tt.card)
			if err == nil || err.Error() != tt.err {
				t.Fatalf("got %v, want %s", err, tt.err)
			}
			if g.Revision != 7 || !reflect.DeepEqual(before0, g.Players[0].Hand) || !reflect.DeepEqual(before1, g.Players[1].Hand) {
				t.Fatal("invalid move mutated game")
			}
		})
	}
}

func TestDealAndRoundProgression(t *testing.T) {
	for run := 0; run < 20; run++ {
		g := newGame(&Player{ID: "human", Name: "Player"})
		if err := g.action(0, g.Revision, "bot", 0); err != nil {
			t.Fatal(err)
		}
		if g.Round != 1 || g.Dealer != 0 || g.Turn != 1 || len(g.Deck) != 40 || len(g.Pile) != 4 || g.Pile[3].Rank == 11 || g.Bottom.Rank == 11 {
			t.Fatal("incorrect opening deal")
		}
		seen := map[int]bool{}
		for _, c := range g.Pile {
			seen[c.ID] = true
		}
		for g.Phase == "playing" {
			seat := g.Turn
			card := g.Players[seat].Hand[0]
			if seen[card.ID] {
				t.Fatalf("duplicate card %d", card.ID)
			}
			seen[card.ID] = true
			if err := g.action(seat, g.Revision, "play", card.ID); err != nil {
				t.Fatal(err)
			}
			if g.Phase == "playing" && len(g.History)%8 == 0 {
				if len(g.Players[0].Hand) != 4 || len(g.Players[1].Hand) != 4 {
					t.Fatal("hands not replenished")
				}
			}
		}
		if len(seen) != 52 || len(g.History) != 48 || len(g.Players[0].Captured)+len(g.Players[1].Captured) != 52 {
			t.Fatal("cards lost during round")
		}
		if g.Results[0].Points+g.Results[1].Points != 13 {
			t.Fatal("incorrect special card points")
		}
		majority := g.Results[0].Majority + g.Results[1].Majority
		if g.Results[0].Cards == 26 {
			if majority != 0 {
				t.Fatal("tied majority received points")
			}
		} else if majority != 3 {
			t.Fatal("majority must be three points")
		}
		if !g.Ready[1] || g.Ready[0] {
			t.Fatal("bot should automatically be ready")
		}
		expectedRound := 2
		if g.Phase == "finished" {
			expectedRound = 1
		}
		if err := g.action(0, g.Revision, "next", 0); err != nil {
			t.Fatal(err)
		}
		if g.Phase != "playing" || g.Round != expectedRound || g.Dealer != (expectedRound-1)%2 || g.Turn != 1-g.Dealer {
			t.Fatal("dealer did not rotate")
		}
	}
}

func TestFinalScoringAndMatch(t *testing.T) {
	for _, tt := range []struct {
		name           string
		score0, score1 int
		phase          string
		winner         int
	}{
		{"below target", 0, 0, "round_over", -1}, {"player zero wins", 100, 0, "finished", 0}, {"player one wins", 0, 100, "finished", 1}, {"both reach target higher wins", 99, 100, "finished", 1}, {"tie continues", 100, 100, "round_over", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := &Game{Players: []*Player{{Score: tt.score0, Captured: []Card{{Rank: 1}}}, {Score: tt.score1, Captured: []Card{{Rank: 11}}}}, LastSeat: 0, Winner: -1, Pile: []Card{{Rank: 7}}}
			// A tied card count prevents the majority bonus from changing a tied match.
			g.Players[1].Captured = append(g.Players[1].Captured, Card{Rank: 8})
			g.finishRound()
			if g.Phase != tt.phase || g.Winner != tt.winner {
				t.Fatalf("got %s winner %d; want %s winner %d", g.Phase, g.Winner, tt.phase, tt.winner)
			}
			if len(g.Players[0].Captured) != 2 || len(g.Pile) != 0 {
				t.Fatal("remaining cards not awarded to last capture")
			}
		})
	}
}

func TestReadyAndRematch(t *testing.T) {
	g := &Game{Phase: "finished", Revision: 1, Round: 5, Players: []*Player{{Score: 110}, {Score: 90}}, Winner: 0}
	if err := g.action(0, 1, "next", 0); err != nil {
		t.Fatal(err)
	}
	if g.Phase != "finished" || !g.Ready[0] {
		t.Fatal("must wait for both players")
	}
	if err := g.action(0, 2, "next", 0); err == nil {
		t.Fatal("duplicate ready accepted")
	}
	if err := g.action(1, 2, "next", 0); err != nil {
		t.Fatal(err)
	}
	if g.Phase != "playing" || g.Round != 1 || g.Players[0].Score != 0 || g.Players[1].Score != 0 || g.Winner != -1 {
		t.Fatal("rematch didn't reset scores")
	}
}

func TestBotUsesOnlyPublicInformation(t *testing.T) {
	for _, tt := range []struct {
		name string
		pile []Card
		hand []Card
		want int
	}{
		{"prefer matching rank", []Card{{Rank: 6}}, []Card{{ID: 10, Rank: 11}, {ID: 18, Rank: 6}, {ID: 3, Rank: 4}}, 18},
		{"save jack on a small pile", []Card{{Rank: 6}}, []Card{{ID: 10, Rank: 11}, {ID: 3, Rank: 4}}, 3},
		{"jack takes valuable card", []Card{{Rank: 1}}, []Card{{ID: 10, Rank: 11}, {ID: 3, Rank: 4}}, 10},
		{"jack takes larger pile", []Card{{Rank: 3}, {Rank: 4}, {Rank: 6}}, []Card{{ID: 10, Rank: 11}, {ID: 3, Rank: 4}}, 10},
		{"empty table low value discard", nil, []Card{{ID: 0, Rank: 1}, {ID: 3, Rank: 4}}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := &Game{Turn: 1, Pile: tt.pile, Players: []*Player{{Hand: []Card{{ID: 51, Rank: 13}}}, {Bot: true, Hand: tt.hand}}}
			if got := g.botCard(); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
			g.Players[0].Hand = []Card{{ID: 10, Rank: 11}, {ID: 18, Rank: 6}}
			g.Deck = []Card{{ID: 5, Rank: 6}}
			if got := g.botCard(); got != tt.want {
				t.Fatal("opponent hand or deck changed bot decision")
			}
		})
	}
}
