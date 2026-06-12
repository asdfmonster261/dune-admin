package main

import (
	_ "embed"
	"strings"
)

// Embedded list of valid SkillsSetModuleLevel "Module" values, RE'd from
// DT_TrainingModules at runtime via the mini-UE4SS REPL (2026-06-12).
// The dispatcher's UExperienceUtils::SetSkillsModuleLevel(player, FName, level)
// looks the module up via UGameplayTagsManager — passing a tag NOT in this
// list silently no-ops. Regenerate after a Funcom rebuild by re-running the
// REPL dump (probe_envelope.py path / see commit message).

//go:embed skill_modules.txt
var skillModulesRaw string

var skillModulesList = func() []string {
	var out []string
	for _, line := range strings.Split(skillModulesRaw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}()
