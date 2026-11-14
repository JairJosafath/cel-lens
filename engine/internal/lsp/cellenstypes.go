package lsp

import (
	"context"
	"maps"
	"slices"

	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// lensesOffText says that the types of .cellens are off, for the messages of
// the paths that ask readsLenses.
const lensesOffText = "giving a value a type of your own in .cellens is the experimental feature cellens-types, which is off"

// lensNoticeID is the id of the request that tells the user, once, that a
// .cellens of the workspace is not read (see noticeLenses).
const lensNoticeID = "cel-lens-cellens-types-off"

// readsLenses reports whether the session reads .cellens files: whether the
// experimental feature cellens-types is on. It is the one place that asks;
// every path that would read a .cellens, or show what one gives, asks it
// once. Off, the session acts as if no .cellens existed: none is searched
// for or read, so none types a value or gets diagnostics, hover or
// completion, the line above a rule shows no Give it a type or Edit, the
// commands behind them fail, and hover says the feature is off where a
// .cellens could type a dyn value.
func (ss *session) readsLenses() bool { return ss.features.Has(feature.CellensTypes) }

// unreadLens is a .cellens the editor has open while cellens-types is off:
// its URI, and the editor's text and version of it.
type unreadLens struct {
	uri     string
	text    []byte
	version int
}

// keepUnread keeps the editor's text of the .cellens at uri, open while
// cellens-types is off, unread, for when the feature turns on.
func (ss *session) keepUnread(uri string, text []byte, version int) {
	if ss.unread == nil {
		ss.unread = map[string]unreadLens{}
	}
	ss.unread[crdKey(uri)] = unreadLens{uri: uri, text: text, version: version}
}

// switchLenses follows cellens-types turning on or off; was is whether it was
// on. Turning it off stops the search in progress and forgets every .cellens,
// keeping the editor's text of the open ones unread, and counts as the
// notice (see noticeLenses): the person who just turned it off is not told
// to turn it on. Turning it on stops the
// probe in progress, reads the open ones with the editor's text and searches
// the workspace folders for the others, for a batch of its own. The caller
// checks the open documents again, which also sends or takes back the
// diagnostics of the .cellens files.
func (ss *session) switchLenses(ctx context.Context, was bool) {
	on := ss.readsLenses()
	if on == was {
		return
	}
	ss.stopLensSearch()
	if !on {
		ss.lensNoticed = true
		for _, key := range slices.Sorted(maps.Keys(ss.lenses)) {
			if l := ss.lenses[key]; l.open {
				ss.keepUnread(l.uri, l.text, l.version)
			}
			ss.dropLens(key)
		}
		return
	}
	for _, key := range slices.Sorted(maps.Keys(ss.unread)) {
		u := ss.unread[key]
		ss.putLens(ctx, u.uri, u.text, true)
		ss.lenses[key].version = u.version
	}
	ss.unread = nil
	ss.batch++
	ss.searchLenses(ctx, ss.folders)
}

// probeLenses looks in folders, in a goroutine, for whether they hold a
// .cellens, which it reads none of, while cellens-types is off: the first it
// finds is what the notice names (see noticeLenses). It does nothing once
// the notice was sent; while a probe runs, the folders wait for it to end.
func (ss *session) probeLenses(ctx context.Context, folders []string) {
	if ss.lensNoticed || len(folders) == 0 {
		return
	}
	if ss.lensSearch != nil {
		ss.lensQueued = outermost(append(ss.lensQueued, folders...))
		return
	}
	ss.runLensSearch(ctx, outermost(folders), nil, true)
}

// endProbe follows the end of a probe, which found found: it sends the
// notice for the .cellens it found, or else probes the folders queued
// meanwhile that still exist.
func (ss *session) endProbe(ctx context.Context, found lensFound) error {
	queued := slices.DeleteFunc(ss.lensQueued, func(f string) bool { return !isDir(f) })
	ss.lensQueued, ss.lensQueuedBatches = nil, nil
	if keys := slices.Sorted(maps.Keys(found.files)); len(keys) > 0 {
		return ss.noticeLenses(keys[0])
	}
	ss.probeLenses(ctx, queued)
	return nil
}

// noticeLenses tells the user, once per session, that the .cellens with the
// key is not read because cellens-types is off, with a window/showMessageRequest
// whose one action turns the feature on: its experimental names the
// feature, for the client to add to its setting of experimental features at
// the scope that sets it. The server only asks; the client changes the
// setting; the message says so in words too, for a client whose button does
// nothing. It does nothing for an empty key, or once the notice was sent or
// the feature turned off in this session.
func (ss *session) noticeLenses(key string) error {
	if key == "" || ss.lensNoticed {
		return nil
	}
	ss.lensNoticed = true
	return ss.conn.Request(lensNoticeID, "window/showMessageRequest", map[string]any{
		"type":    messageWarning,
		"message": "cel-lens does not read " + ss.keySource(key) + ": " + lensesOffText + ": add cellens-types to the experimental features (cel-lens.experimental in VS Code) to read it.",
		"actions": []map[string]any{{"title": "Turn on " + string(feature.CellensTypes), "experimental": string(feature.CellensTypes)}},
	})
}
