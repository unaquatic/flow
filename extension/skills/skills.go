// Package skills is the extension that gives the model skills, as the Agent
// Skills standard describes them (agentskills.io): folders that each hold a
// SKILL.md, instructions for one kind of task that open with a name and a
// description, and the scripts and references the instructions name. It
// plugs into flow at three of the points package extension describes:
//
//   - Behaviour: through extension.SystemHook it lists each skill's name,
//     description and path in the system prompt of a new session, so that
//     the model reads a skill's SKILL.md once a task calls for it; and
//     through extension.ExpandHook it expands the text of a prompt that
//     invokes a skill, "/skill:name what to do", before it becomes the
//     prompt's text part, into the skill's instructions followed by "what
//     to do", as Block writes it, so that the model reads
//     the instructions whatever it would choose. It adds no content of its
//     own.
//   - Terminal UI: package ui offers a /skill:name command for each skill.
//
// It finds skills in the user's ~/.agents/skills, and in the .agents/skills
// of each folder of the project, from the working directory up to the root
// of its git repository, as Roots names them. Its Settings are the "skills"
// entry of the config file's extensions section.
package skills

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/internal/project"
	"github.com/unaquatic/flow/internal/realpath"
)

// Name names the extension, in the extensions section of the config file,
// which holds its Settings.
const Name = "skills"

// File is the name of the file that makes a folder a skill.
const File = "SKILL.md"

// Dir is the folder, under the home directory and under each folder of a
// project, that holds skills, as Roots names them.
var Dir = filepath.Join(".agents", "skills")

// CommandPrefix starts a prompt that invokes a skill, as in
// "/skill:pdf-tools extract report.pdf".
const CommandPrefix = "/skill:"

// MaxNameLength and MaxDescriptionLength are the longest name and
// description the Agent Skills standard allows. A skill that exceeds either
// is still found, with a warning.
const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
)

// Settings are the extension's settings, which the config file holds as
// {"extensions": {"skills": {...}}}.
type Settings struct {
	// Enabled turns skills on or off. Nil turns them on.
	Enabled *bool `json:"enabled,omitempty"`
}

// On reports whether the settings turn skills on.
func (s Settings) On() bool { return s.Enabled == nil || *s.Enabled }

// Skill is a skill that Find found.
type Skill struct {
	// Name names the skill, in the system prompt and in its command. It is
	// the frontmatter's name, or else the name of the skill's folder.
	Name string
	// Description tells the model what the skill does and when it applies.
	Description string
	// Path is the absolute path of the skill's SKILL.md.
	Path string
	// DisableModelInvocation keeps the skill out of the system prompt, as
	// the frontmatter's disable-model-invocation sets it, so that it runs
	// only when a prompt invokes it.
	DisableModelInvocation bool
}

// Dir returns the skill's folder, which the paths its instructions name are
// relative to.
func (s Skill) Dir() string { return filepath.Dir(s.Path) }

// Command returns the prompt that invokes the skill, as in
// "/skill:pdf-tools".
func (s Skill) Command() string { return CommandPrefix + s.Name }

// Roots returns the folders Find looks in for skills, in the order a name
// found in an earlier one wins: Dir under home, and then Dir under each
// folder of workingDir's project, nearest first, as project.Dirs yields
// them. Either may be empty, for none.
func Roots(home, workingDir string) []string {
	var roots []string
	if home != "" {
		roots = append(roots, filepath.Join(home, Dir))
	}
	for dir := range project.Dirs(workingDir) {
		roots = append(roots, filepath.Join(dir, Dir))
	}
	return roots
}

