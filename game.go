package main

import (
	"crypto/rand"
	"errors"
	"math/big"
	"slices"
)

type Card struct {
	ID   int    `json:"id"`
	Rank int    `json:"rank"`
	Suit string `json:"suit"`
}

type Player struct {
	ID       string
	Name     string
	Bot      bool
	Hand     []Card
	Captured []Card
	Bonus    int
	Score    int
}

type Capture struct {
	Seat  int  `json:"seat"`
	Card  Card `json:"card"`
	Count int  `json:"count"`
	Bonus int  `json:"bonus"`
}

type Move struct {
	Seat    int  `json:"seat"`
	Card    Card `json:"card"`
	Capture int  `json:"capture"`
	Bonus   int  `json:"bonus"`
}

type RoundScore struct {
	Cards    int `json:"cards"`
	Points   int `json:"points"`
	Bonus    int `json:"bonus"`
	Majority int `json:"majority"`
	Total    int `json:"total"`
}

type Game struct {
	Players     []*Player
	Phase       string
	Revision    uint64
	Round       int
	Dealer      int
	Turn        int
	Deck        []Card
	Pile        []Card
	Bottom      Card
	LastCapture *Capture
	LastSeat    int
	History     []Move
	Results     []RoundScore
	Ready       [2]bool
	Winner      int
}

func newGame(p *Player) *Game {
	return &Game{Players: []*Player{p}, Phase: "lobby", Revision: 1, Winner: -1}
}

func shuffledDeck() []Card {
	deck := make([]Card, 0, 52)
	for s, suit := range []string{"clubs", "diamonds", "hearts", "spades"} {
		for rank := 1; rank <= 13; rank++ {
			deck = append(deck, Card{ID: s*13 + rank - 1, Rank: rank, Suit: suit})
		}
	}
	for i := len(deck) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(n.Int64())
		deck[i], deck[j] = deck[j], deck[i]
	}
	// The exposed bottom card is never a jack.
	if deck[51].Rank == 11 {
		for i := 50; i >= 0; i-- {
			if deck[i].Rank != 11 {
				deck[i], deck[51] = deck[51], deck[i]
				break
			}
		}
	}
	// A non-jack is turned face up in the initial four-card pile.
	for i := 3; i >= 0; i-- {
		if deck[i].Rank != 11 {
			deck[i], deck[3] = deck[3], deck[i]
			return deck
		}
	}
	return shuffledDeck()
}

func (g *Game) startRound() {
	g.Round++
	g.Dealer = (g.Round - 1) % 2
	g.Turn = 1 - g.Dealer
	g.Phase = "playing"
	g.Deck = shuffledDeck()
	g.Bottom = g.Deck[51]
	g.Pile = slices.Clone(g.Deck[:4])
	g.Deck = g.Deck[4:]
	g.LastSeat, g.Winner = -1, -1
	g.LastCapture = nil
	g.History = []Move{}
	g.Results = nil
	g.Ready = [2]bool{}
	for _, p := range g.Players {
		p.Hand = nil
		p.Captured = nil
		p.Bonus = 0
	}
	g.deal()
}

func (g *Game) deal() {
	for _, seat := range []int{1 - g.Dealer, g.Dealer} {
		g.Players[seat].Hand = slices.Clone(g.Deck[:4])
		g.Deck = g.Deck[4:]
	}
}

func cardPoints(c Card) int {
	if c.Rank == 1 || c.Rank == 11 {
		return 1
	}
	if c.Rank == 2 && c.Suit == "clubs" {
		return 2
	}
	if c.Rank == 10 && c.Suit == "diamonds" {
		return 3
	}
	return 0
}

