package pipe

// Schema returns a Draft-07 JSON Schema for the pipe-mode QueryDSL.
//
// The shape:
//
//	root.QueryDSL = {mode:"pipe", from:..., pipe:[Stage]}
//	$defs.PipeStage = oneOf({op:"filter", ...} | {op:"project", ...} | ...)
//
// The op union is built from Catalog() so adding a new op only needs a
// catalog entry — the schema picks it up automatically.
//
// JSON-Schema-aware editors (Monaco, JSONForms, RJSF) get autocomplete and
// validation for free when handed this document.
func Schema() map[string]any {
	defs := map[string]any{
		"WhereClause":     whereClauseSchema(),
		"SelectField":     selectFieldSchema(),
		"OrderByClause":   orderByClauseSchema(),
		"AggregateClause": aggregateClauseSchema(),
		"PipeStage":       pipeStageUnionSchema(),
		"FromClause":      fromClauseSchema(),
	}

	return map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"$id":         "https://xraph.github.io/dql/schemas/query-pipe-dsl.json",
		"title":       "DQL (pipe mode)",
		"description": "JSON Schema for mode:\"pipe\" queries.",
		"type":        "object",
		"required":    []string{"mode", "from", "pipe"},
		"properties": map[string]any{
			"mode":       map[string]any{"const": "pipe"},
			"from":       refSchema("FromClause"),
			"projectId":  map[string]any{"type": "string"},
			"parameters": map[string]any{"type": "object", "additionalProperties": map[string]any{}},
			"pipe": map[string]any{
				"type":     "array",
				"items":    refSchema("PipeStage"),
				"minItems": 1,
			},
			"viz": map[string]any{"type": "object"},
		},
		"$defs": defs,
	}
}

func fromClauseSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{
				"type":     "object",
				"required": []string{"dataset"},
				"properties": map[string]any{
					"dataset": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func whereClauseSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string", "description": "DTL expression form"},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string"},
					"op": map[string]any{
						"type": "string",
						"enum": []string{
							"==", "!=", ">", "<", ">=", "<=",
							"in", "not_in", "like", "not_like",
							"is_null", "is_not_null", "between",
						},
					},
					"value": map[string]any{},
					"expr":  map[string]any{"type": "string"},
					"and":   map[string]any{"type": "array", "items": refSchema("WhereClause")},
					"or":    map[string]any{"type": "array", "items": refSchema("WhereClause")},
					"not":   refSchema("WhereClause"),
				},
				"additionalProperties": false,
			},
		},
	}
}

func selectFieldSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string"},
					"expr":  map[string]any{"type": "string"},
					"as":    map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
	}
}

func orderByClauseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": map[string]any{"type": "string"},
			"expr":  map[string]any{"type": "string"},
			"dir":   map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "asc"},
		},
		"additionalProperties": false,
	}
}

func aggregateClauseSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"fn", "as"},
		"properties": map[string]any{
			"fn": map[string]any{
				"type": "string",
				"enum": []string{
					"COUNT", "SUM", "AVG", "MIN", "MAX",
					"STDEV", "VARIANCE", "MEDIAN", "PERCENTILE",
					"FIRST", "LAST", "ARRAY_AGG", "STRING_AGG",
					"COUNTIF", "SUMIF", "EXPR",
				},
			},
			"field": map[string]any{"type": "string"},
			"expr":  map[string]any{"type": "string"},
			"args":  map[string]any{"type": "array"},
			"as":    map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

// pipeStageUnionSchema builds a discriminated oneOf from the catalog.
//
// Each branch merges the op's ConfigSchema with `op: "<name>"` so editors
// can dispatch on the literal `op` value.
func pipeStageUnionSchema() map[string]any {
	cat := Catalog()
	branches := make([]any, 0, len(cat))
	for _, m := range cat {
		// Copy the schema and inject the op discriminator.
		branch := cloneStringMap(m.ConfigSchema)
		props, _ := branch["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
			branch["properties"] = props
		}
		props["op"] = map[string]any{"const": m.Name}

		req, _ := branch["required"].([]string)
		req = append([]string{"op"}, req...)
		branch["required"] = req

		if m.Description != "" {
			branch["description"] = m.Description
		} else {
			branch["description"] = m.Summary
		}
		branch["title"] = m.Name
		branches = append(branches, branch)
	}
	return map[string]any{
		"oneOf": branches,
	}
}

// cloneStringMap returns a shallow clone of a JSON-Schema-shaped map,
// deep enough that callers can mutate the top-level "properties" / "required"
// without affecting the catalog source.
func cloneStringMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case map[string]any:
			out[k] = cloneStringMap(t)
		case []string:
			cp := make([]string, len(t))
			copy(cp, t)
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}
