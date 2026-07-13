package databasecontract

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
)

const inventorySQL = `
WITH inventory(object_key) AS (
    SELECT 'relation:' || relation.relkind::text || ':' || relation.relname
    FROM pg_class AS relation
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany'
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f')
    UNION ALL
    SELECT 'sequence:' || relation.relname
    FROM pg_class AS relation
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany' AND relation.relkind = 'S'
    UNION ALL
    SELECT 'index:' || relation.relname
    FROM pg_class AS relation
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany' AND relation.relkind IN ('i', 'I')
    UNION ALL
    SELECT 'type:' || type_row.typname
    FROM pg_type AS type_row
    JOIN pg_namespace AS namespace ON namespace.oid = type_row.typnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'routine:' || routine_row.prokind::text || ':' || routine_row.proname ||
           '(' || pg_get_function_identity_arguments(routine_row.oid) || ')'
    FROM pg_proc AS routine_row
    JOIN pg_namespace AS namespace ON namespace.oid = routine_row.pronamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'trigger:' || trigger_row.tgname
    FROM pg_trigger AS trigger_row
    JOIN pg_class AS relation ON relation.oid = trigger_row.tgrelid
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany' AND NOT trigger_row.tgisinternal
    UNION ALL
    SELECT 'constraint:' || constraint_row.contype::text || ':' || constraint_row.conname
    FROM pg_constraint AS constraint_row
    JOIN pg_namespace AS namespace ON namespace.oid = constraint_row.connamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'policy:' || policy_row.polname
    FROM pg_policy AS policy_row
    JOIN pg_class AS relation ON relation.oid = policy_row.polrelid
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'rule:' || rule_row.rulename
    FROM pg_rewrite AS rule_row
    JOIN pg_class AS relation ON relation.oid = rule_row.ev_class
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany' AND rule_row.rulename <> '_RETURN'
    UNION ALL
    SELECT 'statistics:' || statistics_row.stxname
    FROM pg_statistic_ext AS statistics_row
    JOIN pg_namespace AS namespace ON namespace.oid = statistics_row.stxnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'collation:' || collation_row.collname
    FROM pg_collation AS collation_row
    JOIN pg_namespace AS namespace ON namespace.oid = collation_row.collnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'conversion:' || conversion_row.conname
    FROM pg_conversion AS conversion_row
    JOIN pg_namespace AS namespace ON namespace.oid = conversion_row.connamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'operator:' || operator_row.oprname || '(' ||
           operator_row.oprleft::regtype::text || ',' || operator_row.oprright::regtype::text || ')'
    FROM pg_operator AS operator_row
    JOIN pg_namespace AS namespace ON namespace.oid = operator_row.oprnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'operator_class:' || operator_class_row.opcname
    FROM pg_opclass AS operator_class_row
    JOIN pg_namespace AS namespace ON namespace.oid = operator_class_row.opcnamespace
    WHERE namespace.nspname = 'ascendany'
    UNION ALL
    SELECT 'operator_family:' || operator_family_row.opfname
    FROM pg_opfamily AS operator_family_row
    JOIN pg_namespace AS namespace ON namespace.oid = operator_family_row.opfnamespace
    WHERE namespace.nspname = 'ascendany'
)
SELECT object_key FROM inventory ORDER BY object_key`

