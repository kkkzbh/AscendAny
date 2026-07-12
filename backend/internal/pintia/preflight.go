package pintia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

type preflightKind uint8

const (
	preflightNull preflightKind = 1 << iota
	preflightBoolean
	preflightNumber
	preflightString
	preflightArray
	preflightObject
)

type preflightSchema struct {
	kinds      preflightKind
	properties map[string]*preflightSchema
	items      *preflightSchema
}

type preflightCompiler struct {
	root      map[string]any
	cache     map[string]*preflightSchema
	resolving map[string]struct{}
}

func compilePreflightSchema(document any) (*preflightSchema, error) {
	root, ok := document.(map[string]any)
	if !ok {
		return nil, errors.New("authoritative schema root must be an object")
	}
	compiler := preflightCompiler{
		root:      root,
		cache:     make(map[string]*preflightSchema),
		resolving: make(map[string]struct{}),
	}
	compiled, err := compiler.compile(root)
	if err != nil {
		return nil, fmt.Errorf("compile schema-aware preflight: %w", err)
	}
	if compiled.kinds&preflightObject == 0 {
		return nil, errors.New("authoritative schema root must accept an object")
	}
	return compiled, nil
}

func (compiler *preflightCompiler) compile(schema map[string]any) (*preflightSchema, error) {
	if referenceValue, exists := schema["$ref"]; exists {
		reference, ok := referenceValue.(string)
		if !ok {
			return nil, errors.New("$ref must be a string")
		}
		return compiler.compileReference(reference)
	}
	if branchesValue, exists := schema["anyOf"]; exists {
		branches, ok := branchesValue.([]any)
		if !ok || len(branches) == 0 {
			return nil, errors.New("anyOf must be a non-empty array")
		}
		merged := &preflightSchema{}
		for index, branchValue := range branches {
			branch, ok := branchValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("anyOf[%d] must be an object", index)
			}
			compiled, err := compiler.compile(branch)
			if err != nil {
				return nil, fmt.Errorf("anyOf[%d]: %w", index, err)
			}
			if err := mergePreflightSchema(merged, compiled); err != nil {
				return nil, fmt.Errorf("anyOf[%d]: %w", index, err)
			}
		}
		return merged, nil
	}

	kinds, err := preflightKinds(schema)
	if err != nil {
		return nil, err
	}
	compiled := &preflightSchema{kinds: kinds}
	if kinds&preflightObject != 0 {
		closed, ok := schema["additionalProperties"].(bool)
		if !ok || closed {
			return nil, errors.New("every object reachable from the snapshot root must set additionalProperties to false")
		}
		propertiesValue, ok := schema["properties"]
		if !ok {
			return nil, errors.New("closed object schema must declare properties")
		}
		properties, ok := propertiesValue.(map[string]any)
		if !ok {
			return nil, errors.New("properties must be an object")
		}
		compiled.properties = make(map[string]*preflightSchema, len(properties))
		for name, propertyValue := range properties {
			property, ok := propertyValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("property %q schema must be an object", name)
			}
			child, err := compiler.compile(property)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			compiled.properties[name] = child
		}
	}
	if kinds&preflightArray != 0 {
		itemsValue, ok := schema["items"]
		if !ok {
			return nil, errors.New("array schema must declare items")
		}
		items, ok := itemsValue.(map[string]any)
		if !ok {
			return nil, errors.New("array items must be an object schema")
		}
		compiled.items, err = compiler.compile(items)
		if err != nil {
			return nil, fmt.Errorf("array items: %w", err)
		}
	}
	return compiled, nil
}

func (compiler *preflightCompiler) compileReference(reference string) (*preflightSchema, error) {
	if compiled, exists := compiler.cache[reference]; exists {
		return compiled, nil
	}
	if _, exists := compiler.resolving[reference]; exists {
		return nil, fmt.Errorf("recursive reference %q is not allowed", reference)
	}
	compiler.resolving[reference] = struct{}{}
	defer delete(compiler.resolving, reference)

	resolved, err := resolveLocalSchemaReference(compiler.root, reference)
	if err != nil {
		return nil, err
	}
	compiled, err := compiler.compile(resolved)
	if err != nil {
		return nil, fmt.Errorf("reference %q: %w", reference, err)
	}
	compiler.cache[reference] = compiled
	return compiled, nil
}