func (g *Game) finishRound() {
	seat := g.LastSeat
	if seat < 0 {
		seat = g.Dealer
	}
	g.Players[seat].Captured = append(g.Players[seat].Captured, g.Pile...)
	g.Pile = nil
	g.Results = make([]RoundScore, 2)
	for i, p := range g.Players {
		s := RoundScore{Cards: len(p.Captured), Bonus: p.Bonus}
		for _, c := range p.Captured {
			s.Points += cardPoints(c)
		}
		if len(p.Captured) > len(g.Players[1-i].Captured) {
			s.Majority = 3
		}
		s.Total = s.Points + s.Bonus + s.Majority
		p.Score += s.Total
		g.Results[i] = s
		g.Ready[i] = p.Bot
	}
	g.Phase = "round_over"
	if g.Players[0].Score != g.Players[1].Score && (g.Players[0].Score >= 101 || g.Players[1].Score >= 101) {
		g.Phase = "finished"
		g.Winner = 0
		if g.Players[1].Score > g.Players[0].Score {
			g.Winner = 1
		}
	}
}

func (g *Game) action(seat int, revision uint64, kind string, cardID int) error {
	if revision != g.Revision {
		return errors.New("stale")
	}
	if seat < 0 || seat >= len(g.Players) {
		return errors.New("unauthorized")
	}
	switch kind {
	case "bot":
		if seat != 0 || g.Phase != "lobby" || len(g.Players) != 1 {
			return errors.New("invalid_action")
		}
		g.Players = append(g.Players, &Player{ID: rand.Text(), Name: "Ada", Bot: true})
		g.startRound()
	case "start":
		if seat != 0 || g.Phase != "lobby" || len(g.Players) != 2 {
			return errors.New("invalid_action")
		}
		g.startRound()
	case "next":
		if (g.Phase != "round_over" && g.Phase != "finished") || g.Ready[seat] {
			return errors.New("invalid_action")
		}
		g.Ready[seat] = true
		if g.Ready[0] && g.Ready[1] {
			if g.Phase == "finished" {
				g.Round = 0
				for _, p := range g.Players {
					p.Score = 0
				}
			}
			g.startRound()
		}
	case "play":
		if g.Phase != "playing" || g.Turn != seat {
			return errors.New("not_your_turn")
		}
		p := g.Players[seat]
		index := slices.IndexFunc(p.Hand, func(c Card) bool { return c.ID == cardID })
		if index < 0 {
			return errors.New("invalid_card")
		}
		c := p.Hand[index]
		p.Hand = slices.Delete(p.Hand, index, index+1)
		move := Move{Seat: seat, Card: c}
		count := len(g.Pile)
		if count > 0 && (c.Rank == 11 || c.Rank == g.Pile[count-1].Rank) {
			// The initial pile and the final card of a round cannot score Pişti.
			final := len(g.Deck) == 0 && len(g.Players[0].Hand)+len(g.Players[1].Hand) == 0
			if count == 1 && !final && c.Rank == g.Pile[0].Rank {
				move.Bonus = 10
				if c.Rank == 11 {
					move.Bonus = 20
				}
			}
			move.Capture = count + 1
			p.Captured = append(p.Captured, g.Pile...)
			p.Captured = append(p.Captured, c)
			p.Bonus += move.Bonus
			g.Pile = nil
			g.LastSeat = seat
			g.LastCapture = &Capture{Seat: seat, Card: c, Count: move.Capture, Bonus: move.Bonus}
		} else {
			g.Pile = append(g.Pile, c)
		}
		g.History = append(g.History, move)
		g.Turn = 1 - seat
		if len(g.Players[0].Hand)+len(g.Players[1].Hand) == 0 {
			if len(g.Deck) > 0 {
				g.deal()
			} else {
				g.finishRound()
			}
		}
	default:
		return errors.New("invalid_action")
	}
	g.Revision++
	return nil
}

// Ada sees only her own hand and the public pile. Prefer a match, then a
// worthwhile jack capture, otherwise discard the least valuable card.
func (g *Game) botCard() int {
	hand := g.Players[g.Turn].Hand
	if len(g.Pile) > 0 {
		top := g.Pile[len(g.Pile)-1]
		for _, c := range hand {
			if c.Rank == top.Rank {
				return c.ID
			}
		}
		if len(g.Pile) > 2 || cardPoints(top) > 0 {
			for _, c := range hand {
				if c.Rank == 11 {
					return c.ID
				}
			}
		}
	}
	best := hand[0]
	for _, c := range hand {
		if cardPoints(c) < cardPoints(best) {
			best = c
		}
	}
	return best.ID
}
