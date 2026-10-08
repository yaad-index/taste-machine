// Package importer defines how a data source becomes Taste Machine files.
// The engine knows no data source: each source is an Importer in its own
// package.
package importer

import (
	"context"

	"github.com/yaad-index/taste-machine/fileformat"
)

// Importer compiles a source into a shelf and a taste file. The taste file
// may carry a learn-from catalogue with the facts of items the person rated
// that are not on the shelf.
type Importer interface {
	Compile(ctx context.Context) (*fileformat.Catalogue, *fileformat.Taste, error)
}
