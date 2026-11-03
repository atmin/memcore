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
	pos, err := parseInterleaved(flags, args)
	if err != nil {
		return usageErr(err.Error())
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErr("usage: memcore draft <kind> [<topic>] [--description <text>] [--at <dir>]")
	}
	kind, topic := pos[0], ""
	if len(pos) == 2 {
		topic = pos[1]
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
	d, err := template(kind, topic, strings.TrimSpace(*description), level, *executable)
	if err != nil {
		return err
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
	if d.inSkill != "" {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(path.Join(folder, "skills", d.inSkill, "SKILL.md")))); err != nil {
			return inputErrf("no skill %s at %s: memcore draft skill %s starts it", d.inSkill, title(level), d.inSkill)
		}
	}
	if kind == "script" {
		if err := freeScriptName(repo, topic); err != nil {
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
func template(kind, topic, description, level string, executable bool) (draft, error) {
	needsTopic := map[string]bool{"plan": true, "note": true, "script": true, "helper": true, "skill": true}
	needsDescription := map[string]bool{"plan": true, "note": true, "script": true}
	switch {
	case needsTopic[kind] && topic == "":
		return draft{}, usageErr(fmt.Sprintf("usage: memcore draft %s <topic>", kind))
	case !needsTopic[kind] && topic != "":
		return draft{}, usageErr(fmt.Sprintf("usage: memcore draft %s, with no topic", kind))
	case (needsDescription[kind] || (kind == "skill" && !strings.Contains(topic, "/"))) && description == "":
		return draft{}, usageErr(fmt.Sprintf("memcore draft %s needs --description, the line that the index shows", kind))
	case strings.ContainsAny(description, "\r\n"):
		return draft{}, inputErrf("the description is one line, with no line break")
	}
	front := func() string {
		fm := store.Frontmatter{Keys: []string{"name", "description"}, Values: map[string]string{"name": topic, "description": description}}
		return string(fm.Bytes())
	}
	switch kind {
	case "plan":
		if !docNameRE.MatchString(topic) {
			return draft{}, inputErrf("the topic %s is not lower case with hyphens", topic)
		}
		body, err := planTemplate()
		if err != nil {
			return draft{}, err
		}
		return draft{path: "plans/" + topic + ".md", content: front() + body, mode: 0o644}, nil
	case "note":
		if !docNameRE.MatchString(topic) {
			return draft{}, inputErrf("the topic %s is not lower case with hyphens", topic)
		}
		content := fmt.Sprintf("%s\n# %s\n\nFindings as of %s.\n", front(), heading(topic), time.Now().Format("2006-01-02"))
		return draft{path: "notes/" + topic + ".md", content: content, mode: 0o644}, nil
	case "script", "helper":
		if !binNameRE.MatchString(topic) {
			return draft{}, inputErrf("the name %s is not lower case letters, digits, dots, underscores and hyphens", topic)
		}
		if kind == "helper" {
			return draft{path: "bin/" + topic, mode: 0o644}, nil
		}
		content := fmt.Sprintf("#!/bin/sh\n# %s\n# Usage: %s\nset -eu\n", description, topic)
		return draft{path: "bin/" + topic, content: content, mode: 0o755}, nil
	case "skill":
		if name, file, ok := strings.Cut(topic, "/"); ok {
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
		if !docNameRE.MatchString(topic) {
			return draft{}, inputErrf("the name %s is not lower case with hyphens", topic)
		}
		return draft{path: "skills/" + topic + "/SKILL.md", content: fmt.Sprintf("%s\n# %s\n", front(), heading(topic)), mode: 0o644}, nil
	case "conventions":
		return draft{path: "conventions.md", content: fmt.Sprintf("# Conventions of %s\n", title(level)), mode: 0o644}, nil
	case "glossary":
		content := fmt.Sprintf("# Glossary of %s\n\nThe documents at this level use each term below with one meaning only.\n", title(level))
		return draft{path: "glossary.md", content: content, mode: 0o644}, nil
	}
	return draft{}, usageErr(fmt.Sprintf("no kind %q: the kinds are plan, note, script, helper, skill, conventions and glossary", kind))
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

// heading turns a topic into a title, as slow-search into Slow search.
func heading(topic string) string {
	words := strings.ReplaceAll(topic, "-", " ")
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
		accepted := filepath.Join(store.StateDir(repo), "accepted") + string(filepath.Separator)
		if target, err := os.Readlink(found); err != nil || !strings.HasPrefix(target, accepted) {
			return inputErrf("the command %s on PATH already holds the name %s: choose another name", found, name)
		}
	}
	return nil
}
