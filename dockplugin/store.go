package dockplugin

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Quadrants: an Eisenhower matrix flattened to one answer. 0 means "not
// sorted yet" (the inbox).
const (
	QInbox = iota
	QDo
	QSchedule
	QDelegate
	QDrop
)

var quadrantNames = [...]string{"Inbox", "Do now", "Schedule", "Delegate", "Drop"}
var quadrantHints = [...]string{"", "urgent + important", "important, not urgent", "urgent, not important", "neither"}

// Item is one todo. Only Text is required; everything else is optional.
type Item struct {
	ID       int       `json:"id"`
	Text     string    `json:"text"`
	Quadrant int       `json:"quadrant,omitempty"`
	Context  string    `json:"context,omitempty"` // e.g. code, people
	Who      string    `json:"who,omitempty"`     // person it relates to
	Due      string    `json:"due,omitempty"`     // YYYY-MM-DD
	Done     bool      `json:"done,omitempty"`
	Created  time.Time `json:"created"`
	DoneAt   time.Time `json:"done_at,omitzero"`
}

type fileData struct {
	NextID int     `json:"next_id"`
	Items  []*Item `json:"items"`
}

// Store keeps all items in one JSON file, written atomically after every change.
type Store struct {
	path   string
	nextID int
	Items  []*Item
}

func OpenStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "todos.json"), nextID: 1}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	s.Items, s.nextID = d.Items, max(d.NextID, 1)
	for _, it := range s.Items {
		s.nextID = max(s.nextID, it.ID+1)
	}
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(fileData{NextID: s.nextID, Items: s.Items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Add(it Item) (*Item, error) {
	it.ID, it.Created = s.nextID, time.Now()
	s.nextID++
	p := &it
	s.Items = append(s.Items, p)
	return p, s.save()
}

// Update replaces the stored item that has the same ID.
func (s *Store) Update(it Item) error {
	for i, o := range s.Items {
		if o.ID == it.ID {
			c := it
			s.Items[i] = &c
			return s.save()
		}
	}
	return errors.New("item not found")
}

func (s *Store) Delete(id int) error {
	for i, o := range s.Items {
		if o.ID == id {
			s.Items = append(s.Items[:i:i], s.Items[i+1:]...)
			return s.save()
		}
	}
	return nil
}

func (s *Store) Get(id int) *Item {
	for _, o := range s.Items {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (s *Store) SetDone(id int, done bool) error {
	it := s.Get(id)
	if it == nil {
		return nil
	}
	c := *it
	c.Done = done
	c.DoneAt = time.Time{}
	if done {
		c.DoneAt = time.Now()
	}
	return s.Update(c)
}

// Counts returns open items per quadrant.
func (s *Store) Counts() (c [5]int) {
	for _, it := range s.Items {
		if !it.Done {
			c[it.Quadrant]++
		}
	}
	return
}

// groupOrder is the order sections appear in: what needs doing now first,
// then what still needs a decision.
var groupOrder = []int{QDo, QInbox, QSchedule, QDelegate, QDrop}

// Visible returns the items to show, grouped in groupOrder and sorted by due
// date (undated last) then age. Done items are left out unless showDone.
func (s *Store) Visible(filter string, showDone bool) []*Item {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []*Item
	for _, q := range groupOrder {
		var g []*Item
		for _, it := range s.Items {
			if it.Quadrant != q || (it.Done && !showDone) || !matches(it, filter) {
				continue
			}
			g = append(g, it)
		}
		sort.SliceStable(g, func(i, j int) bool {
			a, b := g[i], g[j]
			if a.Done != b.Done {
				return !a.Done
			}
			if (a.Due == "") != (b.Due == "") {
				return a.Due != ""
			}
			if a.Due != b.Due {
				return a.Due < b.Due
			}
			return a.ID < b.ID
		})
		out = append(out, g...)
	}
	return out
}

func matches(it *Item, f string) bool {
	if f == "" {
		return true
	}
	return strings.Contains(strings.ToLower(it.Text+" "+it.Context+" "+it.Who), f)
}

// Config lives in ConfigDir/config.json. Every field is optional.
type Config struct {
	// Ask lists the questions "tab" walks through when adding: any of
	// quadrant, context, who, due.
	Ask []string `json:"ask"`
	// Contexts are the answers offered for the context question.
	Contexts []string `json:"contexts"`
}

func DefaultConfig() Config {
	return Config{
		Ask:      []string{"quadrant", "context", "who", "due"},
		Contexts: []string{"code", "people", "other"},
	}
}

func LoadConfig(dir string) Config {
	c := DefaultConfig()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return c
	}
	var u Config
	if json.Unmarshal(b, &u) != nil {
		return c
	}
	if u.Ask != nil {
		c.Ask = u.Ask
	}
	if len(u.Contexts) > 0 {
		c.Contexts = u.Contexts[:min(len(u.Contexts), 9)]
	}
	return c
}
