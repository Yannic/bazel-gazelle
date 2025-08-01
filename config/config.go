/* Copyright 2017 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package config provides extensible configuration for Gazelle libraries.
//
// Packages may define Configurers which add support for new command-line
// options and directive comments in build files. Note that the
// language.Language interface embeds Configurer, so each language extension
// has the opportunity
//
// When Gazelle walks the directory trees in a repository, it calls the
// Configure method of each Configurer to produce a Config object.
// Config objects are passed as arguments to most functions in Gazelle, so
// this mechanism may be used to control many aspects of Gazelle's behavior.
package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/internal/module"
	"github.com/bazelbuild/bazel-gazelle/internal/wspace"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Config holds information about how Gazelle should run. This is based on
// command line arguments, directives, other hints in build files.
//
// A Config applies to a single directory. A Config is created for the
// repository root directory, then copied and modified for each subdirectory.
//
// Config itself contains only general information. Most configuration
// information is language-specific and is stored in Exts. This information
// is modified by extensions that implement Configurer.
type Config struct {
	// WorkDir is the effective working directory, used to resolve relative
	// paths on the command line. When Gazelle is invoked with 'bazel run',
	// this is set by BUILD_WORKSPACE_DIRECTORY.
	WorkDir string

	// RepoRoot is the absolute, canonical path to the root directory of the
	// repository with all symlinks resolved.
	RepoRoot string

	// RepoName is the name of the repository.
	RepoName string

	// ReadBuildFilesDir is the absolute path to a directory where
	// build files should be read from instead of RepoRoot.
	ReadBuildFilesDir string

	// WriteBuildFilesDir is the absolute path to a directory where
	// build files should be written to instead of RepoRoot.
	WriteBuildFilesDir string

	// ValidBuildFileNames is a list of base names that are considered valid
	// build files. Some repositories may have files named "BUILD" that are not
	// used by Bazel and should be ignored. Must contain at least one string.
	ValidBuildFileNames []string

	// ShouldFix determines whether Gazelle attempts to remove and replace
	// usage of deprecated rules.
	ShouldFix bool

	// Strict determines how Gazelle handles build file and directive errors. When
	// set, Gazelle will exit with non-zero value after logging such errors.
	Strict bool

	// IndexLibraries determines whether Gazelle should build an index of
	// libraries in the workspace for dependency resolution
	IndexLibraries bool

	// When IndexLazy is true, Gazelle builds its index lazily, only reading
	// specific directories indicated by the user or by extensions.
	// When false, Gazelle indexes all directories.
	IndexLazy bool

	// KindMap maps from a kind name to its replacement. It provides a way for
	// users to customize the kind of rules created by Gazelle, via
	// # gazelle:map_kind.
	KindMap map[string]MappedKind

	// AliasMap maps a wrapper macro name to the kind of rule that it wraps.
	// It provides a way for users to define custom macros that generate rules
	// that are understood by gazelle, while still allowing gazelle to update
	// the attrs for the macro calls. Configured via # gazelle:macro.
	AliasMap map[string]string

	// Repos is a list of repository rules declared in the main WORKSPACE file
	// or in macros called by the main WORKSPACE file. This may affect rule
	// generation and dependency resolution.
	Repos []*rule.Rule

	// Langs is a list of language names which Gazelle should process.
	// An empty list means "all languages".
	Langs []string

	// Exts is a set of configurable extensions. Generally, each language
	// has its own set of extensions, but other modules may provide their own
	// extensions as well. Values in here may be populated by command line
	// arguments, directives in build files, or other mechanisms.
	Exts map[string]interface{}

	// Whether Gazelle is loaded as a Bzlmod 'bazel_dep'.
	Bzlmod bool

	// ModuleToApparentName is a function that maps the name of a Bazel module
	// to the apparent name (repo_name) specified in the MODULE.bazel file. It
	// returns the empty string if the module is not found.
	ModuleToApparentName func(string) string
}

// MappedKind describes a replacement to use for a built-in kind.
type MappedKind struct {
	FromKind, KindName, KindLoad string
}

func New() *Config {
	return &Config{
		ValidBuildFileNames: DefaultValidBuildFileNames,
		Exts:                make(map[string]interface{}),
	}
}

// Clone creates a copy of the configuration for use in a subdirectory.
// Note that the Exts map is copied, but its contents are not.
// Configurer.Configure should do this, if needed.
func (c *Config) Clone() *Config {
	cc := *c
	cc.Exts = make(map[string]interface{})
	for k, v := range c.Exts {
		cc.Exts[k] = v
	}
	cc.KindMap = make(map[string]MappedKind)
	for k, v := range c.KindMap {
		cc.KindMap[k] = v
	}
	return &cc
}

var DefaultValidBuildFileNames = []string{"BUILD.bazel", "BUILD"}

// IsValidBuildFileName returns true if a file with the given base name
// should be treated as a build file.
func (c *Config) IsValidBuildFileName(name string) bool {
	for _, n := range c.ValidBuildFileNames {
		if name == n {
			return true
		}
	}
	return false
}

// DefaultBuildFileName returns the base name used to create new build files.
func (c *Config) DefaultBuildFileName() string {
	return c.ValidBuildFileNames[0]
}

// Configurer is the interface for language or library-specific configuration
// extensions. Most (ideally all) modifications to Config should happen
// via this interface.
type Configurer interface {
	// RegisterFlags registers command-line flags used by the extension. This
	// method is called once with the root configuration when Gazelle
	// starts. RegisterFlags may set an initial values in Config.Exts. When flags
	// are set, they should modify these values.
	RegisterFlags(fs *flag.FlagSet, cmd string, c *Config)

	// CheckFlags validates the configuration after command line flags are parsed.
	// This is called once with the root configuration when Gazelle starts.
	// CheckFlags may set default values in flags or make implied changes.
	CheckFlags(fs *flag.FlagSet, c *Config) error

	// KnownDirectives returns a list of directive keys that this Configurer can
	// interpret. Gazelle prints errors for directives that are not recoginized by
	// any Configurer.
	KnownDirectives() []string

	// Configure modifies the configuration using directives and other information
	// extracted from a build file. Configure is called in each directory.
	//
	// c is the configuration for the current directory. It starts out as a copy
	// of the configuration for the parent directory.
	//
	// rel is the slash-separated relative path from the repository root to
	// the current directory. It is "" for the root directory itself.
	//
	// f is the build file for the current directory or nil if there is no
	// existing build file.
	Configure(c *Config, rel string, f *rule.File)
}

var _ Configurer = (*CommonConfigurer)(nil)

// CommonConfigurer handles language-agnostic command-line flags and directives,
// i.e., those that apply to Config itself and not to Config.Exts.
type CommonConfigurer struct {
	repoRoot                          string
	indexLibraries, indexLazy, strict bool
	langCsv                           string
	bzlmod                            bool
}

func (cc *CommonConfigurer) RegisterFlags(fs *flag.FlagSet, cmd string, c *Config) {
	cc.indexLibraries = true
	cc.indexLazy = false
	fs.StringVar(&cc.repoRoot, "repo_root", "", "path to a directory which corresponds to go_prefix, otherwise gazelle searches for it.")
	fs.Var(indexFlag{indexLibraries: &cc.indexLibraries, indexLazy: &cc.indexLazy}, "index", "determines how Gazelle indexes library rules. 'all' means index all libraries in all repo directories. 'lazy' means specific directories, determined by extensions. 'none' means indexing is disabled.")
	fs.BoolVar(&cc.strict, "strict", false, "when true, gazelle will exit with none-zero value for build file syntax errors or unknown directives")
	fs.StringVar(&cc.langCsv, "lang", "", "if non-empty, process only these languages (e.g. \"go,proto\")")
	fs.BoolVar(&cc.bzlmod, "bzlmod", false, "for internal usage only")
}

func (cc *CommonConfigurer) CheckFlags(fs *flag.FlagSet, c *Config) error {
	var err error
	if cc.repoRoot == "" {
		if wsDir := os.Getenv("BUILD_WORKSPACE_DIRECTORY"); wsDir != "" {
			cc.repoRoot = wsDir
		} else if parent, err := wspace.FindRepoRoot(c.WorkDir); err == nil {
			cc.repoRoot = parent
		} else {
			return fmt.Errorf("-repo_root not specified, and WORKSPACE cannot be found: %v", err)
		}
	}
	if filepath.IsAbs(cc.repoRoot) {
		c.RepoRoot = cc.repoRoot
	} else {
		c.RepoRoot = filepath.Join(c.WorkDir, cc.repoRoot)
	}
	c.RepoRoot, err = filepath.EvalSymlinks(c.RepoRoot)
	if err != nil {
		return fmt.Errorf("%s: failed to resolve symlinks: %v", cc.repoRoot, err)
	}
	c.RepoName, err = extractRepositoryName(c.RepoRoot)
	if err != nil {
		return fmt.Errorf("failed to extract repository name: %v", err)
	}
	c.IndexLibraries = cc.indexLibraries
	c.IndexLazy = cc.indexLazy
	c.Strict = cc.strict
	if len(cc.langCsv) > 0 {
		c.Langs = strings.Split(cc.langCsv, ",")
	}
	c.Bzlmod = cc.bzlmod
	c.ModuleToApparentName, err = module.ExtractModuleToApparentNameMapping(c.RepoRoot)
	if err != nil {
		return fmt.Errorf("failed to parse MODULE.bazel: %v", err)
	}
	return nil
}

func (cc *CommonConfigurer) KnownDirectives() []string {
	return []string{"map_kind", "alias_kind", "lang"}
}

func (cc *CommonConfigurer) Configure(c *Config, rel string, f *rule.File) {
	if f == nil {
		return
	}
	for _, d := range f.Directives {
		switch d.Key {
		case "map_kind":
			vals := strings.Fields(d.Value)
			if len(vals) != 3 {
				log.Printf("expected three arguments (gazelle:map_kind from_kind to_kind load_file), got %v", vals)
				continue
			}
			if c.KindMap == nil {
				c.KindMap = make(map[string]MappedKind)
			}
			c.KindMap[vals[0]] = MappedKind{
				FromKind: vals[0],
				KindName: vals[1],
				KindLoad: vals[2],
			}

		case "alias_kind":
			vals := strings.Fields(d.Value)
			if len(vals) != 2 {
				log.Printf("expected two arguments (gazelle:alias_kind alias_kind underlying_kind), got %v", vals)
				continue
			}

			aliasName := vals[0]
			underlyingKind := vals[1]
			if aliasName == underlyingKind {
				log.Printf("alias_kind: alias kind %q is the same as the underlying kind %q", aliasName, underlyingKind)
				continue
			}

			if c.AliasMap == nil {
				c.AliasMap = make(map[string]string)
			}
			c.AliasMap[aliasName] = underlyingKind

		case "lang":
			if len(d.Value) > 0 {
				c.Langs = strings.Split(d.Value, ",")
			} else {
				c.Langs = nil
			}
		}
	}
}

type indexFlag struct {
	indexLibraries, indexLazy *bool
}

func (f indexFlag) String() string {
	switch {
	case *f.indexLibraries && !*f.indexLazy:
		return "all"
	case *f.indexLibraries && *f.indexLazy:
		return "lazy"
	default:
		return "none"
	}
}

func (f indexFlag) Set(s string) error {
	switch s {
	case "false", "none":
		*f.indexLibraries = false
		*f.indexLazy = false
	case "lazy":
		*f.indexLibraries = true
		*f.indexLazy = true
	case "true", "all":
		*f.indexLibraries = true
		*f.indexLazy = false
	default:
		return fmt.Errorf("invalid value for -index=%s; valid values are 'none', 'all', 'lazy'", s)
	}
	return nil
}

func (f indexFlag) IsBoolFlag() bool {
	return true
}

func extractRepositoryName(repoRoot string) (string, error) {
	name, err := module.ExtractModuleName(repoRoot)
	if name != "" || err != nil {
		return name, err
	}

	workspacePath := wspace.FindWORKSPACEFile(repoRoot)
	workspace, _ := rule.LoadWorkspaceFile(workspacePath, "")
	if workspace != nil {
		return findWorkspaceName(workspace), nil
	}
	return "", nil
}

func findWorkspaceName(f *rule.File) string {
	var name string
	for _, r := range f.Rules {
		if r.Kind() == "workspace" {
			name = r.Name()
			break
		}
	}
	// HACK(bazelbuild/rules_go#2355, bazelbuild/rules_go#2387):
	// We can't patch the WORKSPACE file with the correct name because Bazel
	// writes it first; our patches won't apply.
	if name == "com_google_googleapis" {
		return "go_googleapis"
	}
	return name
}