// Find returns the skills in roots, in order: in each root, each folder that
// holds a File, found depth first in the order of their names, is a skill,
// and the folders under it are not looked in. Folders whose name starts with
// a dot, and node_modules, are passed over; links are followed, and a file a
// root found already, by another path, is not read again. A root that does
// not exist holds no skills.
//
// A skill whose name an earlier skill has is left out, as is one whose File
// cannot be read or holds no description. Each is reported in the error,
// joined as errors.Join joins them, with a warning for a name or description
// that the standard does not allow, whose skill is found all the same. The
// skills are found whatever the error.
func Find(roots ...string) ([]Skill, error) {
	var (
		skills []Skill
		errs   []error
		files  = map[string]bool{}
		dirs   = map[string]bool{}
	)
	add := func(path string) {
		real := realpath.Of(path)
		if files[real] {
			return
		}
		files[real] = true
		s, err := Read(path)
		if err != nil {
			errs = append(errs, err)
			if s.Description == "" {
				return
			}
		}
		if i := slices.IndexFunc(skills, func(o Skill) bool { return o.Name == s.Name }); i >= 0 {
			errs = append(errs, fmt.Errorf("skill %s: %s: name taken by %s", s.Name, path, skills[i].Path))
			return
		}
		skills = append(skills, s)
	}
	var walk func(dir string)
	walk = func(dir string) {
		real := realpath.Of(dir)
		if dirs[real] {
			return
		}
		dirs[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			return
		}
		if st, err := os.Stat(filepath.Join(dir, File)); err == nil && st.Mode().IsRegular() {
			add(filepath.Join(dir, File))
			return
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if st, err := os.Stat(sub); err == nil && st.IsDir() {
				walk(sub)
			}
		}
	}
	for _, root := range roots {
		if abs, err := filepath.Abs(root); err == nil {
			walk(abs)
		}
	}
	return skills, errors.Join(errs...)
}

// Read reads the skill whose SKILL.md is at path. It fails when the file
// cannot be read, or its frontmatter, and when the description is missing;
// then the Skill holds no Description, and is no skill. A name or a
// description that the standard does not allow, as Validate tells, is an
// error too, but then the Skill is whole.
func Read(path string) (Skill, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Skill{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	fm, _, err := SplitFrontmatter(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("skill %s: %w", path, err)
	}
	s := Skill{
		Name:                   cmp.Or(strings.TrimSpace(fm["name"]), filepath.Base(filepath.Dir(path))),
		Description:            strings.TrimSpace(fm["description"]),
		Path:                   path,
		DisableModelInvocation: fm.Bool("disable-model-invocation"),
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("skill %s: %s: no description", s.Name, path)
	}
	if err := s.Validate(); err != nil {
		return s, fmt.Errorf("skill %s: %s: %w", s.Name, path, err)
	}
	return s, nil
}

// Validate reports what the Agent Skills standard does not allow of the
// skill's name and description: a name longer than MaxNameLength, or of
// characters other than lowercase letters, digits and single hyphens
// between them, and a description longer than MaxDescriptionLength.
func (s Skill) Validate() error {
	var errs []error
	if n := len(s.Name); n > MaxNameLength {
		errs = append(errs, fmt.Errorf("name is %d characters, more than %d", n, MaxNameLength))
	}
	valid := func(r rune) bool { return r == '-' || 'a' <= r && r <= 'z' || '0' <= r && r <= '9' }
	if strings.IndexFunc(s.Name, func(r rune) bool { return !valid(r) }) >= 0 {
		errs = append(errs, errors.New("name holds characters other than a-z, 0-9 and -"))
	}
	if strings.HasPrefix(s.Name, "-") || strings.HasSuffix(s.Name, "-") || strings.Contains(s.Name, "--") {
		errs = append(errs, errors.New("name starts or ends with a hyphen, or holds two in a row"))
	}
	if n := len(s.Description); n > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("description is %d characters, more than %d", n, MaxDescriptionLength))
	}
	return errors.Join(errs...)
}

// Lookup returns the skill among skills named name, and reports whether
// there is one.
func Lookup(skills []Skill, name string) (Skill, bool) {
	i := slices.IndexFunc(skills, func(s Skill) bool { return s.Name == name })
	if i < 0 {
		return Skill{}, false
	}
	return skills[i], true
}

// ParseCommand returns the name of the skill that text invokes, as in
// "/skill:pdf-tools extract report.pdf", and what follows it, without the
// spaces around it, and reports whether text invokes one.
func ParseCommand(text string) (name, args string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), CommandPrefix)
	if !ok {
		return "", "", false
	}
	name = rest
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		name, args = rest[:i], strings.TrimSpace(rest[i:])
	}
	return name, args, name != ""
}

// ListingTool returns the name of the tool among tools that the model reads
// a skill's file with, as Listing tells it: read, or else bash, or "" when
// tools hold neither.
func ListingTool(tools []agent.ToolDefinition) string {
	for _, name := range []string{"read", "bash"} {
		if slices.ContainsFunc(tools, func(t agent.ToolDefinition) bool { return t.Name == name }) {
			return name
		}
	}
	return ""
}

// ListingTag is the tag of the section of the system prompt that lists the
// skills, as flow tags each section of it.
const ListingTag = "skill_instructions"