var (
	createLinePattern      = regexp.MustCompile(`(?m)^CREATE [^\n]+`)
	createTablePattern     = regexp.MustCompile(`(?m)^CREATE TABLE ascendany\.([a-z][a-z0-9_]*) \($`)
	tableBlockPattern      = regexp.MustCompile(`(?ms)^CREATE TABLE ascendany\.([a-z][a-z0-9_]*) \(\n(.*?)^\);`)
	createFunctionPattern  = regexp.MustCompile(`(?ms)^CREATE FUNCTION ascendany\.([a-z][a-z0-9_]*)\((.*?)\)\nRETURNS\b`)
	replaceFunctionPattern = regexp.MustCompile(`(?ms)^CREATE OR REPLACE FUNCTION ascendany\.([a-z][a-z0-9_]*)\((.*?)\)\nRETURNS\b`)
	functionArguments      = regexp.MustCompile(`^(?:[a-z][a-z0-9_]* (?:uuid|bigint|boolean|text)(?:, [a-z][a-z0-9_]* (?:uuid|bigint|boolean|text))*)?$`)
	createIndexPattern     = regexp.MustCompile(`(?m)^CREATE (?:UNIQUE )?INDEX ([a-z][a-z0-9_]*)$`)
	createTriggerPattern   = regexp.MustCompile(`(?ms)^CREATE (CONSTRAINT )?TRIGGER ([a-z][a-z0-9_]*)\n(.*?);`)
	triggerTablePattern    = regexp.MustCompile(`\bON ascendany\.([a-z][a-z0-9_]*)\b`)
	namedConstraintPattern = regexp.MustCompile(`(?s)\bCONSTRAINT\s+([a-z][a-z0-9_]*)\s+(CHECK|UNIQUE|PRIMARY\s+KEY|FOREIGN\s+KEY|EXCLUDE)\b`)
	primaryKeyPattern      = regexp.MustCompile(`\bPRIMARY\s+KEY\b`)
	inlineUniquePattern    = regexp.MustCompile(`(?m)^    ([a-z][a-z0-9_]*) [^\n]*\bUNIQUE\b`)
	identityPattern        = regexp.MustCompile(`(?m)^    ([a-z][a-z0-9_]*) [^\n]*\bGENERATED ALWAYS AS IDENTITY\b`)
	sequenceNamePattern    = regexp.MustCompile(`(?m)^        SEQUENCE NAME ascendany\.([a-z][a-z0-9_]*)$`)
	nextTableItemPattern   = regexp.MustCompile(`(?m)^    (?:[a-z][a-z0-9_]*|CONSTRAINT|PRIMARY KEY|UNIQUE|FOREIGN KEY|CHECK)\b`)
	unnamedConstraint      = regexp.MustCompile(`(?m)^    (?:UNIQUE|FOREIGN KEY|CHECK)\b`)
	inlineReferenceOrCheck = regexp.MustCompile(`(?m)^    [a-z][a-z0-9_]* [^\n]*\b(?:REFERENCES|CHECK)\b`)
	dynamicImmutableBlock  = regexp.MustCompile(`(?ms)DO \$immutable_triggers\$.*?immutable_tables constant text\[\] := ARRAY\[(.*?)\];.*?table_name \|\| '_immutable_rows'.*?table_name \|\| '_immutable_truncate'.*?\$immutable_triggers\$;`)
	quotedIdentifier       = regexp.MustCompile(`'([a-z][a-z0-9_]*)'`)
)

func expectedInventory() ([]string, error) {
	definitions, err := migrate.Embedded()
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{})
	for _, definition := range definitions {
		if err := addMigrationInventory(keys, definition.SQL); err != nil {
			return nil, fmt.Errorf("migration %d (%s): %w", definition.Version, definition.Name, err)
		}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result, nil
}

