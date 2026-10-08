// Package score is the v1 matcher of ADR 0002: deterministic scoring of
// items against one member's taste, with an explanation for every result.
package score

import "github.com/yaad-index/taste-machine/dataset"

// Defaults from ADR 0002 section 8.
const (
	DefaultK                   = 3.0
	DefaultPlayWeight          = 0.5
	DefaultPlaySaturation      = 10.0
	DefaultAnswerWeight        = 1.0
	DefaultSimilarityThreshold = 0.6
)

// Constants are the tunable numbers of the scoring rules.
type Constants struct {
	// K damps the affinity of a value seen on few items.
	K float64
	// PlayWeight (p) is the strongest signal plays alone can give.
	PlayWeight float64
	// PlaySaturation (P) is the play count at which PlayWeight is reached.
	PlaySaturation float64
	// AnswerWeight (B) weighs answers against the taste score.
	AnswerWeight float64
}

// Defaults returns the ADR 0002 constants.
func Defaults() Constants {
	return Constants{K: DefaultK, PlayWeight: DefaultPlayWeight, PlaySaturation: DefaultPlaySaturation, AnswerWeight: DefaultAnswerWeight}
}

// For returns the defaults with the member's overrides applied.
func For(m *dataset.Member) Constants {
	c := Defaults()
	o := m.Meta.Constants
	if o.K != nil {
		c.K = *o.K
	}
	if o.PlayWeight != nil {
		c.PlayWeight = *o.PlayWeight
	}
	if o.PlaySaturation != nil {
		c.PlaySaturation = *o.PlaySaturation
	}
	if o.AnswerWeight != nil {
		c.AnswerWeight = *o.AnswerWeight
	}
	return c
}
