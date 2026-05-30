package output

import (
	"github.com/itchyny/gojq"
)

// Query wraps a compiled gojq program, keeping gojq out of the cmd layer.
type Query struct {
	code *gojq.Code
}

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
			// A bare halt/halt_error ends the stream without erroring, per jq.
			if he, ok := err.(*gojq.HaltError); ok && he.Value() == nil {
				break
			}
			return nil, err
		}
		results = append(results, v)
	}
	return results, nil
}
