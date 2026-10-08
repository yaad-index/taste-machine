package score

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/yaad-index/taste-machine/schema"
)

// Edges are the ascending lower bounds of buckets 1 to len(Edges); a value
// below the first edge is in bucket 0.
type Edges []float64

// Count is the number of buckets.
func (e Edges) Count() int { return len(e) + 1 }

// Bucket returns the bucket a value falls in.
func (e Edges) Bucket(v float64) int {
	b := 0
	for _, edge := range e {
		if v >= edge {
			b++
		}
	}
	return b
}

// Label names a bucket by its bounds, for explanations.
func (e Edges) Label(b int) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	switch {
	case len(e) == 0:
		return "all"
	case b == 0:
		return "< " + f(e[0])
	case b >= len(e):
		return ">= " + f(e[len(e)-1])
	default:
		return fmt.Sprintf("%s to < %s", f(e[b-1]), f(e[b]))
	}
}

// QuantileEdges splits the distinct values into n buckets of about equal
// size. Equal values never straddle an edge, so ties collapse buckets and
// fewer than n is possible.
func QuantileEdges(values []float64, n int) Edges {
	distinct := slices.Clone(values)
	slices.Sort(distinct)
	distinct = slices.Compact(distinct)
	d := len(distinct)
	var edges Edges
	for i := 1; i < n; i++ {
		idx := i * d / n
		if idx == 0 || idx >= d {
			continue
		}
		if len(edges) == 0 || distinct[idx] > edges[len(edges)-1] {
			edges = append(edges, distinct[idx])
		}
	}
	return edges
}

// edgesFor is a number field's declared edges, or quantile edges over
// values with the declared or default bucket count.
func edgesFor(f schema.Field, values []float64) Edges {
	if len(f.Edges) > 0 {
		return Edges(f.Edges)
	}
	n := f.Buckets
	if n == 0 {
		n = schema.DefaultBuckets
	}
	return QuantileEdges(values, n)
}
