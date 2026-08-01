package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// ProjectConfig selects a subset of columns, optionally renaming them.
// Exactly one of Select / Drop should be set. When Select is empty and Drop is
// empty, the operator is a pass-through.
type ProjectConfig struct {
	Select []dsl.SelectField `json:"select,omitempty"`
	Drop   []string          `json:"drop,omitempty"`
}

type projectOp struct {
	cfg ProjectConfig
}

func (o *projectOp) Name() string     { return "project" }
func (o *projectOp) IsLiveSafe() bool { return true }

func (o *projectOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(o.cfg.Select) == 0 && len(o.cfg.Drop) == 0 {
		return in, nil
	}
	if len(o.cfg.Drop) > 0 {
		drop := make(map[string]struct{}, len(o.cfg.Drop))
		for _, c := range o.cfg.Drop {
			drop[c] = struct{}{}
		}
		for _, row := range in {
			for c := range drop {
				delete(row, c)
			}
		}
		return in, nil
	}

	mappings := make([]struct{ from, to string }, 0, len(o.cfg.Select))
	for _, s := range o.cfg.Select {
		from := s.Field
		if from == "" {
			from = s.As
		}
		to := s.OutputName()
		mappings = append(mappings, struct{ from, to string }{from, to})
	}
	out := make([]dsl.Row, 0, len(in))
	for _, row := range in {
		outRow := make(dsl.Row, len(mappings))
		for _, m := range mappings {
			if v, ok := row[m.from]; ok {
				outRow[m.to] = v
			}
		}
		out = append(out, outRow)
	}
	return out, nil
}

func projectFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg ProjectConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("project: decode config: %w", err)
	}
	if len(cfg.Select) > 0 && len(cfg.Drop) > 0 {
		return nil, fmt.Errorf("project: select and drop are mutually exclusive")
	}
	return &projectOp{cfg: cfg}, nil
}

// RenameConfig maps source column names to new names.
type RenameConfig struct {
	Map map[string]string `json:"map"`
}

type renameOp struct {
	cfg RenameConfig
}

func (o *renameOp) Name() string     { return "rename" }
func (o *renameOp) IsLiveSafe() bool { return true }

func (o *renameOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(o.cfg.Map) == 0 {
		return in, nil
	}
	for _, row := range in {
		for from, to := range o.cfg.Map {
			if v, ok := row[from]; ok {
				row[to] = v
				delete(row, from)
			}
		}
	}
	return in, nil
}

func renameFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg RenameConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("rename: decode config: %w", err)
	}
	if len(cfg.Map) == 0 {
		return nil, fmt.Errorf("rename: map is required and must be non-empty")
	}
	return &renameOp{cfg: cfg}, nil
}

// DropConfig removes columns from each row.
type DropConfig struct {
	Columns []string `json:"columns"`
}

type dropOp struct {
	cfg DropConfig
}

func (o *dropOp) Name() string     { return "drop" }
func (o *dropOp) IsLiveSafe() bool { return true }

func (o *dropOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if len(o.cfg.Columns) == 0 {
		return in, nil
	}
	for _, row := range in {
		for _, c := range o.cfg.Columns {
			delete(row, c)
		}
	}
	return in, nil
}

func dropFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg DropConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("drop: decode config: %w", err)
	}
	if len(cfg.Columns) == 0 {
		return nil, fmt.Errorf("drop: columns is required and must be non-empty")
	}
	return &dropOp{cfg: cfg}, nil
}

func init() {
	Register("project", projectFactory)
	Register("rename", renameFactory)
	Register("drop", dropFactory)
}
