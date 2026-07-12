package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	AlgorithmV1    = "ascendany_analytics_v1"
	MaxConfigBytes = 64 * 1024
)

type Config struct {
	AlgorithmVersion string             `json:"algorithmVersion"`
	AcceptedVerdicts []string           `json:"acceptedVerdicts"`
	Winsor           WinsorConfig       `json:"winsor"`
	HalfLifeDays     HalfLifeDaysConfig `json:"halfLifeDays"`
	Rating           RatingConfig       `json:"rating"`
}

type WinsorConfig struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type HalfLifeDaysConfig struct {
	Knowledge   float64 `json:"knowledge"`
	Accuracy    float64 `json:"accuracy"`
	Quality     float64 `json:"quality"`
	Flexibility float64 `json:"flexibility"`
	Proficiency float64 `json:"proficiency"`
}

type RatingConfig struct {
	Initial           int64 `json:"initial"`
	BinarySearchMin   int64 `json:"binarySearchMin"`
	BinarySearchMax   int64 `json:"binarySearchMax"`
	BinarySearchSteps int   `json:"binarySearchSteps"`
}

type ParsedConfig struct {
	Value     Config
	Canonical []byte
	SHA256    string
}

type rawConfig struct {
	AlgorithmVersion *string                `json:"algorithmVersion"`
	AcceptedVerdicts *[]string              `json:"acceptedVerdicts"`
	Winsor           *rawWinsorConfig       `json:"winsor"`
	HalfLifeDays     *rawHalfLifeDaysConfig `json:"halfLifeDays"`
	Rating           *rawRatingConfig       `json:"rating"`
}

type rawWinsorConfig struct {
	Low  *float64 `json:"low"`
	High *float64 `json:"high"`
}

type rawHalfLifeDaysConfig struct {
	Knowledge   *float64 `json:"knowledge"`
	Accuracy    *float64 `json:"accuracy"`
	Quality     *float64 `json:"quality"`
	Flexibility *float64 `json:"flexibility"`
	Proficiency *float64 `json:"proficiency"`
}

type rawRatingConfig struct {
	Initial           *int64 `json:"initial"`
	BinarySearchMin   *int64 `json:"binarySearchMin"`
	BinarySearchMax   *int64 `json:"binarySearchMax"`
	BinarySearchSteps *int   `json:"binarySearchSteps"`
}

func ParseConfig(data []byte) (ParsedConfig, error) {
	if len(data) > MaxConfigBytes {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", fmt.Errorf("config exceeds %d bytes", MaxConfigBytes))
	}
	var raw rawConfig
	if err := decodeClosedJSON(data, &raw); err != nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", err)
	}
	if raw.AlgorithmVersion == nil || raw.AcceptedVerdicts == nil || raw.Winsor == nil || raw.HalfLifeDays == nil || raw.Rating == nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", errors.New("every top-level config field is required"))
	}
	if raw.Winsor.Low == nil || raw.Winsor.High == nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", errors.New("winsor.low and winsor.high are required"))
	}
	if raw.HalfLifeDays.Knowledge == nil || raw.HalfLifeDays.Accuracy == nil || raw.HalfLifeDays.Quality == nil || raw.HalfLifeDays.Flexibility == nil || raw.HalfLifeDays.Proficiency == nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", errors.New("all five half-life values are required"))
	}
	if raw.Rating.Initial == nil || raw.Rating.BinarySearchMin == nil || raw.Rating.BinarySearchMax == nil || raw.Rating.BinarySearchSteps == nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", errors.New("all rating fields are required"))
	}

	configuration := Config{
		AlgorithmVersion: *raw.AlgorithmVersion,
		AcceptedVerdicts: append([]string(nil), (*raw.AcceptedVerdicts)...),
		Winsor: WinsorConfig{
			Low:  *raw.Winsor.Low,
			High: *raw.Winsor.High,
		},
		HalfLifeDays: HalfLifeDaysConfig{
			Knowledge:   *raw.HalfLifeDays.Knowledge,
			Accuracy:    *raw.HalfLifeDays.Accuracy,
			Quality:     *raw.HalfLifeDays.Quality,
			Flexibility: *raw.HalfLifeDays.Flexibility,
			Proficiency: *raw.HalfLifeDays.Proficiency,
		},
		Rating: RatingConfig{
			Initial:           *raw.Rating.Initial,
			BinarySearchMin:   *raw.Rating.BinarySearchMin,
			BinarySearchMax:   *raw.Rating.BinarySearchMax,
			BinarySearchSteps: *raw.Rating.BinarySearchSteps,
		},
	}
	if err := validateConfig(&configuration); err != nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "parse config", err)
	}
	canonical, err := json.Marshal(configuration)
	if err != nil {
		return ParsedConfig{}, analyticsError(ErrorInvalidConfiguration, true, "canonicalize config", err)
	}
	digest := sha256.Sum256(canonical)
	return ParsedConfig{
		Value:     configuration,
		Canonical: canonical,
		SHA256:    hex.EncodeToString(digest[:]),
	}, nil
}

