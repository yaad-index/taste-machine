package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/schema"
)

func TestValidateTypeRoleTable(t *testing.T) {
	types := []schema.Type{schema.Category, schema.Bool, schema.Set, schema.Number, schema.Range, schema.Votes}
	roles := []schema.Role{schema.Filter, schema.Preference, schema.Both, schema.Info}
	allowed := map[schema.Type][]schema.Role{
		schema.Range: {schema.Filter, schema.Info},
		schema.Votes: {schema.Preference, schema.Info},
	}
	for _, typ := range types {
		for _, role := range roles {
			want := true
			if rs, ok := allowed[typ]; ok {
				want = false
				for _, r := range rs {
					if r == role {
						want = true
					}
				}
			}
			err := schema.Schema{Fields: []schema.Field{{Name: "f", Type: typ, Role: role}}}.Validate()
			if want {
				assert.NoError(t, err, "%s/%s", typ, role)
			} else {
				assert.ErrorContains(t, err, "cannot have role", "%s/%s", typ, role)
			}
		}
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		fields []schema.Field
		want   string
	}{
		{"unknown type", []schema.Field{{Name: "f", Type: "colour", Role: schema.Info}}, "unknown type"},
		{"unknown role", []schema.Field{{Name: "f", Type: schema.Set, Role: "maybe"}}, "unknown role"},
		{"empty name", []schema.Field{{Type: schema.Set, Role: schema.Info}}, "empty name"},
		{"declared twice", []schema.Field{{Name: "f", Type: schema.Set, Role: schema.Info}, {Name: "f", Type: schema.Bool, Role: schema.Info}}, "declared twice"},
		{"unknown missing", []schema.Field{{Name: "f", Type: schema.Set, Role: schema.Filter, Missing: "skip"}}, "unknown missing policy"},
		{"negative weight", []schema.Field{{Name: "f", Type: schema.Set, Role: schema.Preference, Weight: -1}}, "negative weight"},
		{"edges on set", []schema.Field{{Name: "f", Type: schema.Set, Role: schema.Info, Edges: []float64{1, 2}}}, "bucket settings on a set field"},
		{"buckets on category", []schema.Field{{Name: "f", Type: schema.Category, Role: schema.Info, Buckets: 3}}, "bucket settings on a category field"},
		{"edges and count", []schema.Field{{Name: "f", Type: schema.Number, Role: schema.Info, Edges: []float64{1, 2}, Buckets: 3}}, "both edges and a bucket count"},
		{"negative count", []schema.Field{{Name: "f", Type: schema.Number, Role: schema.Info, Buckets: -2}}, "negative bucket count"},
		{"edges not ascending", []schema.Field{{Name: "f", Type: schema.Number, Role: schema.Info, Edges: []float64{2, 2}}}, "strictly ascending"},
		{"group_size on number", []schema.Field{{Name: "f", Type: schema.Number, Role: schema.Filter, GroupSize: true}}, "only range fields may carry it"},
		{"group_size twice", []schema.Field{
			{Name: "a", Type: schema.Range, Role: schema.Filter, GroupSize: true},
			{Name: "b", Type: schema.Range, Role: schema.Filter, GroupSize: true},
		}, "group_size is set on 2 fields"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schema.Schema{Fields: tc.fields}.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	s := schema.Schema{Fields: []schema.Field{
		{Name: "fits", Type: schema.Range, Role: schema.Filter, GroupSize: true},
		{Name: "weight", Type: schema.Number, Role: schema.Preference, Edges: []float64{1.5, 2.5, 3.5}},
		{Name: "length", Type: schema.Number, Role: schema.Both, Buckets: 4, Missing: schema.Drop},
		{Name: "tags", Type: schema.Set, Role: schema.Both, Askable: true, Weight: 2},
	}}
	require.NoError(t, s.Validate())
}

func TestFieldDefaults(t *testing.T) {
	f := schema.Field{Name: "f", Type: schema.Set, Role: schema.Both}
	assert.InDelta(t, 1.0, f.EffectiveWeight(), 0)
	assert.Equal(t, schema.Keep, f.EffectiveMissing())
	assert.True(t, f.IsFilter())
	assert.True(t, f.IsPreference())

	f = schema.Field{Name: "f", Type: schema.Set, Role: schema.Info, Weight: 2.5, Missing: schema.Drop}
	assert.InDelta(t, 2.5, f.EffectiveWeight(), 0)
	assert.Equal(t, schema.Drop, f.EffectiveMissing())
	assert.False(t, f.IsFilter())
	assert.False(t, f.IsPreference())
}

func TestDecodeAndMarshal(t *testing.T) {
	cases := []struct {
		typ  schema.Type
		in   string
		out  string
		want func(t *testing.T, v schema.Value)
	}{
		{schema.Category, `"red"`, `"red"`, func(t *testing.T, v schema.Value) { assert.Equal(t, []string{"red"}, v.Values()) }},
		{schema.Bool, `true`, `true`, func(t *testing.T, v schema.Value) { assert.Equal(t, []string{"true"}, v.Values()) }},
		{schema.Bool, `false`, `false`, func(t *testing.T, v schema.Value) { assert.Equal(t, []string{"false"}, v.Values()) }},
		{schema.Set, `["b","a"]`, `["a","b"]`, func(t *testing.T, v schema.Value) { assert.Equal(t, []string{"a", "b"}, v.Values()) }},
		{schema.Set, `[]`, `[]`, func(t *testing.T, v schema.Value) { assert.Empty(t, v.Values()) }},
		{schema.Number, `2.5`, `2.5`, func(t *testing.T, v schema.Value) { assert.InDelta(t, 2.5, v.Number, 0) }},
		{schema.Range, `{"min":2,"max":4}`, `{"min":2,"max":4}`, func(t *testing.T, v schema.Value) {
			assert.True(t, v.Range.Contains(2))
			assert.True(t, v.Range.Contains(4))
			assert.False(t, v.Range.Contains(5))
			assert.False(t, v.Range.Contains(1))
		}},
		{schema.Votes, `{"x":3,"y":1}`, `{"x":3,"y":1}`, func(t *testing.T, v schema.Value) {
			assert.Equal(t, map[string]float64{"x": 0.75, "y": 0.25}, v.Shares())
		}},
		{schema.Votes, `{}`, `{}`, func(t *testing.T, v schema.Value) { assert.Nil(t, v.Shares()) }},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ)+" "+tc.in, func(t *testing.T) {
			f := schema.Field{Name: "f", Type: tc.typ, Role: schema.Info}
			v, err := f.Decode(json.RawMessage(tc.in))
			require.NoError(t, err)
			tc.want(t, v)
			out, err := json.Marshal(v)
			require.NoError(t, err)
			assert.JSONEq(t, tc.out, string(out))
			again, err := f.Decode(out)
			require.NoError(t, err)
			assert.Equal(t, v, again)
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		typ  schema.Type
		in   string
		want string
	}{
		{schema.Category, `null`, "null value"},
		{schema.Category, `""`, "empty category"},
		{schema.Category, `3`, "cannot unmarshal"},
		{schema.Bool, `"yes"`, "cannot unmarshal"},
		{schema.Set, `["a","a"]`, "repeated"},
		{schema.Set, `["a",""]`, "empty set member"},
		{schema.Set, `"a"`, "cannot unmarshal"},
		{schema.Number, `"2"`, "cannot unmarshal"},
		{schema.Range, `{"min":4,"max":2}`, "above max"},
		{schema.Range, `{"min":1,"max":2,"step":1}`, "unknown field"},
		{schema.Votes, `{"x":-1}`, "negative vote count"},
		{schema.Votes, `{"":1}`, "empty vote value"},
		{schema.Number, `1 2`, "trailing data"},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ)+" "+tc.in, func(t *testing.T) {
			f := schema.Field{Name: "f", Type: tc.typ, Role: schema.Info}
			_, err := f.Decode(json.RawMessage(tc.in))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), `field "f"`)
		})
	}
}

func TestSchemaFieldLookup(t *testing.T) {
	s := schema.Schema{Fields: []schema.Field{{Name: "a", Type: schema.Set, Role: schema.Info}}}
	f, ok := s.Field("a")
	assert.True(t, ok)
	assert.Equal(t, "a", f.Name)
	_, ok = s.Field("b")
	assert.False(t, ok)
}
