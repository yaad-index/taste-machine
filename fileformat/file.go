package fileformat

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/yaad-index/taste-machine/schema"
)

// Names of the entries inside a file.
const (
	metadataEntry  = "metadata.json"
	itemsEntry     = "items.jsonl"
	learnFromEntry = "learn-from.zip"
)

// maxEntrySize bounds how much one zip entry may expand to, so a hostile
// file cannot exhaust memory.
const maxEntrySize = 256 << 20

// Item is one catalogue item and its facts.
type Item struct {
	ID    string
	Facts map[string]schema.Value
}

// TasteItem is what one taste file says about one item.
type TasteItem struct {
	ID     string
	Rating *float64
	Plays  int
	// Fields holds the per-user fields the taste metadata declares.
	Fields map[string]schema.Value
}

// Catalogue is a shelf or an acquisition list.
type Catalogue struct {
	Meta  Metadata
	Items []Item
}

// Taste is one person's taste file.
type Taste struct {
	Meta  Metadata
	Items []TasteItem
	// LearnFrom holds the facts of items this person rated, so their
	// history counts over another shelf. It may be nil.
	LearnFrom *Catalogue
}

type itemLine struct {
	ID     string                     `json:"id"`
	Facts  map[string]json.RawMessage `json:"facts,omitempty"`
	Rating *float64                   `json:"rating,omitempty"`
	Plays  *int                       `json:"plays,omitempty"`
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
}

// ReadCatalogueFile reads a catalogue or acquisition list from disk.
func ReadCatalogueFile(path string) (*Catalogue, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = r.Close() }()
	c, err := readCatalogue(&r.Reader)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// ReadTasteFile reads a taste file from disk.
func ReadTasteFile(path string) (*Taste, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = r.Close() }()
	t, err := readTaste(&r.Reader)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// ReadCatalogue reads a catalogue or acquisition list.
func ReadCatalogue(r io.ReaderAt, size int64) (*Catalogue, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	return readCatalogue(z)
}

// ReadTaste reads a taste file.
func ReadTaste(r io.ReaderAt, size int64) (*Taste, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	return readTaste(z)
}

