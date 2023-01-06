package kibela

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Songmu/kibelasync/client"
	"golang.org/x/xerrors"
)

// Note represents note of Kibela
type Note struct {
	ID          `json:"id"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	CoEditing   bool           `json:"coediting"`
	Folders     []*FolderInput `json:"folders"`
	Groups      []*Group       `json:"groups"`
	Author      User           `json:"author"`
	UpdatedAt   Time           `json:"updatedAt"`
	PublishedAt Time           `json:"publishedAt"`
	Summary     string         `json:"summary"`
}

func (n *Note) toMD(dir string) *MD {
	groups := make([]string, len(n.Groups))
	for i, g := range n.Groups {
		groups[i] = g.Name
	}
	author := ""
	if !n.CoEditing {
		author = n.Author.Account
	}
	return &MD{
		ID:        n.ID,
		Content:   n.Content,
		UpdatedAt: n.UpdatedAt.Time,
		dir:       dir,
		FrontMatter: &Meta{
			Title:   n.Title,
			Folders: n.Folders,
			Groups:  groups,
			Author:  author,
		},
	}
}

func (ki *Kibela) getNotesCount(ctx context.Context, folderID ID) (int, error) {
	data, err := ki.cli.Do(ctx, &client.Payload{Query: totalCountQuery(folderID)})
	if err != nil {
		return 0, xerrors.Errorf("failed to ki.getNotesCount: %w", err)
	}
	var res struct {
		Notes struct {
			TotalCount int `json:"totalCount"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return 0, xerrors.Errorf("failed to ki.getNotesCount: %w", err)
	}
	return res.Notes.TotalCount, nil
}

const (
	// max query cost limit per request is 10,000
	// so adjust limit size to not exceed it.
	// 100 (base) + 2 (id, updatedAt) * 4900 = 9900
	bundleLimit = 4900
	// 100 (base) + 3 (id, updatedAt, cursor) * 3200 = 9700
	pageLimit = 3200
)

