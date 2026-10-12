package domain

import (
	"math"
	"testing"
)

func TestWilsonLowerBoundControlValues(t *testing.T) {
	for _, tc := range []struct {
		counts Counts
		want   float64
	}{
		{Counts{}, 0},
		{Counts{Cringe: 0, NonCringe: 10}, 0},
		{Counts{Cringe: 1}, 0.206543291},
		{Counts{Cringe: 9, NonCringe: 1}, 0.595843615},
		{Counts{Cringe: 90, NonCringe: 10}, 0.825632696},
		{Counts{Cringe: 100}, 0.963005193},
	} {
		got := tc.counts.WilsonLowerBound()
		if math.IsNaN(got) || got < 0 || got > 1 || math.Abs(got-tc.want) > 1e-9 {
			t.Fatal(tc.counts, got, tc.want)
		}
	}
}

func TestWilsonLowerBoundRewardsPositiveVotesAndLargerSamples(t *testing.T) {
	previous := -1.0
	for cringe := int64(0); cringe <= 20; cringe++ {
		score := (Counts{Cringe: cringe, NonCringe: 20 - cringe}).WilsonLowerBound()
		if score <= previous {
			t.Fatal("positive vote did not improve ranking", cringe, score, previous)
		}
		previous = score
	}
	previous = -1
	for n := int64(1); n <= 100; n++ {
		score := (Counts{Cringe: n, NonCringe: n}).WilsonLowerBound()
		if score <= previous {
			t.Fatal("larger sample at 50% did not improve ranking", n, score, previous)
		}
		previous = score
	}
}
