package recommendation

import (
	"fmt"
	"slices"
	"time"
)

type knowledgeActivityAccumulator struct {
	attempted map[string]struct{}
	correct   map[string]struct{}
	last      *time.Time
	recent    map[string]RecommendationKnowledgeActivityDay
}

func buildKnowledgeActivity(
	catalog knowledgeCatalog,
	facts problemFactIndex,
	problemRows []problemActivityRow,
	recentRows []recentActivityRow,
) ([]RecommendationKnowledgeActivity, error) {
	assignmentByKey := make(map[string]problemAssignment, len(catalog.Assignments))
	for _, assignment := range catalog.Assignments {
		assignmentByKey[pintiaProblemKey(assignment.ProblemID, assignment.ProblemFactSHA256)] = assignment
	}
	byPoint := make(map[string]*knowledgeActivityAccumulator, len(catalog.Points))
	for _, point := range catalog.Points {
		byPoint[point.ID] = &knowledgeActivityAccumulator{
			attempted: make(map[string]struct{}),
			correct:   make(map[string]struct{}),
			recent:    make(map[string]RecommendationKnowledgeActivityDay),
		}
	}
	for index, row := range problemRows {
		fact, exists := facts[row.Identity]
		if !exists {
			return nil, fmt.Errorf("knowledge activity problem %d has no prevalidated fact", index)
		}
		assignment, assigned := assignmentByKey[fact.ProblemKey]
		if !assigned {
			continue
		}
		for _, weight := range assignment.Knowledge {
			activity := byPoint[weight.KnowledgePointID]
			activity.attempted[fact.SourceProblemKey] = struct{}{}
			if row.Correct {
				activity.correct[fact.SourceProblemKey] = struct{}{}
			}
			if activity.last == nil || row.LastSubmittedAt.After(*activity.last) {
				value := row.LastSubmittedAt.UTC()
				activity.last = &value
			}
		}
	}
	for index, row := range recentRows {
		if _, err := time.Parse(time.DateOnly, row.Date); err != nil {
			return nil, fmt.Errorf("knowledge activity recent row %d has invalid date: %w", index, err)
		}
		fact, exists := facts[row.Identity]
		if !exists {
			return nil, fmt.Errorf("knowledge activity recent row %d has no prevalidated fact", index)
		}
		assignment, assigned := assignmentByKey[fact.ProblemKey]
		if !assigned {
			continue
		}
		for _, weight := range assignment.Knowledge {
			activity := byPoint[weight.KnowledgePointID]
			day := activity.recent[row.Date]
			day.Date = row.Date
			day.Attempted += row.Attempted
			day.Correct += row.Correct
			activity.recent[row.Date] = day
		}
	}
	result := make([]RecommendationKnowledgeActivity, len(catalog.Points))
	for index, point := range catalog.Points {
		activity := byPoint[point.ID]
		dates := make([]string, 0, len(activity.recent))
		for date := range activity.recent {
			dates = append(dates, date)
		}
		slices.Sort(dates)
		recent := make([]RecommendationKnowledgeActivityDay, len(dates))
		for dayIndex, date := range dates {
			recent[dayIndex] = activity.recent[date]
		}
		result[index] = RecommendationKnowledgeActivity{
			KnowledgePointID: point.ID,
			Attempted:        int64(len(activity.attempted)),
			Correct:          int64(len(activity.correct)),
			LastTriedAt:      activity.last,
			RecentSeries:     recent,
		}
	}
	return result, nil
}
