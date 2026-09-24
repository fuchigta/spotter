package schema_test

import (
	"testing"

	"github.com/fuchigta/spotter/internal/config"
	"github.com/fuchigta/spotter/internal/schema"
)

func TestCompileNil(t *testing.T) {
	s, err := schema.Compile(nil)
	if err != nil {
		t.Fatalf("Compile(nil) error: %v", err)
	}
	if err := s.Validate(map[string]any{"任意のキー": "任意の値"}); err != nil {
		t.Errorf("検証しない schema のはずが Validate() がエラーを返した: %v", err)
	}
}

func TestSimpleValid(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{
			"threshold": {Type: "integer", Required: true},
			"label":     {Type: "string"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"threshold": 10, "label": "x"}); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestSimpleMissingRequired(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{
			"threshold": {Type: "integer", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{}); err == nil {
		t.Fatal("必須フィールドが無いのに Validate() がエラーになりませんでした")
	}
}

func TestSimpleWrongType(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{
			"threshold": {Type: "integer"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"threshold": "not-a-number"}); err == nil {
		t.Fatal("型が違うのに Validate() がエラーになりませんでした")
	}
}

func TestSimpleUnknownField(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{
			"threshold": {Type: "integer"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"unknown": 1}); err == nil {
		t.Fatal("未知のフィールドがあるのに Validate() がエラーになりませんでした")
	}
}

func TestSimpleArray(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{
			"paths": {Type: "array", Items: "string"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"paths": []any{"a", "b"}}); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
	if err := s.Validate(map[string]any{"paths": []any{"a", 1}}); err == nil {
		t.Fatal("要素の型が違うのに Validate() がエラーになりませんでした")
	}
}

func TestJSONSchemaValid(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "pattern": "^[a-z]+$"},
			},
			"required": []any{"pattern"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"pattern": "abc"}); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

// TestSimpleFieldTypes は simple の各 type（string/integer/number/boolean）について、
// 通る値・通らない値をテーブル駆動で確認する。integer は小数（3.5 等）を弾く点が
// number と違う（isInteger は float64 が整数値かどうかを見る）。
func TestSimpleFieldTypes(t *testing.T) {
	cases := []struct {
		typ     string
		valid   []any
		invalid []any
	}{
		{
			typ:     "string",
			valid:   []any{"x", ""},
			invalid: []any{1, true, 1.5},
		},
		{
			typ:     "integer",
			valid:   []any{1, int64(2), float64(3)},
			invalid: []any{"1", true, 3.5},
		},
		{
			typ:     "number",
			valid:   []any{1, int64(2), 2.5, -1.25},
			invalid: []any{"1", true},
		},
		{
			typ:     "boolean",
			valid:   []any{true, false},
			invalid: []any{"true", 1, 0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			s, err := schema.Compile(&config.SchemaConfig{
				Simple: map[string]config.FieldSpec{"field": {Type: tc.typ}},
			})
			if err != nil {
				t.Fatalf("Compile() error: %v", err)
			}
			for _, v := range tc.valid {
				if err := s.Validate(map[string]any{"field": v}); err != nil {
					t.Errorf("%s: 通るはずの値 %#v が Validate() でエラーになった: %v", tc.typ, v, err)
				}
			}
			for _, v := range tc.invalid {
				if err := s.Validate(map[string]any{"field": v}); err == nil {
					t.Errorf("%s: 通らないはずの値 %#v が Validate() を通過した", tc.typ, v)
				}
			}
		})
	}
}

func TestSimpleTypeUnspecifiedIsError(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{"field": {}},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if err := s.Validate(map[string]any{"field": "x"}); err == nil {
		t.Fatal("type 未指定の field は Validate() がエラーになるはず")
	}
}

func TestSimpleUnsupportedTypeIsError(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		Simple: map[string]config.FieldSpec{"field": {Type: "object"}},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if err := s.Validate(map[string]any{"field": "x"}); err == nil {
		t.Fatal("未対応の type は Validate() がエラーになるはず")
	}
}

func TestJSONSchemaInvalid(t *testing.T) {
	s, err := schema.Compile(&config.SchemaConfig{
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "pattern": "^[a-z]+$"},
			},
			"required": []any{"pattern"},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	if err := s.Validate(map[string]any{"pattern": "ABC"}); err == nil {
		t.Fatal("pattern に一致しないのに Validate() がエラーになりませんでした")
	}
	if err := s.Validate(map[string]any{}); err == nil {
		t.Fatal("required を満たさないのに Validate() がエラーになりませんでした")
	}
}
