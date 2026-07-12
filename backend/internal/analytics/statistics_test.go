package analytics

import "testing"

func TestPercentileScoresUseDeterministicMidranksForTies(t *testing.T) {
	t.Parallel()

	allTied := percentileScores(map[int64]float64{3: 7, 1: 7, 2: 7}, 0.05, 0.95)
	for actorID, score := range allTied {
		if score != 50 {
			t.Fatalf("actor %d tied score = %v, want 50", actorID, score)
		}
	}
	mixed := percentileScores(map[int64]float64{1: 0, 2: 10, 3: 10, 4: 1000}, 0.25, 0.75)
	if mixed[2] != mixed[3] {
		t.Fatalf("equal raw values got scores %v and %v", mixed[2], mixed[3])
	}
	for actorID, score := range mixed {
		if score < 0 || score > 100 {
			t.Fatalf("actor %d score = %v outside [0,100]", actorID, score)
		}
	}
}

func TestIntegerDistributionUsesLinearDeterministicQuantiles(t *testing.T) {
	t.Parallel()

	if integerDistribution(nil) != nil {
		t.Fatal("empty distribution is non-nil")
	}
	got := integerDistribution([]int64{100, 10, 40, 20})
	if got.Count != 4 || got.Min != 10 || got.Max != 100 || got.Median != 30 || got.P95 != 91 {
		t.Fatalf("distribution = %#v", got)
	}
}
