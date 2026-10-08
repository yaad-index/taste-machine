// Package dataset loads a shelf, one or more taste files and an optional
// acquisition list, checks they belong together, and joins them by item id.
package dataset

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

// Input is one taste file to load.
type Input struct {
	Taste *fileformat.Taste
	// Name labels the member when the taste metadata has no label,
	// usually the file name.
	Name string
}

// NoticeKind says what a Notice reports.
type NoticeKind string

const (
	// UnknownItem: a taste file names an item found neither in the shelf
	// nor in its learn-from catalogue. It is ignored.
	UnknownItem NoticeKind = "unknown item"
	// UnknownListItem: a blocked or favourites id found neither in the
	// shelf nor in the acquisition list. It is ignored.
	UnknownListItem NoticeKind = "unknown list item"
	// AlreadyInCatalogue: an acquisition-list item is already on the shelf.
	AlreadyInCatalogue NoticeKind = "already in the catalogue"
)

// Notice is something the load found and handled without failing.
type Notice struct {
	Kind NoticeKind
	// Member is the label of the taste file concerned, empty for the
	// acquisition list.
	Member string
	ID     string
}

// Member is one person's taste, resolved against the shelf.
type Member struct {
	Label string
	// Meta is the taste metadata; a file without one has the zero value.
	Meta fileformat.TasteMeta
	// Schema declares the member's per-user fields.
	Schema schema.Schema
	// Entries holds what the taste file says per item id, for every id
	// that has facts in the shelf or the learn-from catalogue.
	Entries map[string]fileformat.TasteItem
	// Blocked and Favourites hold the list ids found in the shelf or the
	// acquisition list.
	Blocked    map[string]bool
	Favourites map[string]bool

	learnFrom map[string]map[string]schema.Value
}

// IDs returns the ids in Entries, sorted.
func (m *Member) IDs() []string { return sortedKeys(m.Entries) }

// Dataset is the joined, validated input.
type Dataset struct {
	SchemaID string
	// Schema declares the item attributes.
	Schema schema.Schema
	// Shelf and Acquisition are sorted by id.
	Shelf       []fileformat.Item
	Acquisition []fileformat.Item
	// Members are sorted by label.
	Members []*Member
	// Notices are sorted by kind, member and id.
	Notices []Notice

	shelf map[string]map[string]schema.Value
}

// Facts returns the facts of an item for a member: the shelf's when the
// shelf has it, otherwise the member's learn-from catalogue's. The map is
// shared, so callers must not modify it.
func (d *Dataset) Facts(m *Member, id string) (map[string]schema.Value, bool) {
	if f, ok := d.shelf[id]; ok {
		return f, true
	}
	f, ok := m.learnFrom[id]
	return f, ok
}

// OnShelf reports whether the shelf has the item.
func (d *Dataset) OnShelf(id string) bool {
	_, ok := d.shelf[id]
	return ok
}

// Load checks that the files belong together and joins them. The
// acquisition list may be nil. Every problem that would make a result
// wrong is an error; ids that can simply be skipped become notices.
func Load(shelf *fileformat.Catalogue, tastes []Input, acquisition *fileformat.Catalogue) (*Dataset, error) {
	if shelf == nil {
		return nil, errors.New("no shelf")
	}
	if shelf.Meta.Kind != fileformat.KindCatalogue {
		return nil, fmt.Errorf("shelf: kind is %q, want %q", shelf.Meta.Kind, fileformat.KindCatalogue)
	}
	if len(tastes) == 0 {
		return nil, errors.New("no taste file")
	}
	d := &Dataset{
		SchemaID: shelf.Meta.SchemaID,
		Schema:   shelf.Meta.Schema,
		Shelf:    sortedItems(shelf.Items),
		shelf:    factsByID(shelf.Items),
	}
	var errs []error
	if acquisition != nil {
		if err := d.addAcquisition(acquisition); err != nil {
			errs = append(errs, err)
		}
	}
	labels := map[string]bool{}
	for i, in := range tastes {
		m, err := d.member(in)
		if err != nil {
			errs = append(errs, fmt.Errorf("taste file %d (%s): %w", i+1, in.Name, err))
			continue
		}
		if labels[m.Label] {
			errs = append(errs, fmt.Errorf("taste file %d (%s): label %q is used twice", i+1, in.Name, m.Label))
			continue
		}
		labels[m.Label] = true
		d.Members = append(d.Members, m)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	slices.SortFunc(d.Members, func(a, b *Member) int { return cmp.Compare(a.Label, b.Label) })
	slices.SortFunc(d.Notices, func(a, b Notice) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Member, b.Member), cmp.Compare(a.ID, b.ID))
	})
	return d, nil
}

func (d *Dataset) addAcquisition(acq *fileformat.Catalogue) error {
	if acq.Meta.Kind != fileformat.KindAcquisition {
		return fmt.Errorf("acquisition list: kind is %q, want %q", acq.Meta.Kind, fileformat.KindAcquisition)
	}
	if err := d.sameSchema("acquisition list", acq.Meta); err != nil {
		return err
	}
	d.Acquisition = sortedItems(acq.Items)
	for _, it := range d.Acquisition {
		if d.OnShelf(it.ID) {
			d.Notices = append(d.Notices, Notice{Kind: AlreadyInCatalogue, ID: it.ID})
		}
	}
	return nil
}

