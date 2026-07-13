package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

type knowledgeCatalogWire struct {
	TaxonomyID         *string                  `json:"taxonomyId"`
	KnowledgePoints    *[]knowledgePointWire    `json:"knowledgePoints"`
	ProblemAssignments *[]problemAssignmentWire `json:"problemAssignments"`
}

type knowledgePointWire struct {
	ID              *string   `json:"id"`
	Label           *string   `json:"label"`
	Description     *string   `json:"description"`
	PrerequisiteIDs *[]string `json:"prerequisiteIds"`
}

type problemAssignmentWire struct {
	Platform          *string       `json:"platform"`
	ProblemID         *string       `json:"problemId"`
	ProblemFactSHA256 *string       `json:"problemFactSha256"`
	Knowledge         *[]weightWire `json:"knowledge"`
}

type weightWire struct {
	KnowledgePointID *string `json:"knowledgePointId"`
	Weight           *string `json:"weight"`
}

type knowledgeCatalog struct {
	TaxonomyID  string
	Points      []knowledgePoint
	Assignments []problemAssignment
}

type knowledgePoint struct {
	ID              string
	Label           string
	Description     string
	PrerequisiteIDs []string
}

type problemAssignment struct {
	Platform          string
	ProblemID         string
	ProblemFactSHA256 string
	Knowledge         []catalogWeight
}

type catalogWeight struct {
	KnowledgePointID string
	Weight           float64
	raw              string
}

