// Package heymodel holds the row types hey-cli's renderer draws.
//
// Copied from basecamp/hey-cli internal/mail (MIT). The struct definitions are
// kept verbatim so the renderer in internal/tui/heyui compiles unchanged; the
// constructors that built them from HEY's SDK are dropped, because here they
// are built from Proton conversations instead.
package heymodel

import "time"

// Posting is one row of a source: a thread, a bundle or a single entry as HEY lists it.
// The timestamps are the ones HEY served, so whatever shows one decides how it reads —
// formatting a time into a string and parsing it back is how a reader east of UTC ends
// up looking at yesterday's date.
type Posting struct {
	ID                    int64
	TopicID               int64
	CreatedAt             time.Time
	Name                  string
	Summary               string
	AlternativeSenderName string
	Seen                  bool
	// IsBundle marks a row that is a bundle of one contact's unseen threads. It comes
	// from the posting's kind — HEY's `bundled` flag means filed *inside* a bundle.
	IsBundle          bool
	BubbledUp         bool
	Muted             bool
	VisibleEntryCount int32
	Creator           Contact
	Extenzions        []Extenzion
	Folders           []Folder
	Collections       []Collection
}

// Contact is who a posting came from.
type Contact struct {
	ID           int64
	Name         string
	EmailAddress string
}

// Extenzion is the HEY extension — a group address — a posting arrived through.
type Extenzion struct {
	ID   int64
	Name string
}

// Folder is a label a posting is filed under.
type Folder struct {
	ID   int64
	Name string
}

// Collection is a collection a posting's topic belongs to.
type Collection struct {
	ID   int64
	Name string
}

// Kind is what a source is: a system box, a user folder, or a collection.
type Kind string

const (
	KindBox        Kind = "box"
	KindFolder     Kind = "folder"
	KindCollection Kind = "collection"
)

// Source is a list the renderer is drawing: which box, folder or label the
// postings came from.
//
// hey-cli carries HEY's own BoxKind here to tell the named boxes apart. This
// version uses Imbox instead, because only the Imbox splits "New for You" from
// "Previously Seen".
type Source struct {
	Kind  Kind
	ID    string
	Name  string
	Imbox bool
}

// IsImbox reports whether this source is the Imbox, which is the only list
// that separates unread from read.
func (s Source) IsImbox() bool { return s.Kind == KindBox && s.Imbox }
