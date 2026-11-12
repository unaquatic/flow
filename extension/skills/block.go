package skills

import (
	"os"
	"strings"
)

// Block is the text of a prompt that invoked a skill: the skill's
// instructions, followed by what the prompt asks of the skill.
type Block struct {
	// Body is the skill's instructions, its SKILL.md without the
	// frontmatter, as they read when the prompt was sent.
	Body string
	// Args is what the prompt asks of the skill, the text that followed
	// its command, or empty.
	Args string
}

// Text returns the text a prompt that invokes the skill is stored and sent
// as: Body, and then, a blank line under it, Args, unless it is empty.
func (b Block) Text() string {
	if b.Args == "" {
		return b.Body
	}
	return b.Body + "\n\n" + b.Args
}

// NewBlock reads the instructions of s, its SKILL.md without the
// frontmatter, into the Block of a prompt that invokes s and asks args of
// it.
func NewBlock(s Skill, args string) (Block, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return Block{}, err
	}
	_, body, err := SplitFrontmatter(string(data))
	if err != nil {
		return Block{}, err
	}
	return Block{Body: strings.TrimSpace(body), Args: args}, nil
}