func (d *Dataset) sameSchema(what string, meta fileformat.Metadata) error {
	if meta.SchemaID != d.SchemaID {
		return fmt.Errorf("%s: schema_id %q does not match the shelf's %q", what, meta.SchemaID, d.SchemaID)
	}
	if !reflect.DeepEqual(meta.Schema, d.Schema) {
		return fmt.Errorf("%s: schema_id %q matches the shelf's but the declared fields differ", what, meta.SchemaID)
	}
	return nil
}

func (d *Dataset) member(in Input) (*Member, error) {
	t := in.Taste
	if t == nil {
		return nil, errors.New("no taste")
	}
	if t.Meta.Kind != fileformat.KindTaste {
		return nil, fmt.Errorf("kind is %q, want %q", t.Meta.Kind, fileformat.KindTaste)
	}
	if t.Meta.SchemaID != d.SchemaID {
		return nil, fmt.Errorf("schema_id %q does not match the shelf's %q", t.Meta.SchemaID, d.SchemaID)
	}
	m := &Member{Label: in.Name, Schema: t.Meta.Schema}
	if t.Meta.Taste != nil {
		m.Meta = *t.Meta.Taste
	}
	if m.Meta.Label != "" {
		m.Label = m.Meta.Label
	}
	if m.Label == "" {
		return nil, errors.New("no label: the metadata has none and no name was given")
	}
	var errs []error
	if t.LearnFrom != nil {
		if err := d.sameSchema("learn-from catalogue", t.LearnFrom.Meta); err != nil {
			errs = append(errs, err)
		}
		m.learnFrom = factsByID(t.LearnFrom.Items)
	}
	for _, f := range m.Schema.Fields {
		if _, ok := d.Schema.Field(f.Name); ok {
			errs = append(errs, fmt.Errorf("per-user field %q has the name of a catalogue field", f.Name))
		}
	}
	errs = append(errs, d.checkPreferences(m)...)
	for i, a := range m.Meta.Answers {
		if _, ok := d.field(m, a.Field); !ok {
			errs = append(errs, fmt.Errorf("answer %d: field %q is not declared", i+1, a.Field))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	m.Entries = make(map[string]fileformat.TasteItem, len(t.Items))
	for _, it := range t.Items {
		if _, ok := d.Facts(m, it.ID); !ok {
			d.Notices = append(d.Notices, Notice{Kind: UnknownItem, Member: m.Label, ID: it.ID})
			continue
		}
		m.Entries[it.ID] = it
	}
	m.Blocked = d.resolveList(m, m.Meta.Blocked)
	m.Favourites = d.resolveList(m, m.Meta.Favourites)
	return m, nil
}

// field looks a field up in the catalogue schema, then in the member's.
func (d *Dataset) field(m *Member, name string) (schema.Field, bool) {
	if f, ok := d.Schema.Field(name); ok {
		return f, true
	}
	return m.Schema.Field(name)
}

func (d *Dataset) checkPreferences(m *Member) []error {
	var errs []error
	for _, name := range sortedKeys(m.Meta.Preferences) {
		f, ok := d.field(m, name)
		if !ok {
			errs = append(errs, fmt.Errorf("preference on %q: field is not declared", name))
			continue
		}
		switch f.Type {
		case schema.Category, schema.Bool, schema.Set, schema.Votes:
		default:
			errs = append(errs, fmt.Errorf("preference on %q: a %s field has no values to prefer", name, f.Type))
			continue
		}
		for _, value := range sortedKeys(m.Meta.Preferences[name]) {
			if f.Type == schema.Bool && value != "true" && value != "false" {
				errs = append(errs, fmt.Errorf("preference %s=%s: a bool field takes true or false", name, value))
			}
			switch m.Meta.Preferences[name][value] {
			case fileformat.Never:
				if !f.IsFilter() {
					errs = append(errs, fmt.Errorf("preference %s=%s: never needs a field whose role includes filter, %q has role %s", name, value, name, f.Role))
				}
			case fileformat.Like, fileformat.Dislike:
				if !f.IsPreference() {
					errs = append(errs, fmt.Errorf("preference %s=%s: like and dislike need a field whose role includes preference, %q has role %s", name, value, name, f.Role))
				}
			}
		}
	}
	return errs
}

func (d *Dataset) resolveList(m *Member, ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if d.OnShelf(id) || slices.ContainsFunc(d.Acquisition, func(it fileformat.Item) bool { return it.ID == id }) {
			out[id] = true
			continue
		}
		d.Notices = append(d.Notices, Notice{Kind: UnknownListItem, Member: m.Label, ID: id})
	}
	return out
}

func sortedItems(items []fileformat.Item) []fileformat.Item {
	out := slices.Clone(items)
	slices.SortFunc(out, func(a, b fileformat.Item) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func factsByID(items []fileformat.Item) map[string]map[string]schema.Value {
	out := make(map[string]map[string]schema.Value, len(items))
	for _, it := range items {
		out[it.ID] = it.Facts
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
