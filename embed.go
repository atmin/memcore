// Package memcore holds the files that the memcore executable ships.
package memcore

import "embed"

// Skills holds the skills that init seeds into a new memory repo, and the plan template that draft starts a plan from.
//
//go:embed skills/voice/SKILL.md skills/plan/TEMPLATE.md
var Skills embed.FS
