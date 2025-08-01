/* Copyright 2018 The Bazel Authors. All rights reserved.

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

// Package language provides an interface for language extensions in Gazelle.
// Support for a new language can be added by defining a package with a
// function named "New" that returns a value assignable to this interface.
//
// TODO(jayconrod): document how to incorporate languages into a gazelle
// binary that can be run by Bazel.
package language

import (
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Language describes an extension for Gazelle that provides support for
// a set of Bazel rules.
//
// Languages are used primarily by the fix and update commands. The order
// in which languages are used matters, since languages may depend on
// one another. For example, go depends on proto, since go_proto_libraries
// are generated from metadata stored in proto_libraries.
//
// A single instance of Language is created for each fix / update run. Some
// state may be stored in this instance, but stateless behavior is encouraged,
// especially since some operations may be concurrent in the future.
//
// # Tasks languages are used for
//
// * Configuration (embedded interface config.Configurer). Languages may
// define command line flags and alter the configuration in a directory
// based on directives in build files.
//
// * Fixing deprecated usage of rules in build files.
//
// * Generating rules from source files in a directory.
//
// * Resolving library imports (embedded interface resolve.Resolver). For
// example, import strings like "github.com/foo/bar" in Go can be resolved
// into Bazel labels like "@com_github_foo_bar//:go_default_library".
//
// # Tasks languages support
//
// * Generating load statements: languages list files and symbols that may
// be loaded.
//
// * Merging generated rules into existing rules: languages provide metadata
// that helps with rule matching, merging, and deletion.
type Language interface {
	// TODO(jayconrod): is embedding Configurer strictly necessary?
	config.Configurer
	resolve.Resolver

	// Kinds returns a map of maps rule names (kinds) and information on how to
	// match and merge attributes that may be found in rules of those kinds. All
	// kinds of rules generated for this language may be found here.
	Kinds() map[string]rule.KindInfo

	// GenerateRules extracts build metadata from source files in a directory.
	// GenerateRules is called in each directory where an update is requested
	// in depth-first post-order.
	//
	// args contains the arguments for GenerateRules. This is passed as a
	// struct to avoid breaking implementations in the future when new
	// fields are added.
	//
	// A GenerateResult struct is returned. Optional fields may be added to this
	// type in the future.
	//
	// Any non-fatal errors this function encounters should be logged using
	// log.Print.
	GenerateRules(args GenerateArgs) GenerateResult

	// Loads returns .bzl files and symbols they define. Every rule generated by
	// GenerateRules, now or in the past, should be loadable from one of these
	// files.
	//
	// Deprecated: Implement ModuleAwareLanguage's ApparentLoads.
	Loads() []rule.LoadInfo

	// Fix repairs deprecated usage of language-specific rules in f. This is
	// called before the file is indexed. Unless c.ShouldFix is true, fixes
	// that delete or rename rules should not be performed.
	Fix(c *config.Config, f *rule.File)
}

// FinishableLanguage allows a Language to be notified when Generate is finished
// being called.
type FinishableLanguage interface {
	// DoneGeneratingRules is called when all calls to GenerateRules have been
	// completed.
	// This allows for hooks to be called, for instance to release resources
	// such as shutting down a background server.
	// No further calls will be made to GenerateRules on this Language instance
	// after this method has been called.
	DoneGeneratingRules()
}

type ModuleAwareLanguage interface {
	// ApparentLoads returns .bzl files and symbols they define. Every rule
	// generated by GenerateRules, now or in the past, should be loadable from
	// one of these files.
	//
	// The moduleToApparentName argument is a function that resolves a given
	// Bazel module name to the apparent repository name configured for this
	// module in the MODULE.bazel file, or the empty string if there is no such
	// module or the MODULE.bazel file doesn't exist. Languages should use the
	// non-empty value returned by this function to form the repository part of
	// the load statements they return and fall back to using the legacy
	// WORKSPACE name otherwise.
	//
	// See https://bazel.build/external/overview#concepts for more information
	// on repository names.
	//
	// Example: For a project with these lines in its MODULE.bazel file:
	//
	//   bazel_dep(name = "rules_go", version = "0.38.1", repo_name = "my_rules_go")
	//   bazel_dep(name = "gazelle", version = "0.27.0")
	//
	// moduleToApparentName["rules_go"] == "my_rules_go"
	// moduleToApparentName["gazelle"] == "gazelle"
	// moduleToApparentName["foobar"] == ""
	ApparentLoads(moduleToApparentName func(string) string) []rule.LoadInfo
}

// GenerateArgs contains arguments for language.GenerateRules. Arguments are
// passed in a struct value so that new fields may be added in the future
// without breaking existing implementations.
type GenerateArgs struct {
	// Config is the configuration for the directory where rules are being
	// generated.
	Config *config.Config

	// Dir is the canonical absolute path to the directory.
	Dir string

	// Rel is the slash-separated path to the directory, relative to the
	// repository root ("" for the root directory itself). This may be used
	// as the package name in labels.
	Rel string

	// File is the build file for the directory. File is nil if there is
	// no existing build file.
	File *rule.File

	// Subdirs is a list of subdirectories in the directory, including
	// symbolic links to directories that Gazelle will follow.
	// RegularFiles is a list of regular files including other symbolic
	// links.
	// GenFiles is a list of generated files in the directory
	// (usually these are mentioned as "out" or "outs" attributes in rules).
	// These slices must not be modified.
	Subdirs, RegularFiles, GenFiles []string

	// OtherEmpty is a list of empty rules generated by other languages.
	// OtherGen is a list of generated rules generated by other languages.
	OtherEmpty, OtherGen []*rule.Rule
}

// GenerateResult contains return values for language.GenerateRules.
// Results are returned through a struct value so that new (optional)
// fields may be added without breaking existing implementations.
type GenerateResult struct {
	// Gen is a list of rules generated from files found in the directory
	// GenerateRules was asked to process. These will be merged with existing
	// rules or added to the build file.
	Gen []*rule.Rule

	// Empty is a list of rules that cannot be built with the files found in the
	// directory GenerateRules was asked to process. These will be merged with
	// existing rules. If the merged rules are empty, they will be deleted.
	Empty []*rule.Rule

	// Imports contains information about the imported libraries for each
	// rule in Gen. Gen and Imports must have the same length, since they
	// correspond. These values are passed to Resolve after merge. The type
	// is opaque since different languages may use different representations.
	Imports []interface{}

	// RelsToIndex is a list of additional directories to index for dependency
	// resolution, expressed as slash-separated paths relative to the repository
	// root, or "" for the root directory itself. If indexing is enabled,
	// libraries in these directories are indexed before dependencies are
	// resolved. Subdirectories are not recursively indexed. This list may
	// contain non-existent directories.
	//
	// Experimental: this functionality may change a bit until it's been tested
	// with multiple language extensions.
	RelsToIndex []string
}
