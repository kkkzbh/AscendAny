package analytics

import (
	"math"
	"sort"
)

func percentileScores(raw map[int64]float64, low, high float64) map[int64]float64 {
	result := make(map[int64]float64, len(raw))
	if len(raw) == 0 {
		return result
	}
	values := make([]float64, 0, len(raw))
	for _, value := range raw {
		values = append(values, value)
	}
	sort.Float64s(values)
	lower := quantileSorted(values, low)
	upper := quantileSorted(values, high)
	clipped := make([]float64, 0, len(values))
	byActor := make(map[int64]float64, len(raw))
	for actorID, value := range raw {
		value = math.Max(lower, math.Min(upper, value))
		byActor[actorID] = value
		clipped = append(clipped, value)
	}
	sort.Float64s(clipped)
	for actorID, value := range byActor {
		less := sort.Search(len(clipped), func(index int) bool { return clipped[index] >= value })
		upperIndex := sort.Search(len(clipped), func(index int) bool { return clipped[index] > value })
		equal := upperIndex - less
		score := (float64(less) + 0.5*float64(equal)) / float64(len(clipped)) * 100
		result[actorID] = round6(score)
	}
	return result
}

func quantileSorted(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	position := float64(len(values)-1) * percentile
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func integerDistribution(values []int64) *DistributionStats {
	if len(values) == 0 {
		return nil
	}
	sortedValues := append([]int64(nil), values...)
	sort.Slice(sortedValues, func(i, j int) bool { return sortedValues[i] < sortedValues[j] })
	floatValues := make([]float64, len(sortedValues))
	for index, value := range sortedValues {
		floatValues[index] = float64(value)
	}
	return &DistributionStats{
		Count:  int64(len(sortedValues)),
		Min:    sortedValues[0],
		Median: round6(quantileSorted(floatValues, 0.5)),
		P95:    round6(quantileSorted(floatValues, 0.95)),
		Max:    sortedValues[len(sortedValues)-1],
	}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	return quantileSorted(ordered, 0.5)
}

func round6(value float64) float64 {
	if value == 0 {
		return 0
	}
	return math.Round(value*1_000_000) / 1_000_000
}

func pointerFloat(value float64) *float64 {
	value = round6(value)
	return &value
}
