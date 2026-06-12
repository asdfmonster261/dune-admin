package main

import (
	_ "embed"
	"encoding/json"
)

// Embedded skill-module catalog for SkillsSetModuleLevel's Module
// field. Each entry carries:
//   - tag:     the gameplay tag the dispatcher matches against
//   - tree:    ESkillTree enum name (BeneGesserit / Mentat / Trooper /
//              Swordmaster / Planetologist / Hidden / Dev)
//   - display: human-readable name from the DT row's DisplayName FText
//
// Source: live REPL dump of DT_TrainingModules rows (Tag.TagName +
// SkillArea enum + DisplayName). Regenerate after a Funcom rebuild by
// re-running the Lua snippet in the commit-message footer of the
// pak-tools probe script.

//go:embed skill_modules.json
var skillModulesJSON []byte

type SkillModule struct {
	Tag     string `json:"tag"`
	Tree    string `json:"tree"`
	Display string `json:"display"`
}

var skillModulesList = func() []SkillModule {
	var out []SkillModule
	_ = json.Unmarshal(skillModulesJSON, &out)
	return out
}()