func parseKnowledgeCatalog(raw json.RawMessage) (knowledgeCatalog, json.RawMessage, string, error) {
	canonical, digest, err := canonicalObject(raw, "", maximumConfigurationBytes, "knowledge catalog")
	if err != nil {
		return knowledgeCatalog{}, nil, "", err
	}
	var wire knowledgeCatalogWire
	if err := decodeClosed(canonical, &wire); err != nil {
		return knowledgeCatalog{}, nil, "", err
	}
	if wire.TaxonomyID == nil || wire.KnowledgePoints == nil || wire.ProblemAssignments == nil {
		return knowledgeCatalog{}, nil, "", errors.New("every knowledge catalog field is required")
	}
	if !configurationKeyPattern.MatchString(*wire.TaxonomyID) {
		return knowledgeCatalog{}, nil, "", errors.New("taxonomyId is invalid")
	}
	if len(*wire.KnowledgePoints) < 1 || len(*wire.KnowledgePoints) > maximumKnowledgePoints {
		return knowledgeCatalog{}, nil, "", errors.New("knowledge point count is invalid")
	}
	points := make([]knowledgePoint, len(*wire.KnowledgePoints))
	pointIndex := make(map[string]int, len(points))
	for index, value := range *wire.KnowledgePoints {
		if value.ID == nil || value.Label == nil || value.Description == nil || value.PrerequisiteIDs == nil {
			return knowledgeCatalog{}, nil, "", fmt.Errorf("knowledgePoints[%d] requires every field", index)
		}
		if !configurationKeyPattern.MatchString(*value.ID) || !canonicalText(*value.Label, 256) || !canonicalText(*value.Description, 4096) {
			return knowledgeCatalog{}, nil, "", fmt.Errorf("knowledgePoints[%d] text is invalid", index)
		}
		if index > 0 && *value.ID <= points[index-1].ID {
			return knowledgeCatalog{}, nil, "", errors.New("knowledgePoints must be strictly sorted by ID")
		}
		prerequisites := slices.Clone(*value.PrerequisiteIDs)
		for prerequisiteIndex, prerequisite := range prerequisites {
			if !configurationKeyPattern.MatchString(prerequisite) || prerequisite == *value.ID ||
				(prerequisiteIndex > 0 && prerequisite <= prerequisites[prerequisiteIndex-1]) {
				return knowledgeCatalog{}, nil, "", fmt.Errorf("knowledge point %q prerequisites are invalid", *value.ID)
			}
		}
		points[index] = knowledgePoint{ID: *value.ID, Label: *value.Label, Description: *value.Description, PrerequisiteIDs: prerequisites}
		pointIndex[*value.ID] = index
	}
	if err := validateKnowledgeDAG(points, pointIndex); err != nil {
		return knowledgeCatalog{}, nil, "", err
	}
	assignments := make([]problemAssignment, len(*wire.ProblemAssignments))
	for index, value := range *wire.ProblemAssignments {
		if value.Platform == nil || value.ProblemID == nil || value.ProblemFactSHA256 == nil || value.Knowledge == nil {
			return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d] requires every field", index)
		}
		if *value.Platform != "pintia" || !pintia.ValidID(*value.ProblemID) ||
			!lowercaseSHA256Pattern.MatchString(*value.ProblemFactSHA256) || len(*value.Knowledge) < 1 {
			return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d] identity is invalid", index)
		}
		weights := make([]catalogWeight, len(*value.Knowledge))
		for weightIndex, value := range *value.Knowledge {
			if value.KnowledgePointID == nil || value.Weight == nil {
				return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d].knowledge[%d] requires every field", index, weightIndex)
			}
			if _, exists := pointIndex[*value.KnowledgePointID]; !exists ||
				(weightIndex > 0 && *value.KnowledgePointID <= weights[weightIndex-1].KnowledgePointID) {
				return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d] knowledge order or reference is invalid", index)
			}
			rational, ok := new(big.Rat).SetString(*value.Weight)
			parsed, parseErr := strconv.ParseFloat(*value.Weight, 64)
			if !canonicalWeightPattern.MatchString(*value.Weight) || len(*value.Weight) > 128 ||
				!ok || parseErr != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
				rational.Sign() <= 0 || rational.Cmp(big.NewRat(1, 1)) > 0 {
				return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d] knowledge weight is invalid", index)
			}
			weights[weightIndex] = catalogWeight{KnowledgePointID: *value.KnowledgePointID, Weight: parsed, raw: *value.Weight}
		}
		if !exactUnitWeightSum(weights) {
			return knowledgeCatalog{}, nil, "", fmt.Errorf("problemAssignments[%d] knowledge weights must sum exactly to one", index)
		}
		assignments[index] = problemAssignment{
			Platform: *value.Platform, ProblemID: *value.ProblemID,
			ProblemFactSHA256: *value.ProblemFactSHA256, Knowledge: weights,
		}
		if index > 0 && compareAssignment(assignments[index-1], assignments[index]) >= 0 {
			return knowledgeCatalog{}, nil, "", errors.New("problemAssignments must be strictly sorted by platform, problemId, and problemFactSha256")
		}
	}
	return knowledgeCatalog{TaxonomyID: *wire.TaxonomyID, Points: points, Assignments: assignments}, canonical, digest, nil
}

func validateKnowledgeDAG(points []knowledgePoint, indices map[string]int) error {
	state := make([]uint8, len(points))
	var visit func(int) error
	visit = func(index int) error {
		if state[index] == 1 {
			return errors.New("knowledge prerequisite graph contains a cycle")
		}
		if state[index] == 2 {
			return nil
		}
		state[index] = 1
		for _, prerequisiteID := range points[index].PrerequisiteIDs {
			prerequisite, exists := indices[prerequisiteID]
			if !exists {
				return fmt.Errorf("knowledge point %q references missing prerequisite %q", points[index].ID, prerequisiteID)
			}
			if err := visit(prerequisite); err != nil {
				return err
			}
		}
		state[index] = 2
		return nil
	}
	for index := range points {
		if err := visit(index); err != nil {
			return err
		}
	}
	return nil
}

func compareAssignment(left, right problemAssignment) int {
	if comparison := strings.Compare(left.Platform, right.Platform); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(left.ProblemID, right.ProblemID); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.ProblemFactSHA256, right.ProblemFactSHA256)
}
