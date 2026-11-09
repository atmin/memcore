package guards

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atmin/memcore/internal/store"
)

// edit replaces the bytes from start to end of a document.
type edit struct {
	start, end int
	text       string
}

// Relink returns the new content of each markdown document in the repo that links to the document at from,
// once that document moves to to. A path link to from points to to, relative to its own document. When memory
// is true, each [[oldName]] becomes [[newName]]. Code stays as it is. The level indexes stay out, because a
// write regenerates them. The moved document is returned under from, with its own [[oldName]] renamed.
func Relink(repo, from, to, oldName, newName string, memory bool) (map[string][]byte, error) {
	changed := map[string][]byte{}
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(f, ".md") {
			return nil
		}
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			return err
		}
		p := filepath.ToSlash(rel)
		if isIndex(p) && !store.IsTopicIndex(p) {
			return nil
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if out, ok := relink(p, string(content), from, to, oldName, newName, memory); ok {
			changed[p] = []byte(out)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find the links to %s: %w", from, err)
	}
	return changed, nil
}

// relink rewrites the links of one document at p, and reports whether it changed anything.
func relink(p, text, from, to, oldName, newName string, memory bool) (string, bool) {
	masked := maskCode(text)
	var edits []edit
	for _, l := range links(masked) {
		raw := masked[l.target[0]:l.target[1]]
		if t, ok := retarget(p, raw, from, to); ok {
			edits = append(edits, edit{l.target[0], l.target[1], t})
		}
	}
	if memory && oldName != newName {
		for _, m := range wikiRE.FindAllStringSubmatchIndex(masked, -1) {
			if strings.TrimSpace(masked[m[2]:m[3]]) == oldName {
				edits = append(edits, edit{m[0], m[1], "[[" + newName + "]]"})
			}
		}
	}
	if len(edits) == 0 {
		return text, false
	}
	slices.SortFunc(edits, func(a, b edit) int { return b.start - a.start })
	for _, e := range edits {
		text = text[:e.start] + e.text + text[e.end:]
	}
	return text, true
}

// retarget returns the new target of a link in the document at p, when the link points to from. It keeps the
// angle brackets, the anchor and the query of the target, and an absolute target stays absolute.
func retarget(p, raw, from, to string) (string, bool) {
	t := raw
	open, close := "", ""
	if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
		open, close, t = "<", ">", t[1:len(t)-1]
	}
	if t == "" || strings.HasPrefix(t, "#") || schemeRE.MatchString(t) {
		return "", false
	}
	suffix := ""
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t, suffix = t[:i], t[i:]
	}
	if u, err := url.PathUnescape(t); err == nil {
		t = u
	}
	var target string
	if strings.HasPrefix(t, "/") {
		target = path.Clean(strings.TrimPrefix(t, "/"))
	} else {
		target = path.Join(path.Dir(p), t)
	}
	if target != from {
		return "", false
	}
	var next string
	if strings.HasPrefix(t, "/") {
		next = "/" + to
	} else {
		base := path.Dir(p)
		if p == from {
			base = path.Dir(to)
		}
		r, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(to))
		if err != nil {
			return "", false
		}
		next = filepath.ToSlash(r)
	}
	return open + (&url.URL{Path: next}).EscapedPath() + suffix + close, true
}