// OK
func (ki *Kibela) listNoteIDs(ctx context.Context, folderID ID, limit int) ([]*Note, error) {
	num, err := ki.getNotesCount(ctx, folderID)
	if err != nil {
		return nil, xerrors.Errorf("failed to ki.listNodeIDs: %w", err)
	}
	if limit > 0 && limit < num {
		num = limit
	}
	if num > bundleLimit {
		nextCursor := ""
		rest := num
		notes := make([]*Note, 0, num)
		for rest > 0 {
			take := pageLimit
			if take > rest {
				take = rest
			}
			rest = rest - take
			data, err := ki.cli.Do(ctx, &client.Payload{
				Query: listNotePaginateQuery(take, folderID, nextCursor, limit > 0)})
			if err != nil {
				return nil, xerrors.Errorf("failed to ki.getGroups: %w", err)
			}
			var res struct {
				Notes struct {
					Edges []struct {
						Node   *Note  `json:"node"`
						Cursor string `json:"cursor"`
					} `json:"edges"`
				} `json:"notes"`
			}
			if err := json.Unmarshal(data, &res); err != nil {
				return nil, xerrors.Errorf("failed to ki.listNoteIDs: %w", err)
			}
			if len(res.Notes.Edges) > 0 {
				nextCursor = res.Notes.Edges[len(res.Notes.Edges)-1].Cursor
			}
			for _, n := range res.Notes.Edges {
				notes = append(notes, n.Node)
			}
		}
		return notes, nil
	}
	data, err := ki.cli.Do(ctx, &client.Payload{Query: listNoteQuery(num, folderID, limit > 0)})
	if err != nil {
		return nil, xerrors.Errorf("failed to ki.listNoteIDs: %w", err)
	}
	var res struct {
		Notes struct {
			Nodes []*Note `json:"nodes"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, xerrors.Errorf("failed to ki.getNotesCount: %w", err)
	}
	return res.Notes.Nodes, nil
}

// GetNote gets kibela note
func (ki *Kibela) GetNote(ctx context.Context, num int) (*Note, error) {
	id := newID(idTypeBlog, num)
	return ki.getNote(ctx, id)
}

// OK
func (ki *Kibela) getNote(ctx context.Context, id ID) (*Note, error) {
	data, err := ki.cli.Do(ctx, &client.Payload{Query: getNoteQuery(id)})
	if err != nil {
		return nil, xerrors.Errorf("failed to ki.getNote: %w", err)
	}
	var res struct {
		Note *Note `json:"note"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, xerrors.Errorf("failed to ki.getNote: %w", err)
	}
	res.Note.ID = id
	return res.Note, nil
}

// PullNotes pulls notes from Kibela
func (ki *Kibela) PullNotes(ctx context.Context, dir, folder string, limit int) error {
	var folderID ID
	if folder != "" {
		var err error
		folderID, err = ki.fetchFolderID(ctx, folder)
		if err != nil {
			return xerrors.Errorf("failed to PullNotes: %w", err)
		}
	}
	notes, err := ki.listNoteIDs(ctx, folderID, limit)
	if err != nil {
		return xerrors.Errorf("failed to pullNotes: %w", err)
	}
	for _, n := range notes {
		localT := time.Time{}
		idNum, err := n.ID.Number()
		if err != nil {
			return xerrors.Errorf("failed to pullNotes: %w", err)
		}
		mdFilePath := filepath.Join(dir, fmt.Sprintf("%d.md", idNum))
		_, err = os.Stat(mdFilePath)
		if err == nil {
			localMD, err := LoadMD(mdFilePath)
			if err != nil {
				return xerrors.Errorf("failed to pullNotes: %w", err)
			}
			localT = localMD.UpdatedAt
		}
		if n.UpdatedAt.After(localT) {
			allNote, err := ki.getNote(ctx, n.ID)
			if err != nil {
				return xerrors.Errorf("failed to pullNotes: %w", err)
			}
			if err := allNote.toMD(dir).save(); err != nil {
				return xerrors.Errorf("failed to pullNotes: %w", err)
			}
		} else {
			log.Printf("skip %q (not modfied)\n", mdFilePath)
		}
	}
	return nil
}

const pullBundleLimit = 100

// PullFullNotes pull full notes from Kibela
func (ki *Kibela) PullFullNotes(ctx context.Context, dir, folder string, limit int) error {
	var folderID ID
	if folder != "" {
		var err error
		folderID, err = ki.fetchFolderID(ctx, folder)
		if err != nil {
			return xerrors.Errorf("failed to PullFullNotes: %w", err)
		}
	}
	num, err := ki.getNotesCount(ctx, folderID)
	if err != nil {
		return xerrors.Errorf("failed to ki.pullFullNotes: %w", err)
	}
	if limit > 0 && limit < num {
		num = limit
	}
	nextCursor := ""
	rest := num
	for rest > 0 {
		take := pullBundleLimit
		if take > rest {
			take = rest
		}
		rest = rest - take
		data, err := ki.cli.Do(ctx, &client.Payload{
			Query: listFullNotePaginateQuery(take, folderID, nextCursor, limit > 0)})
		if err != nil {
			return xerrors.Errorf("failed to ki.pullFullNotes: %w", err)
		}
		var res struct {
			Notes struct {
				Edges []struct {
					Node   *Note  `json:"node"`
					Cursor string `json:"cursor"`
				} `json:"edges"`
			} `json:"notes"`
		}
		if err := json.Unmarshal(data, &res); err != nil {
			return xerrors.Errorf("failed to ki.pullFullNotes: %w", err)
		}
		if len(res.Notes.Edges) > 0 {
			nextCursor = res.Notes.Edges[len(res.Notes.Edges)-1].Cursor
		}
		for _, e := range res.Notes.Edges {
			if err := e.Node.toMD(dir).save(); err != nil {
				return xerrors.Errorf("failed to pullFullNotes while saving md: %w", err)
			}
		}
	}
	return nil
}

// PullNote pulls a single note
func (ki *Kibela) PullNote(ctx context.Context, dir, arg string) error {
	var (
		id     ID
		isFile bool
	)
	num, err := strconv.Atoi(arg)
	if err == nil {
		id = newID(idTypeBlog, num)
	} else {
		fname := filepath.Base(arg)
		stuffs := strings.Split(fname, ".")
		if len(stuffs) != 2 {
			return xerrors.Errorf("failed to pullNote: invalid filename (must be a number or [0-9]+.md): %s", fname)
		}
		if stuffs[1] != "md" {
			return xerrors.Errorf("failed to pullNote: invalid filename (must be a number or [0-9]+.md): %s", fname)
		}
		num, err := strconv.Atoi(stuffs[0])
		if err != nil {
			return xerrors.Errorf("failed to pullNote: invalid filename (must be a number or [0-9]+.md): %s", fname)
		}
		isFile = true
		id = newID(idTypeBlog, num)
	}
	n, err := ki.getNote(ctx, id)
	if err != nil {
		return xerrors.Errorf("failed to pullNote while getNote(%s): %w", id, err)
	}
	m := n.toMD(dir)
	if isFile {
		m.filepath = arg
	}
	if err := m.save(); err != nil {
		return xerrors.Errorf("failed to pullNote while m.save: %w", err)
	}
	return nil
}

func (ki *Kibela) pushNote(ctx context.Context, n *Note) error {
	remoteNote, err := ki.getNote(ctx, n.ID)
	if err != nil {
		return xerrors.Errorf("failed to pushNote: %w", err)
	}
	groupMap := make(map[string]ID)
	for _, g := range remoteNote.Groups {
		groupMap[g.Name] = g.ID
	}

	// fill group IDs
	for _, g := range n.Groups {
		if string(g.ID) == "" {
			id, ok := groupMap[g.Name]
			if ok {
				g.ID = id
			} else {
				id, err := ki.fetchGroupID(ctx, g.Name)
				if err != nil {
					return xerrors.Errorf("failed to fetch group name: %q, %w", g.Name, err)
				}
				g.ID = id
			}
		}
	}
	baseNote := remoteNote.toNoteInput()
	newNote := n.toNoteInput()
	if reflect.DeepEqual(*baseNote, *newNote) {
		// no update defferences
		n.UpdatedAt = remoteNote.UpdatedAt
		return nil
	}
	data, err := ki.cli.Do(ctx, &client.Payload{
		Query: updateNoteMutation,
		Variables: struct {
			ID       ID         `json:"id"`
			BaseNote *noteInput `json:"baseNote"`
			NewNote  *noteInput `json:"newNote"`
		}{
			ID:       n.ID,
			BaseNote: baseNote,
			NewNote:  newNote,
		},
	})
	if err != nil {
		return xerrors.Errorf("failed to pushNote while accessing remote: %w", err)
	}
	var res struct {
		UpdateNote struct {
			Note *Note `json:"note"`
		} `json:"updateNote"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return xerrors.Errorf("failed to ki.pushNote while unmarshaling response: %w", err)
	}
	if res.UpdateNote.Note == nil {
		return xerrors.New("failed to update kibela on any reason. null updateNote was returned")
	}
	n.Author.Account = res.UpdateNote.Note.Author.Account
	log.Printf("updated %s", ki.noteURL(n))
	n.UpdatedAt = res.UpdateNote.Note.UpdatedAt
	return nil
}

func (n *Note) toNoteInput() *noteInput {
	groupIDs := make([]string, len(n.Groups))
	for i, g := range n.Groups {
		groupIDs[i] = string(g.ID)
	}
	sort.Strings(groupIDs)
	return &noteInput{
		Title:     n.Title,
		Content:   strings.TrimSpace(n.Content) + "\n",
		GroupIDs:  groupIDs,
		Folders:   n.Folders,
		CoEditing: n.CoEditing,
	}
}

func (ki *Kibela) noteURL(n *Note) string {
	basePath := "notes"
	if !n.CoEditing {
		basePath = "@" + n.Author.Account
	}
	num, _ := n.ID.Number()
	return fmt.Sprintf("https://%s.kibe.la/%s/%d", ki.team, basePath, num)
}
