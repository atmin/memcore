// Package memcore holds the files that the memcore executable ships.
package memcore

import "embed"

// Skills holds the skills that init seeds into a new memory repo.
//
//go:embed skills/voice/SKILL.md
var Skills embed.FS
