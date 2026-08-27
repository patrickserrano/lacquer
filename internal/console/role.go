package console

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Role is a named, long-lived supervisory session -- a lead or a PM -- that
// is not tied to any single project's working directory the way a
// fleet.Entry is. Its job is to read status across many projects and talk to
// many other sessions (via `lacquer console`/`lacquer fleet` and
// SendMessage), not to edit one project's code, so "isolate it in project
// X's worktree" is meaningless for it: there is no single X.
//
// A role's task is also substantial (persona, scope, escalation policy) and
// meant to be reused every time the role restarts, so it lives here, once,
// rather than being retyped by hand at every dispatch the way a project
// task is.
type Role struct {
	// Name identifies the role and its tmux session.
	Name string `toml:"name"`
	// Mode is almost always Tmux: a role is long-lived and supervisory, not a
	// one-shot task with a natural end, so a disposable worktree (Background)
	// rarely fits. Declared per-role rather than hardcoded so an operator can
	// still choose Background for a role that genuinely wants worktree
	// isolation. Defaults to Tmux when left blank.
	Mode Mode `toml:"mode"`
	// Task is the role's starting prompt.
	Task string `toml:"task"`
	// Dir is where the role's session runs. Relative paths resolve against
	// the roles file's own directory, matching fleet.Entry.Path. Empty means
	// the roles file's own directory -- the natural default, since that's
	// where the project roster and `lacquer console`/`fleet` commands expect
	// to be run from.
	Dir string `toml:"dir"`
}

// RoleRoster is the list of roles an operator can dispatch.
type RoleRoster struct {
	Role []Role `toml:"role"`
}

// LoadRoleRoster reads and validates a roles file. Mirrors
// fleet.LoadRoster's shape and strictness (undecoded-key rejection, path
// resolution, a required identifying field) so the two config files read the
// same way to an operator, even though this one lives in internal/console
// rather than internal/fleet -- a role gets DISPATCHED (starts a session),
// which is exactly the write effect internal/fleet's own package doc
// forbids itself from having.
func LoadRoleRoster(path string) (RoleRoster, error) {
	var r RoleRoster
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("read roles file: %w", err)
	}
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		return r, fmt.Errorf("parse roles file %s: %w", path, err)
	}
	// A misspelled key would otherwise be dropped in silence -- the same trap
	// fleet.LoadRoster already guards against.
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return r, fmt.Errorf("roles file %s has unknown key(s): %s (known: name, mode, task, dir)", path, strings.Join(keys, ", "))
	}
	if len(r.Role) == 0 {
		return r, fmt.Errorf("roles file %s declares no roles", path)
	}
	base := filepath.Dir(path)
	seen := map[string]bool{}
	for i := range r.Role {
		role := &r.Role[i]
		if role.Name == "" {
			return r, fmt.Errorf("roles file %s: role %d has no name", path, i)
		}
		if seen[role.Name] {
			return r, fmt.Errorf("roles file %s: role %q declared more than once", path, role.Name)
		}
		seen[role.Name] = true
		// Required, unlike a project's task (typed fresh at dispatch time):
		// a role has no other source for its starting prompt, so a blank one
		// here is not "provide it on the command line instead", it is "this
		// role can never start."
		if strings.TrimSpace(role.Task) == "" {
			return r, fmt.Errorf("roles file %s: role %q has no task", path, role.Name)
		}
		switch role.Mode {
		case "":
			role.Mode = Tmux
		case Background, Tmux:
		default:
			return r, fmt.Errorf("roles file %s: role %q has unknown mode %q (want %q or %q)", path, role.Name, role.Mode, Background, Tmux)
		}
		if role.Dir == "" {
			role.Dir = base
		} else if !filepath.IsAbs(role.Dir) {
			role.Dir = filepath.Join(base, role.Dir)
		}
		role.Dir = filepath.Clean(role.Dir)
	}
	return r, nil
}

// DispatchRole starts (or, for Tmux mode, re-attaches) a role session. task,
// when non-empty, overrides the role's declared starting task -- the
// override a relaunch will need to hand a role "you died, here's where you
// left off" instead of its original brief.
//
// Mirrors Dispatch deliberately: same warn-don't-refuse behavior for an
// already-live session, same explicit dry-run gate. The two are not unified
// into one exported function because a role dispatch has no
// roster-membership-tied worktree semantics and no required task argument
// (a role always has one from its own file) -- forcing both through one
// signature would leave most of it unused by whichever caller isn't project
// dispatch. They do share the actual argv-building and process-launch code,
// in runDispatch (dispatch.go).
func DispatchRole(roles RoleRoster, sessions []Session, name, task string, dryRun bool) (string, error) {
	var role *Role
	for i := range roles.Role {
		if roles.Role[i].Name == name {
			role = &roles.Role[i]
			break
		}
	}
	if role == nil {
		return "", fmt.Errorf("no role named %q in the roles file (known: %s)", name, strings.Join(roleNames(roles), ", "))
	}
	if task = strings.TrimSpace(task); task == "" {
		task = role.Task
	}

	// Matched by name, not by directory the way project Dispatch is: a role
	// has no per-project worktree to be "under", its session name IS its
	// identity.
	var warning string
	for _, s := range sessions {
		if s.Name == role.Name {
			warning = fmt.Sprintf("note: role %s already has a session (%s)\n", role.Name, s.Status)
			break
		}
	}

	return runDispatch("dispatch role", role.Name, role.Dir, task, role.Mode, warning, dryRun)
}

func roleNames(r RoleRoster) []string {
	out := make([]string, 0, len(r.Role))
	for _, role := range r.Role {
		out = append(out, role.Name)
	}
	return out
}
