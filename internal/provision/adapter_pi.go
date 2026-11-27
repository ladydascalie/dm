package provision

import "path/filepath"

// Pi is the only harness this package installs into today, and this function
// is the whole adapter: the directories, the artefact names and the mounts.
// The plan and the link rules take a Harness and never ask which one it is.
//
// Pi loads ~/.pi/agent/extensions/*.ts by directory and discovers skill
// directories under ~/.pi/agent/skills, so the extension is a link file and
// the skill is a link directory. A harness with no extension concept mounts
// KindSkill alone; see docs/internals/13-harnesses.md.
func Pi(home string) Harness {
	dir := filepath.Join(home, ".pi", "agent")
	return Harness{
		Name: "pi",
		Dir:  dir,
		Mounts: []Mount{
			{
				Kind: KindExtension,
				Dir:  filepath.Join(dir, "extensions"),
				Name: "dm-agent.ts",
			},
			{
				Kind: KindSkill,
				Dir:  filepath.Join(dir, "skills"),
				Name: "dm-fleet",
			},
		},
	}
}
