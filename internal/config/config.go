// Package config resolves runtime configuration from required environment
// variables. The environment wins; when a required key is unset, it is filled
// from a .env.local in the project directory itself, so each project can carry
// its own keys without a shared global environment.
//
// Memory scoping: a single Zep user (the developer) holds personal,
// cross-project context; each project gets its own standalone graph. A
// "project" may span several repositories by sharing the same
// SENTGRAPH_PROJECT_ID and therefore one project graph.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

const envFileName = ".env.local"

// Config holds resolved settings. Build it with Load.
type Config struct {
	ZepAPIKey string
	UserID    string
	ProjectID string

	// EnvFilePresent reports whether a .env.local was found in the project
	// directory. Without one, serve and doctor refuse to run and hooks exit
	// quietly (RequireProjectConfig), so a global (user-scope) install does not
	// silently run in projects that are not set up.
	EnvFilePresent bool

	// EnvComplete reports that the environment alone supplied every required
	// key, before any .env.local was read. Such a project needs no file: the
	// caller configured it explicitly (MCP server env block, exported vars).
	EnvComplete bool

	// envFileErr is non-nil when a .env.local was found but could not be loaded
	// (syntax/permission error). RequireProjectConfig surfaces it so the user
	// sees the real cause instead of a misleading "key is required".
	envFileErr error

	// Hook frequency / behavior toggles ("read more, write more").
	// TODO: Wire these into hooks and context assembly once runtime tuning is
	// exposed; defaults currently match the intended first release behavior.
	InjectEveryPrompt  bool
	ProjectAutocapture bool
	CaptureTools       bool
	ContextTokenBudget int
}

// Load resolves configuration from the environment, which always wins. Keys
// left unset there are filled from a .env.local in the project directory
// itself (CLAUDE_PROJECT_DIR when set, otherwise the working directory) --
// parent directories are never consulted, so a project only ever picks up its
// own file. The three identity values are still required; Validate rejects any
// that are empty.
func Load() Config {
	envComplete := requiredSetInEnv()
	found, envErr := loadEnvFile()
	return Config{
		ZepAPIKey:          os.Getenv("ZEP_API_KEY"),
		UserID:             os.Getenv("ZEP_USER_ID"),
		ProjectID:          os.Getenv("SENTGRAPH_PROJECT_ID"),
		InjectEveryPrompt:  boolEnv("SENTGRAPH_INJECT_EVERY_PROMPT", true),
		ProjectAutocapture: boolEnv("SENTGRAPH_PROJECT_AUTOCAPTURE", true),
		CaptureTools:       boolEnv("SENTGRAPH_CAPTURE_TOOLS", false),
		ContextTokenBudget: intEnv("SENTGRAPH_CONTEXT_TOKEN_BUDGET", 2000),
		EnvFilePresent:     found,
		EnvComplete:        envComplete,
		envFileErr:         envErr,
	}
}

// requiredSetInEnv reports whether the environment already carries every
// required key. It must run before any .env.local is loaded, since that load
// mutates the process environment.
func requiredSetInEnv() bool {
	for _, k := range []string{"ZEP_API_KEY", "ZEP_USER_ID", "SENTGRAPH_PROJECT_ID"} {
		if os.Getenv(k) == "" {
			return false
		}
	}
	return true
}

// ProjectGraphID is the Zep graph_id for this project's standalone graph.
func (c Config) ProjectGraphID() string {
	return "proj:" + c.ProjectID
}

// Validate reports whether the config is usable for talking to Zep. All three
// identity keys are required.
func (c Config) Validate() error {
	switch {
	case c.ZepAPIKey == "":
		return errors.New("ZEP_API_KEY is required")
	case c.UserID == "":
		return errors.New("ZEP_USER_ID is required")
	case c.ProjectID == "":
		return errors.New("SENTGRAPH_PROJECT_ID is required")
	case c.ContextTokenBudget <= 0:
		return errors.New("SENTGRAPH_CONTEXT_TOKEN_BUDGET must be greater than zero")
	default:
		return nil
	}
}

// ErrProjectNotConfigured reports that this directory never opted into
// sentgraph: no required keys in the environment and no .env.local of its own.
// Hooks match on it (errors.Is) to stay silent in unrelated projects, while a
// different error -- a .env.local that exists but is broken -- must always be
// shown, since that is a setup mistake in a project that did opt in.
var ErrProjectNotConfigured = errors.New(".env.local not found in project directory: sentgraph-mcp is project-scoped -- create .env.local in the project (parent directories are not searched) or pass the keys in the environment, and install the plugin with --scope project")

// RequireProjectConfig guards against global (user-scope) or accidental
// installs: a project must configure sentgraph explicitly, either by supplying
// every required key in the environment or by carrying its own .env.local.
// Neither one present yields ErrProjectNotConfigured, and serve/doctor refuse
// to run while hooks exit quietly.
func (c Config) RequireProjectConfig() error {
	if c.envFileErr != nil {
		return fmt.Errorf(".env.local found but could not be loaded: %w", c.envFileErr)
	}
	if c.EnvComplete || c.EnvFilePresent {
		return nil
	}
	return ErrProjectNotConfigured
}

// loadEnvFile seeds the process environment from the project's own .env.local
// so each project carries its own keys. It looks in CLAUDE_PROJECT_DIR (set by
// Claude Code for project-scoped servers) or else the working directory, and
// only there -- parent directories are deliberately not searched, so a file
// further up the tree can never configure an unrelated project. The file is
// loaded with godotenv (non-override): existing environment variables win, the
// file only fills in the ones that are unset. It returns whether a file was
// found and any load error (a found-but-unparsable file). Missing files are not
// an error.
func loadEnvFile() (bool, error) {
	base := os.Getenv("CLAUDE_PROJECT_DIR")
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return false, nil
		}
		base = wd
	}
	path := filepath.Join(filepath.Clean(base), envFileName)
	info, statErr := os.Stat(path)
	switch {
	case statErr != nil:
		return false, nil
	case info.IsDir():
		// Reported as present so the user hears about the directory sitting
		// where the file belongs, instead of "not found" while staring at it.
		return true, fmt.Errorf("%s is a directory, not a file", path)
	}
	if err := godotenv.Load(path); err != nil {
		return true, fmt.Errorf("load %s: %w", path, err)
	}
	return true, nil
}

func boolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func intEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