func resolveLocalSchemaReference(root map[string]any, reference string) (map[string]any, error) {
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("only local JSON Pointer references are allowed, got %q", reference)
	}
	var current any = root
	for _, encoded := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("reference %q traverses a non-object", reference)
		}
		current, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("reference %q does not exist", reference)
		}
	}
	resolved, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("reference %q does not resolve to an object schema", reference)
	}
	return resolved, nil
}

func preflightKinds(schema map[string]any) (preflightKind, error) {
	if typeValue, exists := schema["type"]; exists {
		switch typed := typeValue.(type) {
		case string:
			return preflightKindForName(typed)
		case []any:
			var kinds preflightKind
			for index, value := range typed {
				name, ok := value.(string)
				if !ok {
					return 0, fmt.Errorf("type[%d] must be a string", index)
				}
				kind, err := preflightKindForName(name)
				if err != nil {
					return 0, err
				}
				kinds |= kind
			}
			if kinds == 0 {
				return 0, errors.New("type array must not be empty")
			}
			return kinds, nil
		default:
			return 0, errors.New("type must be a string or string array")
		}
	}
	constant, exists := schema["const"]
	if !exists {
		return 0, errors.New("schema must declare type, const, $ref, or anyOf")
	}
	switch constant.(type) {
	case nil:
		return preflightNull, nil
	case bool:
		return preflightBoolean, nil
	case json.Number:
		return preflightNumber, nil
	case string:
		return preflightString, nil
	default:
		return 0, errors.New("preflight supports only scalar const values")
	}
}

func preflightKindForName(name string) (preflightKind, error) {
	switch name {
	case "null":
		return preflightNull, nil
	case "boolean":
		return preflightBoolean, nil
	case "integer", "number":
		return preflightNumber, nil
	case "string":
		return preflightString, nil
	case "array":
		return preflightArray, nil
	case "object":
		return preflightObject, nil
	default:
		return 0, fmt.Errorf("unsupported JSON Schema type %q", name)
	}
}

func mergePreflightSchema(target, branch *preflightSchema) error {
	if branch.kinds&preflightObject != 0 {
		if target.properties != nil {
			return errors.New("multiple object branches are ambiguous")
		}
		target.properties = branch.properties
	}
	if branch.kinds&preflightArray != 0 {
		if target.items != nil {
			return errors.New("multiple array branches are ambiguous")
		}
		target.items = branch.items
	}
	target.kinds |= branch.kinds
	return nil
}

func validatePreflightArrayCoverage(root *preflightSchema, arrayLimits map[string]int) error {
	paths := make(map[string]struct{})
	collectPreflightArrayPaths(root, "$", paths)
	for path := range paths {
		if _, exists := arrayLimits[path]; !exists {
			return fmt.Errorf("authoritative schema array %s has no configured limit", path)
		}
	}
	for path := range arrayLimits {
		if _, exists := paths[path]; !exists {
			return fmt.Errorf("configured array limit %s does not exist in the authoritative schema", path)
		}
	}
	return nil
}

func collectPreflightArrayPaths(schema *preflightSchema, path string, paths map[string]struct{}) {
	if schema.kinds&preflightArray != 0 {
		paths[path] = struct{}{}
		collectPreflightArrayPaths(schema.items, path+"[]", paths)
	}
	if schema.kinds&preflightObject == 0 {
		return
	}
	keys := make([]string, 0, len(schema.properties))
	for key := range schema.properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		collectPreflightArrayPaths(schema.properties[key], path+"."+key, paths)
	}
}

type preflightScanner struct {
	ctx         context.Context
	decoder     *json.Decoder
	limits      Limits
	arrayLimits map[string]int
	totalNodes  int64
	stringBytes int64
}

