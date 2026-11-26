package provision

import "path/filepath"

// Pi is the only harness this package installs into. The two directories and
// the artefact names are pi's. This function is the whole adapter: the plan
// and the link rules take a Harness and never ask which one it is.
//
// Pi loads ~/.pi/agent/extensions/*.ts by directory and discovers skill
// directories under ~/.pi/agent/skills, so the extension is a link file and
// the skill is a link directory.
func Pi(home string) Harness {
	dir := filepath.Join(home, ".pi", "agent")
	return Harness{
		Name:          "pi",
		Dir:           dir,
		ExtensionsDir: filepath.Join(dir, "extensions"),
		SkillsDir:     filepath.Join(dir, "skills"),
	}
}
