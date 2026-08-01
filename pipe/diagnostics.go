package pipe

import (
	"errors"
	"strings"

	"github.com/xraph/dql/dsl"
)

// Diagnostic is a single editor-facing problem.
type Diagnostic struct {
	Line      int    `json:"line"`   // 1-based
	Column    int    `json:"column"` // 1-based
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
	Severity  string `json:"severity"` // error | warning | info
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
}

// DiagnoseText runs the textual parser and returns editor diagnostics.
// Empty slice means the text parses cleanly AND every stage's config is
// shape-valid.
//
// Coverage: parse-time errors (missing source, unbalanced brackets,
// unterminated strings, unknown stages) and stage-shape errors (missing
// required fields). Service-availability and dataset-existence errors only
// surface at execute time.
func DiagnoseText(text string) []Diagnostic {
	q, err := ParseText(text)
	if err != nil {
		return []Diagnostic{textualErrorDiagnostic(text, err)}
	}
	// Successful textual parse — now run the structural shape check.
	return DiagnoseStages(text, q.Pipe)
}

// DiagnoseStages validates each parsed pipe stage and returns one
// diagnostic per shape error. Stage line/column point at the start of the
// segment in the original text when supplied, otherwise (1,1).
func DiagnoseStages(text string, stages []dsl.PipeStage) []Diagnostic {
	shape := ValidateShape(stages)
	if len(shape) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(shape))
	for _, e := range shape {
		line, col := 1, 1
		if e.StageIndex >= 0 {
			// stages[0] is the first stage after `source`, which is segment 1
			// in the textual form (segment 0 was `source ...`).
			line, col = stageStartPosition(text, e.StageIndex+1)
		}
		out = append(out, Diagnostic{
			Line:     line,
			Column:   col,
			Severity: "error",
			Code:     "pipe.shape",
			Message:  e.Message,
		})
	}
	return out
}

// textualErrorDiagnostic maps a ParseText error to a positioned diagnostic
// using string-pattern heuristics.
func textualErrorDiagnostic(text string, err error) Diagnostic {
	msg := err.Error()
	startLine, startCol := 1, 1
	switch {
	case strings.Contains(msg, "unterminated string"):
		startLine, startCol = lastByteOf(text, '"')
	case strings.Contains(msg, "unterminated `expression`"):
		startLine, startCol = lastByteOf(text, '`')
	case strings.Contains(msg, "unbalanced brackets"):
		startLine, startCol = firstUnbalanced(text)
	case strings.HasPrefix(msg, "stage "):
		// Format is "stage N: <reason>"
		var n int
		if _, err := scanInt(msg[len("stage "):], &n); err == nil {
			startLine, startCol = stageStartPosition(text, n)
		}
	case strings.Contains(msg, "must start with") || strings.Contains(msg, "source"):
		startLine, startCol = 1, 1
	}
	return Diagnostic{
		Line:     startLine,
		Column:   startCol,
		Severity: "error",
		Code:     "pipe.textual",
		Message:  msg,
	}
}

// stageStartPosition returns (line, column) for the start of the n-th
// segment, where segment 0 is `source ...`, segment 1 is the first stage
// after the first `|`, etc.
func stageStartPosition(text string, segmentIdx int) (int, int) {
	if segmentIdx <= 0 {
		return 1, 1
	}
	depthParen, depthBracket, depthBrace := 0, 0, 0
	inString, inRaw, escape := false, false, false
	pipeIdx := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if escape {
			escape = false
			continue
		}
		switch {
		case inString:
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
		case inRaw:
			if c == '`' {
				inRaw = false
			}
		default:
			switch c {
			case '"':
				inString = true
			case '`':
				inRaw = true
			case '(':
				depthParen++
			case ')':
				depthParen--
			case '[':
				depthBracket++
			case ']':
				depthBracket--
			case '{':
				depthBrace++
			case '}':
				depthBrace--
			case '|':
				if depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
					pipeIdx++
					if pipeIdx == segmentIdx {
						return positionAt(text, i+1)
					}
				}
			}
		}
	}
	return 1, 1
}

func positionAt(text string, offset int) (int, int) {
	if offset > len(text) {
		offset = len(text)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func lastByteOf(text string, b byte) (int, int) {
	idx := strings.LastIndexByte(text, b)
	if idx < 0 {
		return positionAt(text, len(text))
	}
	return positionAt(text, idx)
}

func firstUnbalanced(text string) (int, int) {
	stack := make([]int, 0, 16)
	pairs := map[byte]byte{')': '(', ']': '[', '}': '{'}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch c {
		case '(', '[', '{':
			stack = append(stack, i)
		case ')', ']', '}':
			if len(stack) == 0 {
				return positionAt(text, i)
			}
			top := stack[len(stack)-1]
			if text[top] != pairs[c] {
				return positionAt(text, top)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return positionAt(text, stack[0])
	}
	return positionAt(text, len(text))
}

var errInt = errors.New("expected integer")

// scanInt reads a leading non-negative integer.
func scanInt(s string, out *int) (int, error) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, errInt
	}
	n := 0
	for i := 0; i < end; i++ {
		n = n*10 + int(s[i]-'0')
	}
	*out = n
	return end, nil
}
