package expand

import (
	"context"
	"fmt"
	"strings"

	"github.com/xraph/dql/dsl"
)

// System columns that are always treated as @user references.
var systemUserColumns = map[string]bool{
	"created_by":   true,
	"updated_by":   true,
	"deleted_by":   true,
	"committed_by": true,
}

// Expander enriches query result rows with _meta for reference columns.
// Resolution is batched per column to minimize round-trips.
type Expander struct {
	users UserResolver
	teams TeamResolver
}

// NewExpander creates an expander with the given resolvers. Either may be nil.
func NewExpander(users UserResolver, teams TeamResolver) *Expander {
	return &Expander{users: users, teams: teams}
}

// Expand enriches result rows with a _meta field for reference columns.
// Returns an error if explicitly requested columns cannot be expanded.
func (x *Expander) Expand(ctx context.Context, result *dsl.QueryResult, expand *dsl.ExpandConfig, dataset *dsl.DatasetInfo) error {
	if expand == nil {
		return nil
	}

	// Build a map of column name → ref type for expandable columns
	refMap, err := x.buildRefMap(dataset, expand)
	if err != nil {
		return err
	}

	if len(refMap) == 0 || len(result.Rows) == 0 {
		return nil
	}

	// Collect unique IDs per ref type across all rows
	userIDs := make(map[string]bool)
	teamIDs := make(map[string]bool)

	for _, row := range result.Rows {
		for col, refType := range refMap {
			id, ok := rowString(row, col)
			if !ok {
				continue
			}
			switch refType {
			case "@user":
				userIDs[id] = true
			case "@team":
				teamIDs[id] = true
			}
		}
	}

	// Batch resolve
	userCache := x.resolveUsers(ctx, userIDs)
	teamCache := x.resolveTeams(ctx, teamIDs)

	// Inject _meta into each row
	for _, row := range result.Rows {
		meta := make(map[string]any)
		for col, refType := range refMap {
			id, ok := rowString(row, col)
			if !ok {
				continue
			}
			switch refType {
			case "@user":
				if u, found := userCache[id]; found {
					meta[col] = u
				}
			case "@team":
				if t, found := teamCache[id]; found {
					meta[col] = t
				}
			}
		}
		if len(meta) > 0 {
			row["_meta"] = meta
		}
	}

	return nil
}

// buildRefMap determines which columns should be expanded and their ref type.
// Returns an error if explicitly requested columns are not expandable.
func (x *Expander) buildRefMap(dataset *dsl.DatasetInfo, expand *dsl.ExpandConfig) (map[string]string, error) {
	// First, build the set of all expandable columns
	expandable := make(map[string]string)

	if dataset != nil {
		for _, col := range dataset.Columns {
			if col.RefDataset != "" {
				expandable[col.Name] = col.RefDataset
			}
		}
	}

	// System user columns are always expandable
	for col := range systemUserColumns {
		if _, already := expandable[col]; !already {
			expandable[col] = "@user"
		}
	}

	// Determine which columns to actually expand
	refs := make(map[string]string)

	if expand.All {
		// Expand everything that's expandable
		for col, refType := range expandable {
			refs[col] = refType
		}
	} else {
		// Expand only explicitly requested columns — error if not expandable
		var invalid []string
		for _, col := range expand.Columns {
			refType, ok := expandable[col]
			if !ok {
				invalid = append(invalid, col)
				continue
			}
			refs[col] = refType
		}
		if len(invalid) > 0 {
			return nil, fmt.Errorf("cannot expand non-reference columns: %s", strings.Join(invalid, ", "))
		}
	}

	// Filter out types we can't resolve (no resolver available)
	for col, refType := range refs {
		switch refType {
		case "@user":
			if x.users == nil {
				delete(refs, col)
			}
		case "@team":
			if x.teams == nil {
				delete(refs, col)
			}
		default:
			// Dataset-to-dataset references — not yet supported, skip silently
			delete(refs, col)
		}
	}

	return refs, nil
}

func rowString(row dsl.Row, col string) (string, bool) {
	val, ok := row[col]
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

func (x *Expander) resolveUsers(ctx context.Context, ids map[string]bool) map[string]map[string]any {
	cache := make(map[string]map[string]any, len(ids))
	if x.users == nil || len(ids) == 0 {
		return cache
	}

	for id := range ids {
		u, err := x.users.GetUser(ctx, id)
		if err != nil || u == nil {
			continue
		}
		cache[id] = map[string]any{
			"id":           u.ID,
			"display_name": u.DisplayName,
			"email":        u.Email,
			"avatar_url":   u.AvatarURL,
			"_type":        "user",
		}
	}
	return cache
}

func (x *Expander) resolveTeams(ctx context.Context, ids map[string]bool) map[string]map[string]any {
	cache := make(map[string]map[string]any, len(ids))
	if x.teams == nil || len(ids) == 0 {
		return cache
	}

	for id := range ids {
		t, err := x.teams.GetTeam(ctx, id)
		if err != nil || t == nil {
			continue
		}
		cache[id] = map[string]any{
			"id":    t.ID,
			"name":  t.Name,
			"_type": "team",
		}
	}
	return cache
}
