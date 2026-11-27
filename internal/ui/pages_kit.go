package ui

import (
	"fmt"
	"strconv"
	"strings"

	txt "github.com/CurtisD4q/pmin-base/text"
	mint "github.com/CurtisD4q/pmin-kit"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/paths"
)

// The pages other than Play, made of Mint's own rows: tables for the sets,
// recordings and set list, pickers for the settings, folding groups for the
// sounds and plain lines for the help. Mint moves the cursor and runs
// Enter; susynth's letters (a, n, d, i, [ ]) act on the row under it.

// listScreen fills s with page p's rows.
func (a *App) listScreen(s *mint.Screen, p page) {
	switch p {
	case pageSets:
		s.TopTitle = pageNames[p]
		s.TopInfo = a.pageInfo
		s.TopWidth = 0.55
		s.Sections = []mint.Section{{Rows: []mint.Row{mint.Table(mint.TableOpts{
			ID:      "sets",
			Headers: []string{"Name", "Tracks", "Lines"},
			Columns: []mint.Column{{}, {Right: true}, {Right: true, Dim: true}},
			Get: func() [][]string {
				var rows [][]string
				for _, st := range a.ps.sets {
					rows = append(rows, []string{st.name, fmt.Sprint(st.tracks), fmt.Sprint(st.lines)})
				}
				return rows
			},
			Mark: func(i int, _ []string) mint.Mark {
				if i < len(a.ps.sets) && a.ps.sets[i].name == a.setName {
					// ● the open set: accent, or amber with unsaved changes
					if a.dirty {
						return mint.Mark{Glyph: "●", Kind: mint.BadgeWarn}
					}
					return mint.Mark{Glyph: "●", Kind: mint.BadgeInfo}
				}
				return mint.Mark{}
			},
			OnSelect: func(c *mint.Ctx, i int, _ []string) { a.onRow(c, i, key{kind: kEnter}) },
			Empty:    "no saved sets yet — press ^S on the Play page",
		})}}}
		s.Panes = []*mint.Pane{a.detailsPane(s)}
	case pageRecs:
		s.TopTitle = pageNames[p]
		s.TopInfo = a.pageInfo
		s.TopWidth = 0.55
		s.Sections = []mint.Section{{Rows: []mint.Row{mint.Table(mint.TableOpts{
			ID:      "recordings",
			Headers: []string{"File", "Length", "Size"},
			Columns: []mint.Column{{}, {Right: true}, {Right: true, Dim: true}},
			Get: func() [][]string {
				var rows [][]string
				for _, r := range a.ps.recs {
					secs := int(r.dur.Seconds())
					length := fmt.Sprintf("%d:%02d", secs/60, secs%60)
					if strings.HasSuffix(r.name, ".mid") {
						length = "midi"
					}
					rows = append(rows, []string{r.name, length, txt.Bytes(r.size)})
				}
				return rows
			},
			OnSelect: func(c *mint.Ctx, i int, _ []string) { a.onRow(c, i, key{kind: kEnter}) },
			Empty:    "no recordings yet — press ^R while playing",
		})}}}
		s.Panes = []*mint.Pane{a.detailsPane(s)}
	case pagePerform:
		s.Context = a.pageInfo
		now := func() string {
			if a.live >= 0 && a.live < len(a.setlist) {
				return "▶ " + a.setlist[a.live]
			}
			return "—"
		}
		next := func() string {
			if n := a.live + 1; n >= 0 && n < len(a.setlist) {
				return a.setlist[n]
			}
			return "—"
		}
		s.Sections = []mint.Section{
			{Rows: []mint.Row{
				mint.Value(mint.ValueOpts{Label: "Now", Get: now}),
				mint.Value(mint.ValueOpts{Label: "Next", Get: next}),
			}},
			{Name: "Set list", Rows: []mint.Row{mint.Table(mint.TableOpts{
				ID:      "setlist",
				Headers: []string{"#", "Set", "State"},
				Columns: []mint.Column{{Right: true, Dim: true}},
				Get: func() [][]string {
					var rows [][]string
					for i, n := range a.setlist {
						rows = append(rows, []string{fmt.Sprint(i + 1), n, a.setState(i)})
					}
					return rows
				},
				Mark: func(i int, _ []string) mint.Mark {
					if i >= len(a.setlist) {
						return mint.Mark{}
					}
					if p, err := setPath(a.setlist[i]); err != nil || !exists(p) {
						return mint.Mark{Glyph: "!", Kind: mint.BadgeError}
					}
					if i == a.live {
						return mint.Mark{Glyph: "▶", Kind: mint.BadgeOK}
					}
					return mint.Mark{}
				},
				OnSelect: func(c *mint.Ctx, i int, _ []string) { a.onRow(c, i, key{kind: kEnter}) },
				Empty:    "the set list is empty — open Esc 2 Sets, select a set and press a to add it",
			})}},
		}
	case pageSettings:
		s.Context = a.pageInfo
		var rows []mint.Row
		for i := range settingRows {
			i := i
			rows = append(rows, mint.Picker(mint.PickerOpts{
				ID:    "set:" + strconv.Itoa(i),
				Label: settingRows[i].label,
				Get:   func() string { return a.settingValue(i) },
				Step: func(d int) {
					// ←→ change it; Enter (d is 0) edits or reloads where
					// the hint says so, and otherwise steps it on.
					a.ps.sel = i
					if d == 0 {
						a.changeSetting(i, 1, true)
						return
					}
					a.changeSetting(i, d, false)
				},
			}))
		}
		s.Sections = []mint.Section{{Rows: rows}}
		s.Refresh = func(s *mint.Screen) {
			a.syncSel(s)
			s.Dock = []mint.Section{{Rows: []mint.Row{mint.Note(settingRows[min(a.ps.sel, int(numSettings)-1)].hint)}}}
		}
	case pageSounds:
		s.Context = a.pageInfo
		list := a.sounds()
		var sections []mint.Section
		for _, g := range soundGroups {
			var rows []mint.Row
			for _, it := range list {
				if it.group != g.name {
					continue
				}
				it := it
				hint := "▶ preview"
				if it.word == "" {
					hint = "i insert"
				}
				rows = append(rows, mint.Action(mint.ActionOpts{
					ID:    "snd:" + it.group + "/" + it.name,
					Label: pad(it.name, 20) + " " + it.desc,
					Hint:  hint,
					Does: func() string {
						if it.word == "" {
							return "i inserts an example"
						}
						return "plays a preview"
					},
					OnActivate: func(c *mint.Ctx) { a.audition(it) },
				}))
			}
			if len(rows) == 0 {
				rows = append(rows, mint.Note("none loaded — put .wav files in "+paths.SamplesDir()))
			}
			sections = append(sections, mint.CollapsibleSection(g.name, a.ps.closed[g.name], rows...))
		}
		s.Sections = sections
		s.Refresh = func(s *mint.Screen) {
			line, how := blurbOf(soundGroups[0].name), "Enter or ←→ opens and folds this group"
			if it, ok := a.selectedSound(s); ok {
				line, how = "try it: "+strings.ReplaceAll(it.example, "\n", " ⏎ "), "Enter plays a preview · i inserts this line into your set"
			} else if sel, ok := s.Selected(); ok {
				if b := blurbOf(strings.TrimSpace(strings.TrimLeft(sel.Label, "▾▸ "))); b != "" {
					line = b
				}
			}
			s.Dock = []mint.Section{{Rows: []mint.Row{mint.Note(line), mint.Note(how)}}}
		}
	case pageHelp:
		s.Context = a.pageInfo
		var rows []mint.Row
		for _, l := range helpLines {
			switch {
			case l.head:
				rows = append(rows, mint.Heading(l.text))
			case l.key != "":
				rows = append(rows, mint.Note(pad(l.key, 22)+" "+l.text))
			case l.text == "":
				rows = append(rows, mint.Spacer())
			default:
				rows = append(rows, mint.Note(l.text))
			}
		}
		s.Sections = []mint.Section{{Rows: rows}}
	}
}