func addMigrationInventory(keys map[string]struct{}, sql string) error {
	for _, line := range createLinePattern.FindAllString(sql, -1) {
		if !createTablePattern.MatchString(line) && !strings.HasPrefix(line, "CREATE FUNCTION ascendany.") &&
			!strings.HasPrefix(line, "CREATE OR REPLACE FUNCTION ascendany.") &&
			!createIndexPattern.MatchString(line) && !strings.HasPrefix(line, "CREATE TRIGGER ") &&
			!strings.HasPrefix(line, "CREATE CONSTRAINT TRIGGER ") {
			return fmt.Errorf("unsupported schema creation statement %q", line)
		}
	}
	functionDefinitions := createFunctionPattern.FindAllStringSubmatch(sql, -1)
	replacementFunctionDefinitions := replaceFunctionPattern.FindAllStringSubmatch(sql, -1)
	functionLineCount := 0
	replacementFunctionLineCount := 0
	for _, line := range createLinePattern.FindAllString(sql, -1) {
		if strings.HasPrefix(line, "CREATE FUNCTION ascendany.") {
			functionLineCount++
		}
		if strings.HasPrefix(line, "CREATE OR REPLACE FUNCTION ascendany.") {
			replacementFunctionLineCount++
		}
	}
	if len(functionDefinitions) != functionLineCount {
		return fmt.Errorf("CREATE FUNCTION block is not in the canonical migration form")
	}
	if len(replacementFunctionDefinitions) != replacementFunctionLineCount {
		return fmt.Errorf("CREATE OR REPLACE FUNCTION block is not in the canonical migration form")
	}

	blocks := tableBlockPattern.FindAllStringSubmatch(sql, -1)
	if len(blocks) != len(createTablePattern.FindAllStringSubmatch(sql, -1)) {
		return fmt.Errorf("CREATE TABLE block is not in the canonical migration form")
	}
	for _, block := range blocks {
		tableName, body := block[1], block[2]
		for _, key := range []string{
			"relation:r:" + tableName,
			"type:" + tableName,
			"type:_" + tableName,
		} {
			if err := addInventoryKey(keys, key); err != nil {
				return err
			}
		}
		if unnamedConstraint.MatchString(body) || inlineReferenceOrCheck.MatchString(body) {
			return fmt.Errorf("table %s contains an unnamed CHECK, UNIQUE, or FOREIGN KEY", tableName)
		}
		primaryCount := len(primaryKeyPattern.FindAllStringIndex(body, -1))
		namedPrimaryCount := 0
		for _, match := range namedConstraintPattern.FindAllStringSubmatch(body, -1) {
			if strings.Join(strings.Fields(match[2]), " ") == "PRIMARY KEY" {
				namedPrimaryCount++
			}
		}
		if primaryCount-namedPrimaryCount > 1 {
			return fmt.Errorf("table %s contains multiple unnamed primary keys", tableName)
		}
		if primaryCount-namedPrimaryCount == 1 {
			name := tableName + "_pkey"
			if err := addConstraintAndIndex(keys, "p", name, true); err != nil {
				return err
			}
		}
		for _, match := range inlineUniquePattern.FindAllStringSubmatch(body, -1) {
			name := tableName + "_" + match[1] + "_key"
			if err := addConstraintAndIndex(keys, "u", name, true); err != nil {
				return err
			}
		}
		identities := identityPattern.FindAllStringSubmatchIndex(body, -1)
		for _, match := range identities {
			column := body[match[2]:match[3]]
			tail := body[match[1]:]
			if boundary := nextTableItemPattern.FindStringIndex(tail); boundary != nil {
				tail = tail[:boundary[0]]
			}
			sequenceName := tableName + "_" + column + "_seq"
			if explicit := sequenceNamePattern.FindStringSubmatch(tail); explicit != nil {
				sequenceName = explicit[1]
			}
			if err := addInventoryKey(keys, "sequence:"+sequenceName); err != nil {
				return err
			}
		}
	}

	for _, match := range functionDefinitions {
		signature := strings.Join(strings.Fields(match[2]), " ")
		if !functionArguments.MatchString(signature) {
			return fmt.Errorf("function %s uses a non-canonical identity signature %q", match[1], signature)
		}
		if err := addInventoryKey(keys, "routine:f:"+match[1]+"("+signature+")"); err != nil {
			return err
		}
	}
	for _, match := range replacementFunctionDefinitions {
		signature := strings.Join(strings.Fields(match[2]), " ")
		if !functionArguments.MatchString(signature) {
			return fmt.Errorf("replacement function %s uses a non-canonical identity signature %q", match[1], signature)
		}
		key := "routine:f:" + match[1] + "(" + signature + ")"
		if _, exists := keys[key]; !exists {
			return fmt.Errorf("CREATE OR REPLACE FUNCTION does not replace an earlier routine: %s", key)
		}
	}
	for _, match := range createIndexPattern.FindAllStringSubmatch(sql, -1) {
		if err := addInventoryKey(keys, "index:"+match[1]); err != nil {
			return err
		}
	}
	for _, match := range namedConstraintPattern.FindAllStringSubmatch(sql, -1) {
		kind := strings.Join(strings.Fields(match[2]), " ")
		constraintType := map[string]string{
			"CHECK": "c", "UNIQUE": "u", "PRIMARY KEY": "p", "FOREIGN KEY": "f", "EXCLUDE": "x",
		}[kind]
		if err := addConstraintAndIndex(keys, constraintType, match[1], constraintType == "p" || constraintType == "u" || constraintType == "x"); err != nil {
			return err
		}
	}
	for _, match := range createTriggerPattern.FindAllStringSubmatch(sql, -1) {
		if triggerTablePattern.FindStringSubmatch(match[3]) == nil {
			return fmt.Errorf("trigger %s does not identify an ascendany table", match[2])
		}
		if err := addInventoryKey(keys, "trigger:"+match[2]); err != nil {
			return err
		}
		if match[1] != "" {
			if err := addInventoryKey(keys, "constraint:t:"+match[2]); err != nil {
				return err
			}
		}
	}
	if dynamic := dynamicImmutableBlock.FindStringSubmatch(sql); dynamic != nil {
		for _, match := range quotedIdentifier.FindAllStringSubmatch(dynamic[1], -1) {
			for _, suffix := range []string{"_immutable_rows", "_immutable_truncate"} {
				if err := addInventoryKey(keys, "trigger:"+match[1]+suffix); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func addConstraintAndIndex(keys map[string]struct{}, constraintType, name string, hasIndex bool) error {
	if err := addInventoryKey(keys, "constraint:"+constraintType+":"+name); err != nil {
		return err
	}
	if hasIndex {
		return addInventoryKey(keys, "index:"+name)
	}
	return nil
}

func addInventoryKey(keys map[string]struct{}, key string) error {
	identifier, found := inventoryKeyIdentifier(key)
	if !found {
		return fmt.Errorf("unsupported database inventory key %q", key)
	}
	if len(identifier) > 63 {
		return fmt.Errorf("PostgreSQL identifier exceeds 63 bytes: %s", identifier)
	}
	if _, exists := keys[key]; exists {
		return fmt.Errorf("%w: %s", errDuplicateInventoryKey, key)
	}
	keys[key] = struct{}{}
	return nil
}

func inventoryKeyIdentifier(key string) (string, bool) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 2 {
		return "", false
	}
	var identifier string
	switch parts[0] {
	case "relation", "routine", "constraint":
		if len(parts) != 3 {
			return "", false
		}
		identifier = parts[2]
	default:
		identifier = strings.TrimPrefix(key, parts[0]+":")
	}
	if parts[0] == "routine" {
		var found bool
		identifier, _, found = strings.Cut(identifier, "(")
		if !found {
			return "", false
		}
	}
	return identifier, identifier != ""
}
