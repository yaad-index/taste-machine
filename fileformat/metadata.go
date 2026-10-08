// Package fileformat reads and writes Taste Machine files: a zip holding
// metadata.json and items.jsonl, one item per line.
package fileformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/yaad-index/taste-machine/schema"
)

// FormatVersion is the only file format version this package reads and
// writes.
const FormatVersion = 1

// Kind says what a file is.
type Kind string

const (
	// KindCatalogue is a shelf: items and their facts.
	KindCatalogue Kind = "catalogue"
	// KindTaste is one person's ratings, plays and stated preferences.
	KindTaste Kind = "taste"
	// KindAcquisition is a catalogue of items to evaluate for buying.
	KindAcquisition Kind = "acquisition"
)

// Metadata is the content of metadata.json.
type Metadata struct {
	FormatVersion int    `json:"format_version"`
	SchemaID      string `json:"schema_id"`
	Kind          Kind   `json:"kind"`
	// Schema declares the item attributes of a catalogue or acquisition
	// list, or the per-user fields of a taste file.
	Schema schema.Schema `json:"schema"`
	// Taste is set on taste files only.
	Taste *TasteMeta `json:"taste,omitempty"`
}

// Direction says which end of a rating scale is better.
type Direction string

const (
	// HigherBetter is the default direction.
	HigherBetter Direction = "higher"
	// LowerBetter marks a scale where the lowest rating is the best.
	LowerBetter Direction = "lower"
)

// Scale is the rating scale a taste file uses.
type Scale struct {
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
	Direction Direction `json:"direction,omitempty"`
	Decimals  bool      `json:"decimals,omitempty"`
}

// DefaultScale is the scale assumed when a taste file declares none.
var DefaultScale = Scale{Min: 1, Max: 10, Direction: HigherBetter}

// Stance is a stated preference for one field value.
type Stance string

const (
	// Like replaces the value's affinity with +1.
	Like Stance = "like"
	// Dislike replaces the value's affinity with -1.
	Dislike Stance = "dislike"
	// Never excludes items with the value. Only filter fields allow it.
	Never Stance = "never"
)

// Constants overrides the scoring constants for one taste file. A nil
// member keeps the default.
type Constants struct {
	// K damps affinities of values seen on few items.
	K *float64 `json:"k,omitempty"`
	// PlayWeight is the strongest signal plays alone can give.
	PlayWeight *float64 `json:"play_weight,omitempty"`
	// PlaySaturation is the play count at which PlayWeight is reached.
	PlaySaturation *float64 `json:"play_saturation,omitempty"`
	// AnswerWeight is the weight of answers against taste history.
	AnswerWeight *float64 `json:"answer_weight,omitempty"`
}

// Answer is one stored answer for one-shot mode. The value is read by the
// question flow against the field it answers.
type Answer struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

// TasteMeta is the taste-specific part of a taste file's metadata.
type TasteMeta struct {
	Label       string                       `json:"label,omitempty"`
	Scale       *Scale                       `json:"scale,omitempty"`
	Preferences map[string]map[string]Stance `json:"preferences,omitempty"`
	Blocked     []string                     `json:"blocked,omitempty"`
	Favourites  []string                     `json:"favourites,omitempty"`
	Constants   Constants                    `json:"constants,omitzero"`
	Answers     []Answer                     `json:"answers,omitempty"`
}

// EffectiveScale is the declared scale with defaults applied.
func (t *TasteMeta) EffectiveScale() Scale {
	if t == nil || t.Scale == nil {
		return DefaultScale
	}
	s := *t.Scale
	if s.Direction == "" {
		s.Direction = HigherBetter
	}
	return s
}

// validate checks the metadata on its own, without the files it refers to.
func (m Metadata) validate() error {
	var errs []error
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported format_version %d, this build reads %d", m.FormatVersion, FormatVersion)
	}
	if m.SchemaID == "" {
		errs = append(errs, errors.New("empty schema_id"))
	}
	switch m.Kind {
	case KindCatalogue, KindAcquisition:
		if m.Taste != nil {
			errs = append(errs, fmt.Errorf("a %s file carries taste metadata", m.Kind))
		}
	case KindTaste:
		errs = append(errs, m.validateTaste()...)
	default:
		errs = append(errs, fmt.Errorf("unknown kind %q", m.Kind))
	}
	if err := m.Schema.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (m Metadata) validateTaste() []error {
	var errs []error
	for _, f := range m.Schema.Fields {
		if len(f.Edges) > 0 || f.Buckets != 0 {
			errs = append(errs, fmt.Errorf("field %q: bucket settings are allowed in catalogue metadata only", f.Name))
		}
		if f.GroupSize {
			errs = append(errs, fmt.Errorf("field %q: group_size is allowed in catalogue metadata only", f.Name))
		}
	}
	t := m.Taste
	if t == nil {
		return errs
	}
	if s := t.Scale; s != nil {
		if s.Min >= s.Max {
			errs = append(errs, fmt.Errorf("scale: min %v is not below max %v", s.Min, s.Max))
		}
		switch s.Direction {
		case "", HigherBetter, LowerBetter:
		default:
			errs = append(errs, fmt.Errorf("scale: unknown direction %q", s.Direction))
		}
	}
	for _, field := range sortedKeys(t.Preferences) {
		for _, value := range sortedKeys(t.Preferences[field]) {
			switch t.Preferences[field][value] {
			case Like, Dislike, Never:
			default:
				errs = append(errs, fmt.Errorf("preference %s=%s: unknown stance %q", field, value, t.Preferences[field][value]))
			}
		}
	}
	errs = append(errs, checkIDList("blocked", t.Blocked)...)
	errs = append(errs, checkIDList("favourites", t.Favourites)...)
	for _, id := range t.Blocked {
		if slices.Contains(t.Favourites, id) {
			errs = append(errs, fmt.Errorf("item %q is on both blocked and favourites", id))
		}
	}
	errs = append(errs, t.Constants.validate()...)
	for i, a := range t.Answers {
		if a.Field == "" {
			errs = append(errs, fmt.Errorf("answer %d: empty field", i))
		}
	}
	return errs
}

func (c Constants) validate() []error {
	var errs []error
	check := func(name string, v *float64, ok func(float64) bool, want string) {
		if v != nil && (math.IsNaN(*v) || !ok(*v)) {
			errs = append(errs, fmt.Errorf("constant %s = %v, want %s", name, *v, want))
		}
	}
	check("k", c.K, func(v float64) bool { return v >= 0 }, ">= 0")
	check("play_weight", c.PlayWeight, func(v float64) bool { return v >= 0 && v <= 1 }, "in [0, 1]")
	check("play_saturation", c.PlaySaturation, func(v float64) bool { return v > 1 }, "> 1")
	check("answer_weight", c.AnswerWeight, func(v float64) bool { return v >= 0 }, ">= 0")
	return errs
}

func checkIDList(name string, ids []string) []error {
	var errs []error
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			errs = append(errs, fmt.Errorf("%s: empty id", name))
			continue
		}
		if seen[id] {
			errs = append(errs, fmt.Errorf("%s: item %q listed twice", name, id))
		}
		seen[id] = true
	}
	return errs
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
