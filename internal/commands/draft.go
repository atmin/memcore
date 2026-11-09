package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atmin/memcore"
	"github.com/atmin/memcore/internal/guards"
	"github.com/atmin/memcore/internal/store"
)

var (
	docNameRE  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	binNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	filePartRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// runDraft starts a document of any kind from its template at its level, and commits it.
func runDraft(env Env, args []string) error {
	flags := flag.NewFlagSet("draft", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	description := flags.String("description", "", "the line that the index shows")
	at := flags.String("at", "", "start the document at the level of this directory, on the path to home")
	executable := flags.Bool("exec", false, "make a file inside a skill executable")
	topic := flags.String("topic", "", "start a note in this topic folder of the notes folder")
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErr("usage: memcore draft <kind> [<name>] [--description <text>] [--at <dir>] [--topic <topic>]")
	}
	kind, name := pos[0], ""
	if len(pos) == 2 {
		name = pos[1]
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	h, err := home()
	if err != nil {
		return err
	}
	level, err := placeAt(env.Dir, *at, h)
	if err != nil {
		return err
	}
	folder := store.Folder(level)
	d, err := template(kind, name, strings.TrimSpace(*description), level, *executable)
	if err != nil {
		return err
	}
	if *topic != "" {
		if kind != "note" {
			return usageErr("--topic places only a note: memcore remember --topic places a memory")
		}
		if err := topicPath(*topic); err != nil {
			return err
		}
		d.path = path.Join("notes", *topic, name+".md")
	}
	rel := path.Join(folder, d.path)

	unlock, err := lockWrite(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := noSymlink(repo, rel); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel))); err == nil {
		return inputErrf("%s exists: memcore revise changes it", rel)
	}
	if *topic != "" {
		if err := needTopic(repo, level, "notes", *topic); err != nil {
			return err
		}
	}
	if kind == "topic" {
		if parent := path.Dir(name); strings.Contains(parent, "/") {
			kindFolder, sub, _ := strings.Cut(parent, "/")
			if err := needTopic(repo, level, kindFolder, sub); err != nil {
				return err
			}
		}
	}
	if d.inSkill != "" {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(path.Join(folder, "skills", d.inSkill, "SKILL.md")))); err != nil {
			return inputErrf("no skill %s at %s: memcore draft skill %s starts it", d.inSkill, title(level), d.inSkill)
		}
	}
	if kind == "script" {
		if err := freeScriptName(repo, name); err != nil {
			return err
		}
	}
	if found := guards.Secrets(d.content); len(found) > 0 {
		return inputErrf("the draft holds a likely secret, so draft wrote nothing: remove the secret from the description")
	}
	if strings.HasSuffix(rel, ".md") {
		report, err := guards.VerifyContent(repo, rel, []byte(d.content))
		if err != nil {
			return err
		}
		for _, f := range report.Faults {
			fmt.Fprintf(env.Out, "%s:%d: fault: %s\n", f.Path, f.Line, f.Message)
		}
		if len(report.Faults) > 0 {
			return inputErrf("%d faults, so draft wrote nothing", len(report.Faults))
		}
	}
	made, err := apply(repo, change{writes: []file{{path: rel, content: []byte(d.content), mode: d.mode}}, message: "draft: " + rel})
	if err != nil {
		return err
	}
	env.event().Paths, env.event().Made = []string{rel}, []string{made}
	fmt.Fprintf(env.Out, "Drafted %s. Fill it with memcore revise %s --reason <text>.\n", rel, rel)
	return nil
}

// draft is a new document: its path from the folder of its level, its content and its mode.
type draft struct {
	path, content string
	mode          fs.FileMode
	inSkill       string // the skill that a file inside a skill belongs to
}