func validateStreamingPreflight(
	ctx context.Context,
	payload []byte,
	root *preflightSchema,
	limits Limits,
	arrayLimits map[string]int,
) error {
	if err := validateUTF8(ctx, payload); err != nil {
		return err
	}
	reader := &contextReader{ctx: ctx, reader: bytes.NewReader(payload)}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	scanner := preflightScanner{
		ctx:         ctx,
		decoder:     decoder,
		limits:      limits,
		arrayLimits: arrayLimits,
	}
	if err := scanner.scanValue(root, "$", "$", 0); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if contextErr := context.Cause(ctx); contextErr != nil {
			return contextErr
		}
		if err == nil {
			return validationError(ErrorMalformedJSON, "$", "multiple JSON values are not allowed")
		}
		return validationError(ErrorMalformedJSON, "$", "%v", err)
	}
	return nil
}

func validateUTF8(ctx context.Context, payload []byte) error {
	const chunkBytes = 64 << 10
	for offset := 0; offset < len(payload); {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		end := min(offset+chunkBytes, len(payload))
		if end < len(payload) {
			for end > offset && !utf8.RuneStart(payload[end]) {
				end--
			}
			if end == offset {
				end = min(offset+chunkBytes, len(payload))
			}
		}
		if !utf8.Valid(payload[offset:end]) {
			return validationError(ErrorMalformedJSON, "$", "input is not valid UTF-8")
		}
		offset = end
	}
	return nil
}

