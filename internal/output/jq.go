package output

import (
	"github.com/itchyny/gojq"
)

// Query holds a compiled jq expression ready to filter JSON values.
type Query struct {
	code *gojq.Code
}

// CompileQuery parses and compiles expr into a reusable Query.
func CompileQuery(expr string) (*Query, error) {
	parsed, err := gojq.Parse(expr)
	if err != nil {
		return nil, err
	}
	code, err := gojq.Compile(parsed)
	if err != nil {
		return nil, err
	}
	return &Query{code: code}, nil
}

func (q *Query) run(input any) ([]any, error) {
	iter := q.code.Run(input)
	var results []any
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			// halt/halt_error with no value ends the stream cleanly, per jq semantics.
			if he, ok := err.(*gojq.HaltError); ok && he.Value() == nil {
				break
			}
			return nil, err
		}
		results = append(results, v)
	}
	return results, nil
}
