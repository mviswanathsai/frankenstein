package contextprovider

import (
	"path/filepath"
	"strings"
)

type sourceKind string

const (
	sourceKindFile         sourceKind = "file"
	sourceKindClaudeRule   sourceKind = "claude_rule"
	sourceKindCursorRule   sourceKind = "cursor_rule"
	sourceKindSkill        sourceKind = "skill"
	sourceKindSkillIndex   sourceKind = "skill_index"
	sourceKindDirectoryRef sourceKind = "directory_ref"
)

type classification struct {
	Slot       string
	Recognized bool
}

func classifyBaseFilename(path string) classification {
	switch filepath.Base(path) {
	case "USER.md":
		return classification{Slot: SlotUserProfile, Recognized: true}
	case "MEMORY.md":
		return classification{Slot: SlotMemory, Recognized: true}
	case "AGENTS.md", "AGENTS.override.md", "CLAUDE.md", "CLAUDE.local.md", "CURSOR.md", ".cursorrules", "HERMES.md", ".hermes.md":
		return classification{Slot: SlotProjectInstructions, Recognized: true}
	case "SOUL.md":
		return classification{Slot: SlotIdentity, Recognized: true}
	default:
		return classification{Slot: SlotUnknown, Recognized: false}
	}
}

func classifySource(path string, kind sourceKind) classification {
	switch kind {
	case sourceKindClaudeRule, sourceKindCursorRule:
		return classification{Slot: SlotProjectInstructions, Recognized: true}
	case sourceKindSkill, sourceKindSkillIndex:
		return classification{Slot: SlotSkills, Recognized: true}
	case sourceKindDirectoryRef:
		return classification{Slot: SlotUnknown, Recognized: true}
	default:
		classified := classifyBaseFilename(path)
		if classified.Recognized {
			return classified
		}
		if filepath.Base(path) == "SKILL.md" {
			return classification{Slot: SlotSkills, Recognized: true}
		}
		if strings.HasSuffix(path, ".mdc") && pathHasComponent(path, ".cursor") && pathHasComponent(path, "rules") {
			return classification{Slot: SlotProjectInstructions, Recognized: true}
		}
		return classified
	}
}

func pathHasComponent(path, component string) bool {
	for _, part := range splitPath(path) {
		if part == component {
			return true
		}
	}
	return false
}

func splitPath(path string) []string {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	if volume != "" {
		cleaned = strings.TrimPrefix(cleaned, volume)
	}
	cleaned = strings.Trim(cleaned, string(filepath.Separator))
	if cleaned == "" {
		return nil
	}
	return strings.Split(cleaned, string(filepath.Separator))
}