func (scanner *preflightScanner) scanValue(schema *preflightSchema, path, schemaPath string, depth int) error {
	if err := scanner.addNode(path); err != nil {
		return err
	}
	token, err := scanner.nextToken()
	if err != nil {
		if contextErr := context.Cause(scanner.ctx); contextErr != nil {
			return contextErr
		}
		return validationError(ErrorMalformedJSON, path, "%v", err)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if isDelimiter {
		containerDepth := depth + 1
		if containerDepth > scanner.limits.MaxJSONDepth {
			return validationError(
				ErrorLimitExceeded,
				path,
				"JSON nesting depth is %d; maximum is %d",
				containerDepth,
				scanner.limits.MaxJSONDepth,
			)
		}
		switch delimiter {
		case '{':
			if schema == nil || schema.kinds&preflightObject == 0 {
				return scanner.scanObject(nil, path, "", containerDepth)
			}
			return scanner.scanObject(schema, path, schemaPath, containerDepth)
		case '[':
			if schema == nil || schema.kinds&preflightArray == 0 {
				return scanner.scanArray(nil, path, "", containerDepth)
			}
			return scanner.scanArray(schema, path, schemaPath, containerDepth)
		default:
			return validationError(ErrorMalformedJSON, path, "unexpected delimiter %q", delimiter)
		}
	}

	switch value := token.(type) {
	case string:
		maximum := scanner.limits.MaxStringBytes
		if schemaPath == "$.submissions[].code" {
			maximum = scanner.limits.MaxCodeBytes
		}
		return scanner.addString(path, value, maximum)
	case json.Number:
		if _, err := parseDecimalBytes([]byte(value.String())); err != nil {
			return validationError(ErrorLimitExceeded, path, "numeric canonicalization rejected: %v", err)
		}
	case nil, bool:
		return nil
	default:
		return validationError(ErrorMalformedJSON, path, "unsupported JSON token type %T", token)
	}
	return nil
}

func (scanner *preflightScanner) scanObject(schema *preflightSchema, path, schemaPath string, depth int) error {
	seen := make(map[string]struct{})
	if schema != nil {
		seen = make(map[string]struct{}, len(schema.properties))
	}
	for scanner.decoder.More() {
		if err := context.Cause(scanner.ctx); err != nil {
			return err
		}
		keyToken, err := scanner.nextToken()
		if err != nil {
			if contextErr := context.Cause(scanner.ctx); contextErr != nil {
				return contextErr
			}
			return validationError(ErrorMalformedJSON, path, "%v", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return validationError(ErrorMalformedJSON, path, "object key is not a string")
		}
		keyPath := preflightFieldPath(path, key)
		if _, exists := seen[key]; exists {
			return validationError(ErrorMalformedJSON, keyPath, "duplicate object key %s", preflightFieldName(key))
		}
		seen[key] = struct{}{}
		if err := scanner.addNode(keyPath); err != nil {
			return err
		}
		if err := scanner.addString(keyPath, key, scanner.limits.MaxStringBytes); err != nil {
			return err
		}
		var child *preflightSchema
		childSchemaPath := ""
		if schema != nil {
			var exists bool
			child, exists = schema.properties[key]
			if !exists {
				return validationError(ErrorSchemaViolation, keyPath, "unknown object field %s", preflightFieldName(key))
			}
			childSchemaPath = schemaPath + "." + key
		}
		if err := scanner.scanValue(child, keyPath, childSchemaPath, depth); err != nil {
			return err
		}
	}
	closing, err := scanner.nextToken()
	if err != nil {
		if contextErr := context.Cause(scanner.ctx); contextErr != nil {
			return contextErr
		}
		return validationError(ErrorMalformedJSON, path, "%v", err)
	}
	if closing != json.Delim('}') {
		return validationError(ErrorMalformedJSON, path, "object is not closed")
	}
	return nil
}

func preflightFieldPath(parent, key string) string {
	const maximumPathFieldBytes = 128
	if len(key) > maximumPathFieldBytes {
		return parent + ".<oversized-field-name>"
	}
	return parent + "." + key
}

func preflightFieldName(key string) string {
	const maximumDetailFieldBytes = 128
	if len(key) <= maximumDetailFieldBytes {
		return fmt.Sprintf("%q", key)
	}
	return fmt.Sprintf("%q... (%d UTF-8 bytes)", key[:maximumDetailFieldBytes], len(key))
}

func (scanner *preflightScanner) scanArray(schema *preflightSchema, path, schemaPath string, depth int) error {
	maximum := int64(0)
	if schema != nil {
		configured, exists := scanner.arrayLimits[schemaPath]
		if !exists {
			return fmt.Errorf("preflight invariant: array %s has no configured limit", schemaPath)
		}
		maximum = int64(configured)
	}
	count := int64(0)
	for scanner.decoder.More() {
		if err := context.Cause(scanner.ctx); err != nil {
			return err
		}
		count++
		if maximum > 0 && count > maximum {
			return validationError(ErrorLimitExceeded, path, "contains more than %d items", maximum)
		}
		childPath := fmt.Sprintf("%s[%d]", path, count-1)
		var child *preflightSchema
		childSchemaPath := ""
		if schema != nil {
			child = schema.items
			childSchemaPath = schemaPath + "[]"
		}
		if err := scanner.scanValue(child, childPath, childSchemaPath, depth); err != nil {
			return err
		}
	}
	closing, err := scanner.nextToken()
	if err != nil {
		if contextErr := context.Cause(scanner.ctx); contextErr != nil {
			return contextErr
		}
		return validationError(ErrorMalformedJSON, path, "%v", err)
	}
	if closing != json.Delim(']') {
		return validationError(ErrorMalformedJSON, path, "array is not closed")
	}
	return nil
}

func (scanner *preflightScanner) nextToken() (json.Token, error) {
	if err := context.Cause(scanner.ctx); err != nil {
		return nil, err
	}
	token, err := scanner.decoder.Token()
	if contextErr := context.Cause(scanner.ctx); contextErr != nil {
		return nil, contextErr
	}
	return token, err
}

func (scanner *preflightScanner) addNode(path string) error {
	if scanner.totalNodes >= scanner.limits.MaxTotalNodes {
		return validationError(
			ErrorLimitExceeded,
			path,
			"JSON node count exceeds maximum %d",
			scanner.limits.MaxTotalNodes,
		)
	}
	scanner.totalNodes++
	return nil
}

func (scanner *preflightScanner) addString(path, value string, maximum int) error {
	size := len(value)
	if size > maximum {
		return validationError(
			ErrorLimitExceeded,
			path,
			"contains %d UTF-8 bytes; maximum is %d",
			size,
			maximum,
		)
	}
	if int64(size) > scanner.limits.MaxTotalStringBytes-scanner.stringBytes {
		return validationError(
			ErrorLimitExceeded,
			path,
			"total decoded string bytes exceed maximum %d",
			scanner.limits.MaxTotalStringBytes,
		)
	}
	scanner.stringBytes += int64(size)
	return nil
}