// template returns the draft of a kind, from its template.
func template(kind, name, description, level string, executable bool) (draft, error) {
	needsName := map[string]bool{"plan": true, "note": true, "script": true, "helper": true, "skill": true, "topic": true}
	needsDescription := map[string]bool{"plan": true, "note": true, "script": true, "topic": true}
	switch {
	case needsName[kind] && name == "":
		return draft{}, usageErr(fmt.Sprintf("usage: memcore draft %s <name>", kind))
	case !needsName[kind] && name != "":
		return draft{}, usageErr(fmt.Sprintf("usage: memcore draft %s, with no name", kind))
	case (needsDescription[kind] || (kind == "skill" && !strings.Contains(name, "/"))) && description == "":
		return draft{}, usageErr(fmt.Sprintf("memcore draft %s needs --description, the line that the index shows", kind))
	case strings.ContainsAny(description, "\r\n"):
		return draft{}, inputErrf("the description is one line, with no line break")
	}
	front := func() string {
		fm := store.Frontmatter{Keys: []string{"name", "description"}, Values: map[string]string{"name": name, "description": description}}
		return string(fm.Bytes())
	}
	switch kind {
	case "plan":
		if !docNameRE.MatchString(name) {
			return draft{}, inputErrf("the name %s is not lower case with hyphens", name)
		}
		body, err := planTemplate()
		if err != nil {
			return draft{}, err
		}
		return draft{path: "plans/" + name + ".md", content: front() + body, mode: 0o644}, nil
	case "note":
		if !docNameRE.MatchString(name) {
			return draft{}, inputErrf("the name %s is not lower case with hyphens", name)
		}
		content := fmt.Sprintf("%s\n# %s\n\nFindings as of %s.\n", front(), heading(name), time.Now().Format("2006-01-02"))
		return draft{path: "notes/" + name + ".md", content: content, mode: 0o644}, nil
	case "script", "helper":
		if !binNameRE.MatchString(name) {
			return draft{}, inputErrf("the name %s is not lower case letters, digits, dots, underscores and hyphens", name)
		}
		if kind == "helper" {
			return draft{path: "bin/" + name, mode: 0o644}, nil
		}
		content := fmt.Sprintf("#!/bin/sh\n# %s\n# Usage: %s\nset -eu\n", description, name)
		return draft{path: "bin/" + name, content: content, mode: 0o755}, nil
	case "skill":
		if name, file, ok := strings.Cut(name, "/"); ok {
			if !docNameRE.MatchString(name) {
				return draft{}, inputErrf("the skill name %s is not lower case with hyphens", name)
			}
			for _, part := range strings.Split(file, "/") {
				if !filePartRE.MatchString(part) {
					return draft{}, inputErrf("%s is not a file name inside a skill", file)
				}
			}
			mode := fs.FileMode(0o644)
			if executable {
				mode = 0o755
			}
			return draft{path: "skills/" + name + "/" + file, mode: mode, inSkill: name}, nil
		}
		if !docNameRE.MatchString(name) {
			return draft{}, inputErrf("the name %s is not lower case with hyphens", name)
		}
		return draft{path: "skills/" + name + "/SKILL.md", content: fmt.Sprintf("%s\n# %s\n", front(), heading(name)), mode: 0o644}, nil
	case "topic":
		kindFolder, sub, _ := strings.Cut(name, "/")
		if (kindFolder != "memories" && kindFolder != "notes") || sub == "" {
			return draft{}, inputErrf("a topic folder sits in memories or notes, as memories/<topic>: %s does not", name)
		}
		if err := topicPath(sub); err != nil {
			return draft{}, err
		}
		fm := store.Frontmatter{Keys: []string{"name", "description"}, Values: map[string]string{"name": path.Base(name), "description": description}}
		return draft{path: name + "/README.md", content: string(fm.Bytes()), mode: 0o644}, nil
	case "conventions":
		return draft{path: "conventions.md", content: fmt.Sprintf("# Conventions of %s\n", title(level)), mode: 0o644}, nil
	case "glossary":
		content := fmt.Sprintf("# Glossary of %s\n\nThe documents at this level use each term below with one meaning only.\n", title(level))
		return draft{path: "glossary.md", content: content, mode: 0o644}, nil
	}
	return draft{}, usageErr(fmt.Sprintf("no kind %q: the kinds are plan, note, script, helper, skill, topic, conventions and glossary", kind))
}

// topicPath checks the path of a topic folder inside its kind folder, as stedo/cost: each part is lower case
// with hyphens, and no part is a reserved name of a level.
func topicPath(topic string) error {
	for _, part := range strings.Split(topic, "/") {
		if !docNameRE.MatchString(part) {
			return inputErrf("the topic %s is not lower case with hyphens, as stedo/cost", topic)
		}
		if slices.ContainsFunc(store.Reserved, func(r string) bool { return strings.EqualFold(r, part) }) {
			return inputErrf("the topic %s uses the reserved name %s: choose another name", topic, part)
		}
	}
	return nil
}

// needTopic refuses a topic folder that does not exist, and names the draft that starts it.
func needTopic(repo, level, kindFolder, topic string) error {
	index := path.Join(store.Folder(level), kindFolder, topic, "README.md")
	if err := noSymlink(repo, index); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(index))); err != nil {
		return inputErrf("no topic folder %s/%s at %s: memcore draft topic %s/%s starts it", kindFolder, topic, title(level), kindFolder, topic)
	}
	return nil
}

// planTemplate returns the body of the plan template that memcore ships, after its frontmatter.
func planTemplate() (string, error) {
	b, err := memcore.Skills.ReadFile("skills/plan/TEMPLATE.md")
	if err != nil {
		return "", fmt.Errorf("read the shipped plan template: %w", err)
	}
	_, block, ok := strings.Cut(string(b), "```markdown\n")
	if ok {
		block, _, ok = strings.Cut(block, "\n```")
	}
	if !ok {
		return "", errors.New("the shipped plan template has no markdown block")
	}
	_, body, err := store.ParseFrontmatter(strings.NewReader(block + "\n"))
	if err != nil {
		return "", fmt.Errorf("read the shipped plan template: %w", err)
	}
	return "\n" + body, nil
}

// heading turns a name into a title, as slow-search into Slow search.
func heading(name string) string {
	words := strings.ReplaceAll(name, "-", " ")
	return strings.ToUpper(words[:1]) + words[1:]
}

// freeScriptName refuses a script name that another script of the memory repo or a command on PATH holds.
// A command on PATH that is memcore's own link into the accepted checkout does not count.
func freeScriptName(repo, name string) error {
	var taken string
	err := filepath.WalkDir(repo, func(f string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != name || filepath.Base(filepath.Dir(f)) != "bin" {
			return nil
		}
		rel, _ := filepath.Rel(repo, f)
		rel = filepath.ToSlash(rel)
		folder := path.Dir(path.Dir(rel))
		if folder == "." {
			folder = ""
		}
		info, err := d.Info()
		if _, isLevel := store.LevelOf(folder); !isLevel || err != nil || info.Mode()&0o111 == 0 {
			return nil // a helper, or a file in the bin folder of a skill
		}
		taken = rel
		return fs.SkipAll
	})
	if err != nil {
		return fmt.Errorf("look up the script name %s: %w", name, err)
	}
	if taken != "" {
		return inputErrf("the script name %s is taken by %s: a script name is unique in the memory repo", name, taken)
	}
	if found, err := exec.LookPath(name); err == nil || errors.Is(err, exec.ErrDot) {
		if target, err := os.Readlink(found); err != nil || !ours(repo, target) {
			return inputErrf("the command %s on PATH already holds the name %s: choose another name", found, name)
		}
	}
	return nil
}