// detailsPane is the row under the cursor in full, beside the list.
func (a *App) detailsPane(s *mint.Screen) *mint.Pane {
	return &mint.Pane{Title: "Details", Beside: true, Width: 1 - s.TopWidth,
		Draw: a.paneDraw(func(f *frame, w, h int) (int, int) {
			a.syncSel(s)
			a.drawDetails(f, 0, 0, w, h)
			return -1, -1
		})}
}

// setState is the State column of the set list.
func (a *App) setState(i int) string {
	state := ""
	if i == a.live {
		state = "live"
	}
	if p, err := setPath(a.setlist[i]); err != nil || !exists(p) {
		state = strings.TrimSpace(state + " file missing")
	}
	return state
}

// syncSel makes a.ps.sel the row under Mint's cursor.
func (a *App) syncSel(s *mint.Screen) {
	sel, ok := s.Selected()
	if !ok {
		return
	}
	switch a.page {
	case pageSets, pageRecs, pagePerform:
		if sel.Cells != nil {
			a.ps.sel = sel.Index
		}
	case pageSettings:
		if n, ok := strings.CutPrefix(sel.ID, "set:"); ok {
			if i, err := strconv.Atoi(n); err == nil {
				a.ps.sel = i
			}
		}
	}
}

// selectedSound is the sound under Mint's cursor on the Sounds page.
func (a *App) selectedSound(s *mint.Screen) (soundItem, bool) {
	sel, ok := s.Selected()
	if !ok {
		return soundItem{}, false
	}
	id, ok := strings.CutPrefix(sel.ID, "snd:")
	if !ok {
		return soundItem{}, false
	}
	for _, it := range a.sounds() {
		if it.group+"/"+it.name == id {
			return it, true
		}
	}
	return soundItem{}, false
}

// onRow is Enter on row i of a list page, as Mint reports it.
func (a *App) onRow(c *mint.Ctx, i int, k key) {
	a.ctx = c
	defer func() { a.ctx = nil }()
	a.ps.sel = i
	a.pageKey(k)
}

// audition plays a sound from the Sounds page.
func (a *App) audition(it soundItem) {
	if it.word == "" {
		a.say("effects change other sounds — i inserts an example to try", false)
		return
	}
	if err := a.eng.Audition(it.word, it.params, engine.SampleRate/3); err != nil {
		a.say(err.Error(), true)
	}
}