func validateConfig(configuration *Config) error {
	if configuration.AlgorithmVersion != AlgorithmV1 {
		return fmt.Errorf("algorithmVersion must be %q", AlgorithmV1)
	}
	if len(configuration.AcceptedVerdicts) == 0 || len(configuration.AcceptedVerdicts) > 32 {
		return errors.New("acceptedVerdicts must contain between 1 and 32 values")
	}
	seenVerdicts := make(map[string]struct{}, len(configuration.AcceptedVerdicts))
	for _, verdict := range configuration.AcceptedVerdicts {
		if verdict == "" || strings.TrimSpace(verdict) != verdict || len(verdict) > 64 {
			return errors.New("accepted verdicts must be non-empty trimmed strings no longer than 64 bytes")
		}
		if _, exists := seenVerdicts[verdict]; exists {
			return fmt.Errorf("accepted verdict %q is duplicated", verdict)
		}
		seenVerdicts[verdict] = struct{}{}
	}
	sort.Strings(configuration.AcceptedVerdicts)

	if !finite(configuration.Winsor.Low) || !finite(configuration.Winsor.High) || configuration.Winsor.Low < 0 || configuration.Winsor.Low >= 0.5 || configuration.Winsor.High <= 0.5 || configuration.Winsor.High > 1 || configuration.Winsor.Low >= configuration.Winsor.High {
		return errors.New("winsor bounds must satisfy 0 <= low < 0.5 < high <= 1")
	}
	for _, field := range []struct {
		name  string
		value float64
	}{
		{name: "knowledge", value: configuration.HalfLifeDays.Knowledge},
		{name: "accuracy", value: configuration.HalfLifeDays.Accuracy},
		{name: "quality", value: configuration.HalfLifeDays.Quality},
		{name: "flexibility", value: configuration.HalfLifeDays.Flexibility},
		{name: "proficiency", value: configuration.HalfLifeDays.Proficiency},
	} {
		if !finite(field.value) || field.value <= 0 || field.value > 36500 {
			return fmt.Errorf("halfLifeDays.%s must be in (0, 36500]", field.name)
		}
	}
	if configuration.Rating.Initial < 0 || configuration.Rating.Initial > 100000 {
		return errors.New("rating.initial must be between 0 and 100000")
	}
	if configuration.Rating.BinarySearchMin < -100000 || configuration.Rating.BinarySearchMin >= configuration.Rating.Initial {
		return errors.New("rating.binarySearchMin must be at least -100000 and below initial")
	}
	if configuration.Rating.BinarySearchMax <= configuration.Rating.Initial || configuration.Rating.BinarySearchMax > 1000000 {
		return errors.New("rating.binarySearchMax must exceed initial and be at most 1000000")
	}
	if configuration.Rating.BinarySearchSteps < 1 || configuration.Rating.BinarySearchSteps > 128 {
		return errors.New("rating.binarySearchSteps must be between 1 and 128")
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