func readCatalogue(z *zip.Reader) (*Catalogue, error) {
	meta, err := readMetadata(z)
	if err != nil {
		return nil, err
	}
	if meta.Kind != KindCatalogue && meta.Kind != KindAcquisition {
		return nil, fmt.Errorf("kind is %q, want %q or %q", meta.Kind, KindCatalogue, KindAcquisition)
	}
	if hasEntry(z, learnFromEntry) {
		return nil, fmt.Errorf("a %s file carries %s", meta.Kind, learnFromEntry)
	}
	c := &Catalogue{Meta: meta}
	err = readItems(z, func(l itemLine) error {
		if l.Rating != nil || l.Plays != nil || l.Fields != nil {
			return errors.New("rating, plays and fields belong in a taste file")
		}
		facts, err := decodeFields(meta.Schema, l.Facts)
		if err != nil {
			return err
		}
		c.Items = append(c.Items, Item{ID: l.ID, Facts: facts})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func readTaste(z *zip.Reader) (*Taste, error) {
	meta, err := readMetadata(z)
	if err != nil {
		return nil, err
	}
	if meta.Kind != KindTaste {
		return nil, fmt.Errorf("kind is %q, want %q", meta.Kind, KindTaste)
	}
	scale := meta.Taste.EffectiveScale()
	t := &Taste{Meta: meta}
	err = readItems(z, func(l itemLine) error {
		if l.Facts != nil {
			return errors.New("facts belong in a catalogue file")
		}
		if l.Rating != nil {
			if err := checkRating(*l.Rating, scale); err != nil {
				return err
			}
		}
		item := TasteItem{ID: l.ID, Rating: l.Rating}
		if l.Plays != nil {
			if *l.Plays < 0 {
				return fmt.Errorf("negative plays %d", *l.Plays)
			}
			item.Plays = *l.Plays
		}
		fields, err := decodeFields(meta.Schema, l.Fields)
		if err != nil {
			return err
		}
		item.Fields = fields
		t.Items = append(t.Items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if hasEntry(z, learnFromEntry) {
		raw, err := readEntry(z, learnFromEntry)
		if err != nil {
			return nil, err
		}
		lf, err := ReadCatalogue(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", learnFromEntry, err)
		}
		if lf.Meta.Kind != KindCatalogue {
			return nil, fmt.Errorf("%s: kind is %q, want %q", learnFromEntry, lf.Meta.Kind, KindCatalogue)
		}
		if lf.Meta.SchemaID != meta.SchemaID {
			return nil, fmt.Errorf("%s: schema_id %q does not match the taste file's %q", learnFromEntry, lf.Meta.SchemaID, meta.SchemaID)
		}
		t.LearnFrom = lf
	}
	return t, nil
}

func checkRating(r float64, s Scale) error {
	if math.IsNaN(r) || r < s.Min || r > s.Max {
		return fmt.Errorf("rating %v is outside the scale [%v, %v]", r, s.Min, s.Max)
	}
	if !s.Decimals && r != math.Trunc(r) {
		return fmt.Errorf("rating %v has decimals, the scale allows whole numbers only", r)
	}
	return nil
}

func readMetadata(z *zip.Reader) (Metadata, error) {
	raw, err := readEntry(z, metadataEntry)
	if err != nil {
		return Metadata{}, err
	}
	var m Metadata
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Metadata{}, fmt.Errorf("%s: %w", metadataEntry, err)
	}
	if dec.More() {
		return Metadata{}, fmt.Errorf("%s: trailing data", metadataEntry)
	}
	if err := m.validate(); err != nil {
		return Metadata{}, fmt.Errorf("%s: %w", metadataEntry, err)
	}
	return m, nil
}

func readItems(z *zip.Reader, add func(l itemLine) error) error {
	raw, err := readEntry(z, itemsEntry)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), maxEntrySize)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l itemLine
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			return fmt.Errorf("%s line %d: %w", itemsEntry, n, err)
		}
		if dec.More() {
			return fmt.Errorf("%s line %d: trailing data", itemsEntry, n)
		}
		if l.ID == "" {
			return fmt.Errorf("%s line %d: empty id", itemsEntry, n)
		}
		if seen[l.ID] {
			return fmt.Errorf("%s line %d: item %q appears twice", itemsEntry, n, l.ID)
		}
		seen[l.ID] = true
		if err := add(l); err != nil {
			return fmt.Errorf("%s line %d (item %q): %w", itemsEntry, n, l.ID, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", itemsEntry, err)
	}
	return nil
}

func decodeFields(s schema.Schema, raw map[string]json.RawMessage) (map[string]schema.Value, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]schema.Value, len(raw))
	for _, name := range sortedKeys(raw) {
		f, ok := s.Field(name)
		if !ok {
			return nil, fmt.Errorf("field %q is not declared in the schema", name)
		}
		v, err := f.Decode(raw[name])
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

func hasEntry(z *zip.Reader, name string) bool {
	return slices.ContainsFunc(z.File, func(f *zip.File) bool { return f.Name == name })
}

func readEntry(z *zip.Reader, name string) ([]byte, error) {
	var found *zip.File
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%s appears twice", name)
		}
		found = f
	}
	if found == nil {
		return nil, fmt.Errorf("missing %s", name)
	}
	rc, err := found.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(raw) > maxEntrySize {
		return nil, fmt.Errorf("%s: larger than %d bytes", name, maxEntrySize)
	}
	return raw, nil
}

// zipTime is the modification time written on every entry, so the same
// content always gives the same bytes.
var zipTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Write writes the catalogue. Items are written in id order.
func (c *Catalogue) Write(w io.Writer) error {
	lines := make([]itemLine, 0, len(c.Items))
	for _, it := range c.Items {
		facts, err := encodeFields(it.Facts)
		if err != nil {
			return fmt.Errorf("item %q: %w", it.ID, err)
		}
		lines = append(lines, itemLine{ID: it.ID, Facts: facts})
	}
	return writeFile(w, c.Meta, lines, nil)
}

// Write writes the taste file. Items are written in id order.
func (t *Taste) Write(w io.Writer) error {
	lines := make([]itemLine, 0, len(t.Items))
	for _, it := range t.Items {
		fields, err := encodeFields(it.Fields)
		if err != nil {
			return fmt.Errorf("item %q: %w", it.ID, err)
		}
		l := itemLine{ID: it.ID, Rating: it.Rating, Fields: fields}
		if it.Plays != 0 {
			l.Plays = &it.Plays
		}
		lines = append(lines, l)
	}
	var lf []byte
	if t.LearnFrom != nil {
		var buf bytes.Buffer
		if err := t.LearnFrom.Write(&buf); err != nil {
			return fmt.Errorf("%s: %w", learnFromEntry, err)
		}
		lf = buf.Bytes()
	}
	return writeFile(w, t.Meta, lines, lf)
}

// WriteFile writes v (a *Catalogue or *Taste) to path.
func WriteFile(path string, v interface{ Write(io.Writer) error }) error {
	var buf bytes.Buffer
	if err := v.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func encodeFields(vals map[string]schema.Value) (map[string]json.RawMessage, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(vals))
	for name, v := range vals {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		out[name] = raw
	}
	return out, nil
}

func writeFile(w io.Writer, meta Metadata, lines []itemLine, learnFrom []byte) error {
	if err := meta.validate(); err != nil {
		return fmt.Errorf("%s: %w", metadataEntry, err)
	}
	slices.SortFunc(lines, func(a, b itemLine) int { return strings.Compare(a.ID, b.ID) })
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	var items bytes.Buffer
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			return fmt.Errorf("item %q: %w", l.ID, err)
		}
		items.Write(raw)
		items.WriteByte('\n')
	}
	zw := zip.NewWriter(w)
	entries := []struct {
		name string
		data []byte
	}{
		{metadataEntry, append(metaJSON, '\n')},
		{itemsEntry, items.Bytes()},
	}
	if learnFrom != nil {
		entries = append(entries, struct {
			name string
			data []byte
		}{learnFromEntry, learnFrom})
	}
	for _, e := range entries {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return err
		}
		if _, err := fw.Write(e.data); err != nil {
			return err
		}
	}
	return zw.Close()
}
