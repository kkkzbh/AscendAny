package studentanalytics

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestLoadLatestExamPeerProjectsBandAndPreviousParticipant(t *testing.T) {
	t.Parallel()
	selfScore, medianScore, medianSolved := 88.5, 80.0, 2.5
	medianKnowledge := 72.0
	previousPosition, previousRank, previousSolved := int64(2), int64(2), int64(3)
	previousScore, previousKnowledge := 91.0, 75.0
	tx := &scriptedReadTx{rows: []pgx.Row{testRow(func(destinations ...any) error {
		if len(destinations) != 22 {
			return fmt.Errorf("destination count = %d", len(destinations))
		}
		*(destinations[0].(*int64)) = 10
		*(destinations[1].(*int64)) = 10
		*(destinations[2].(*int64)) = 3
		*(destinations[3].(*int64)) = 3
		*(destinations[4].(**float64)) = &selfScore
		*(destinations[5].(*int64)) = 2
		*(destinations[6].(**float64)) = &medianScore
		*(destinations[7].(**float64)) = &medianSolved
		*(destinations[8].(**float64)) = &medianKnowledge
		for index := 9; index <= 12; index++ {
			*(destinations[index].(**float64)) = nil
		}
		*(destinations[13].(**int64)) = &previousPosition
		*(destinations[14].(**int64)) = &previousRank
		*(destinations[15].(**float64)) = &previousScore
		*(destinations[16].(**int64)) = &previousSolved
		*(destinations[17].(**float64)) = &previousKnowledge
		for index := 18; index <= 21; index++ {
			*(destinations[index].(**float64)) = nil
		}
		return nil
	})}}
	peer, err := loadLatestExamPeer(context.Background(), tx, 7, 8, 9, 10)
	if err != nil {
		t.Fatal(err)
	}
	if peer == nil || peer.TotalParticipants != 10 || peer.Position != 3 || peer.Rank != 3 ||
		peer.Score == nil || *peer.Score != selfScore || peer.BandMedian.Solved == nil ||
		*peer.BandMedian.Solved != medianSolved || peer.Previous == nil ||
		peer.Previous.Position != 2 || peer.Previous.Values.Knowledge == nil ||
		*peer.Previous.Values.Knowledge != previousKnowledge {
		t.Fatalf("peer = %#v", peer)
	}
	if len(tx.queries) != 1 || !strings.Contains(tx.queries[0], "pintia_ranking_problem_results") ||
		!strings.Contains(tx.queries[0], "percentile_cont(0.5)") ||
		!strings.Contains(tx.queries[0], "jsonb_array_elements") ||
		len(tx.arguments) != 1 || fmt.Sprint(tx.arguments[0]) != "[7 8 9 10]" {
		t.Fatalf("query/arguments = %#v/%#v", tx.queries, tx.arguments)
	}
}

func TestValidateLatestExamPeerRejectsInconsistentPreviousPosition(t *testing.T) {
	t.Parallel()
	peer := LatestExamPeer{
		TotalParticipants: 3, Position: 2, Rank: 2,
		Previous: &PeerParticipant{Position: 2, Rank: 1},
	}
	if err := validateLatestExamPeer(peer); err == nil {
		t.Fatal("inconsistent previous participant position was accepted")
	}
}
