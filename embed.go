// Package memcore holds the files that the memcore executable ships.
package memcore

import "embed"

// Skills holds the voice skill, which init seeds into a new memory repo.
// It also holds the plan template that draft starts a plan from.
//
//go:embed skills/voice/SKILL.md skills/plan/TEMPLATE.md
var Skills embed.FS
