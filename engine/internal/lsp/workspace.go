package lsp

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	// maxWorkspaceFiles and maxWorkspaceFileSize bound what the server reads of
	// a workspace to find CustomResourceDefinitions, so a huge folder never
	// holds up the first answers.
	maxWorkspaceFiles    = 20000
	maxWorkspaceFileSize = 1 << 20

	registerWatchersID = "cel-lens-watch-yaml"
)

// File change types of workspace/didChangeWatchedFiles.
const fileDeleted = 3

var windowsDrive = regexp.MustCompile(`^/[A-Za-z]:`)

// crdKey names the file at uri in the session's CRDs. File URIs are named by
// their path, so the open document and the file on disk, which a client may
// spell differently (percent-encoding, drive letter), are one entry; any other
// URI is named by itself.
func crdKey(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	if windowsDrive.MatchString(u.Path) {
		return u.Path[1:]
	}
	return u.Path
}

// diskPath is the path of the file at uri, false for a URI that is no file.
func diskPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	return filepath.FromSlash(crdKey(uri)), true
}

// initializeParams are the parts of initialize that locate the workspace.
type initializeParams struct {
	RootURI          string `json:"rootUri"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
	Capabilities struct {
		Workspace struct {
			DidChangeWatchedFiles struct {
				DynamicRegistration bool `json:"dynamicRegistration"`
			} `json:"didChangeWatchedFiles"`
		} `json:"workspace"`
	} `json:"capabilities"`
}

// folders returns the folders of the workspace as paths.
func (p initializeParams) folders() []string {
	var uris []string
	for _, f := range p.WorkspaceFolders {
		uris = append(uris, f.URI)
	}
	if len(uris) == 0 && p.RootURI != "" {
		uris = append(uris, p.RootURI)
	}
	var out []string
	for _, uri := range uris {
		if path, ok := diskPath(uri); ok {
			out = append(out, path)
		}
	}
	return out
}

// scanWorkspace reads the YAML files below the workspace folders into the
// session's CRDs, skipping hidden folders and node_modules, files over
// maxWorkspaceFileSize and files past maxWorkspaceFiles. A folder or file that
// cannot be read is skipped. It stops when ctx is cancelled.
func (ss *session) scanWorkspace(ctx context.Context, folders []string) {
	seen := 0
	for _, dir := range folders {
		root := os.DirFS(dir)
		_ = fs.WalkDir(root, ".", func(rel string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fs.SkipDir // skip what cannot be read
			}
			if d.IsDir() {
				if name := d.Name(); rel != "." && (strings.HasPrefix(name, ".") || name == "node_modules") {
					return fs.SkipDir
				}
				return nil
			}
			if !isYAML(rel) {
				return nil
			}
			seen++
			if seen > maxWorkspaceFiles {
				return fs.SkipAll
			}
			info, err := d.Info()
			if err != nil || info.Size() > maxWorkspaceFileSize {
				return nil //nolint:nilerr // a file that cannot be read is skipped
			}
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if data, err := os.ReadFile(path); err == nil {
				ss.crds.Set(ctx, crdKey(pathURI(path)), data)
			}
			return nil
		})
	}
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// pathURI is the file URI of an absolute path.
func pathURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a Windows drive
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// registerWatchers asks the client, if it can, to send
// workspace/didChangeWatchedFiles for YAML files, so CRDs that change on disk
// without being open are followed.
func (ss *session) registerWatchers() error {
	if !ss.watchFiles {
		return nil
	}
	return ss.conn.Request(registerWatchersID, "client/registerCapability", map[string]any{
		"registrations": []map[string]any{{
			"id":     registerWatchersID,
			"method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{
				"watchers": []map[string]any{{"globPattern": "**/*.{yaml,yml}"}},
			},
		}},
	})
}

// didChangeWatchedFiles reads the changed files again, or forgets the deleted
// ones, and checks the open documents again when the CRDs changed. A file that
// is open keeps the text of the editor.
func (ss *session) didChangeWatchedFiles(ctx context.Context, params json.RawMessage) error {
	var p struct {
		Changes []struct {
			URI  string `json:"uri"`
			Type int    `json:"type"`
		} `json:"changes"`
	}
	if !decode(params, &p) {
		return nil
	}
	changed := false
	for _, c := range p.Changes {
		if _, open := ss.docs[c.URI]; open || !isYAML(crdKey(c.URI)) {
			continue
		}
		if c.Type == fileDeleted {
			changed = ss.crds.Forget(crdKey(c.URI)) || changed
		} else {
			changed = ss.readFromDisk(ctx, c.URI) || changed
		}
	}
	if !changed {
		return nil
	}
	return ss.publishAll(ctx, "")
}

// readFromDisk records the CRDs of the file at uri as it is on disk, and
// forgets the file when it cannot be read. It reports whether the CRDs changed.
func (ss *session) readFromDisk(ctx context.Context, uri string) bool {
	path, ok := diskPath(uri)
	if !ok {
		return ss.crds.Forget(crdKey(uri))
	}
	if info, err := os.Stat(path); err != nil || info.Size() > maxWorkspaceFileSize {
		return ss.crds.Forget(crdKey(uri))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ss.crds.Forget(crdKey(uri))
	}
	return ss.crds.Set(ctx, crdKey(uri), data)
}

// publishAll checks the open documents again and sends their diagnostics,
// except the one at skip, in the order of their URIs.
func (ss *session) publishAll(ctx context.Context, skip string) error {
	uris := make([]string, 0, len(ss.docs))
	for uri := range ss.docs {
		if uri != skip {
			uris = append(uris, uri)
		}
	}
	slices.Sort(uris)
	for _, uri := range uris {
		if err := ss.publish(ctx, uri); err != nil {
			return err
		}
	}
	return nil
}
