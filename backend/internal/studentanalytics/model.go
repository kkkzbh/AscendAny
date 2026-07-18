package studentanalytics

import (
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const MaxHistoryLimit = 100

type State string

const (
	StateNotGenerated   State = "not_generated"
	StateNoObservations State = "no_observations"
	StateReady          State = "ready"
)

// SelfQuery is constructed from an already verified access principal. The
// repository still compares every principal field with the current account row
// inside the same database snapshot used to read analytics.
type SelfQuery struct {
	AccountID            string
	SessionID            string
	ExpectedAuthRevision int64
	ExpectedRole         auth.Role
	HistoryLimit         int
}

type Result struct {
	State        State
	HeadRevision int64
	Ready        *ReadyResult
}

type ReadyResult struct {
	ReferenceTime time.Time
	Rating        int64
	Current       analytics.MetricValues
	ExamHistory   []ExamHistoryPoint
	RatingHistory []RatingHistoryPoint
	LatestPeer    *LatestExamPeer
}

type LatestExamPeer struct {
	TotalParticipants int64
	Position          int64
	Rank              int64
	Score             *float64
	Solved            int64
	BandMedian        PeerValues
	Previous          *PeerParticipant
}

type PeerParticipant struct {
	Position int64
	Rank     int64
	Score    *float64
	Solved   int64
	Values   analytics.MetricValues
}

type PeerValues struct {
	Score  *float64
	Solved *float64
	Values analytics.MetricValues
}

type ExamHistoryPoint struct {
	ExamID     string
	SnapshotID string
	Title      string
	EventTime  time.Time
	Values     analytics.MetricValues
}

type RatingHistoryPoint struct {
	ExamID      string
	SnapshotID  string
	Title       string
	EventTime   time.Time
	Rank        int64
	OldRating   int64
	Delta       int64
	NewRating   int64
	Seed        float64
	Performance float64
}