// AvailableTag is the tag that holds the list of skills in the system
// prompt, as the Agent Skills standard names it.
const AvailableTag = "available_skills"

// Listing returns the section of the system prompt that lists skills, in a
// ListingTag tag, each tag on a line of its own: what skills are for, how to
// load one with tool, the tool that reads files, and how to read the paths
// its instructions name, and then each skill's name, description and
// location in an AvailableTag tag, escaped, as the Agent Skills standard
// writes them. A skill whose DisableModelInvocation is set is left out, and
// so is the whole listing when none is left, or when tool is "", since the
// model could load none.
func Listing(skills []Skill, tool string) string {
	skills = slices.DeleteFunc(slices.Clone(skills), func(s Skill) bool { return s.DisableModelInvocation })
	if len(skills) == 0 || tool == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s>\n", ListingTag)
	b.WriteString("The following skills provide specialized instructions for specific tasks.\n")
	fmt.Fprintf(&b, "Use the %s tool to load a skill's file when the task matches its description.\n", tool)
	b.WriteString("When a skill file references a relative path, resolve it against the skill directory (the folder that holds SKILL.md) and use that absolute path in tool commands.\n\n")
	fmt.Fprintf(&b, "<%s>\n", AvailableTag)
	for _, s := range skills {
		b.WriteString("  <skill>\n")
		fmt.Fprintf(&b, "    <name>%s</name>\n", html.EscapeString(s.Name))
		fmt.Fprintf(&b, "    <description>%s</description>\n", html.EscapeString(s.Description))
		fmt.Fprintf(&b, "    <location>%s</location>\n", html.EscapeString(s.Path))
		b.WriteString("  </skill>\n")
	}
	fmt.Fprintf(&b, "</%s>\n</%s>", AvailableTag, ListingTag)
	return b.String()
}

// Extension finds skills, lists them in the system prompt of each new
// session, and expands the text of a prompt that invokes a skill into the
// skill's instructions. It looks for them again each time, so that a skill
// added, changed or removed takes effect with the next session, or prompt,
// without a restart. It implements extension.SystemHook and
// extension.ExpandHook.
type Extension struct {
	// Roots are the folders it finds skills in, as Find looks in them.
	Roots []string
}

var (
	_ extension.SystemHook = (*Extension)(nil)
	_ extension.ExpandHook = (*Extension)(nil)
)

// New returns the extension over roots.
func New(roots ...string) *Extension { return &Extension{Roots: roots} }

// Load loads the extension into the app that host describes, with the Roots
// of the user's home directory and host's working directory, or loads none
// when its Settings turn skills off. A home directory that cannot be found
// leaves the user's skills out.
func Load(_ context.Context, host extension.Host) (extension.Extension, error) {
	var st Settings
	if err := host.Config.Extension(Name, &st); err != nil {
		return nil, err
	}
	if !st.On() {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("skills: no home directory", "err", err)
	}
	return New(Roots(home, host.WorkingDir)...), nil
}

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Skills returns the skills in Roots, as Find finds them, and logs what Find
// reports.
func (e *Extension) Skills() []Skill {
	skills, err := Find(e.Roots...)
	if err != nil {
		slog.Warn("skills", "err", err)
	}
	return skills
}

// SystemPrompt returns the Listing of the skills, for the tool among s's
// tools that ListingTool picks.
func (e *Extension) SystemPrompt(_ context.Context, s extension.System) string {
	tool := ListingTool(s.Tools)
	if tool == "" {
		return ""
	}
	return Listing(e.Skills(), tool)
}

// ExpandPrompt expands the text of a prompt that invokes a skill, as
// ParseCommand reads it, into the skill's instructions as they read as it
// is sent, and then what the prompt asks of the skill, as Block.Text writes
// them; the command itself is kept nowhere. A prompt that names no skill is
// not expanded, nor one whose file can no longer be read, which is logged;
// it is sent as it was typed.
func (e *Extension) ExpandPrompt(_ context.Context, p extension.Prompt) (string, bool) {
	name, args, ok := ParseCommand(p.Text)
	if !ok {
		return "", false
	}
	s, ok := Lookup(e.Skills(), name)
	if !ok {
		return "", false
	}
	b, err := NewBlock(s, args)
	if err != nil {
		slog.Warn("skills: read invoked skill", "skill", s.Name, "path", s.Path, "err", err)
		return "", false
	}
	return b.Text(), true
}
