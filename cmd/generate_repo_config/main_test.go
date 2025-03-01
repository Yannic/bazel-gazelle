/* Copyright 2019 The Bazel Authors. All rights reserved.

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

package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/testtools"
)

func TestGenerateRepoConfig(t *testing.T) {
	tests := []struct {
		name             string
		giveWorkspace    string
		giveReposContent string
		wantContent      string
		wantFail         bool
	}{
		{
			name: "no duplicates",
			giveWorkspace: `
# gazelle:repo test
go_repository(
    name = "com_github_pkg_errors",
    build_file_generation = "off",
    commit = "645ef00459ed84a119197bfb8d8205042c6df63d",
    importpath = "github.com/pkg/errors",
)
# gazelle:repository_macro repositories.bzl%go_repositories
`,
			giveReposContent: `
load("@bazel_gazelle//:deps.bzl", "go_repository")
def go_repositories():
    # gazelle:repo test2
    go_repository(
        name = "org_golang_x_net",
        importpath = "golang.org/x/net",
        tag = "1.2",
    )
    # keep
    go_repository(
        name = "org_golang_x_sys",
        importpath = "golang.org/x/sys",
        remote = "https://github.com/golang/sys",
    )
`,
			wantContent: `
# Code generated by generate_repo_config.go; DO NOT EDIT.

go_repository(
    name = "com_github_pkg_errors",
    importpath = "github.com/pkg/errors",
)

go_repository(
    name = "org_golang_x_net",
    importpath = "golang.org/x/net",
)

go_repository(
    name = "org_golang_x_sys",
    importpath = "golang.org/x/sys",
)
`,
		}, {
			name: "with duplicates",
			giveWorkspace: `
go_repository(
    name = "com_github_pkg_errors",
    build_file_generation = "off",
    commit = "645ef00459ed84a119197bfb8d8205042c6df63d",
    importpath = "github.com/pkg/errors",
)
# gazelle:repository_macro repositories.bzl%go_repositories
# gazelle:repository go_repository name=org_golang_x_net importpath=golang.org2/x/net
`,
			giveReposContent: `
load("@bazel_gazelle//:deps.bzl", "go_repository")
def go_repositories():
    go_repository(
        name = "com_github_pkg_errors",
        importpath = "github.com2/pkg/errors",
    )
    go_repository(
        name = "org_golang_x_net",
        importpath = "golang.org/x/net",
        tag = "1.2",
    )
`,
			wantFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := []testtools.FileSpec{
				{
					Path:    "WORKSPACE",
					Content: tt.giveWorkspace,
				}, {
					Path:    "repositories.bzl",
					Content: tt.giveReposContent,
				},
			}

			dir, cleanup := testtools.CreateFiles(t, files)
			defer cleanup()

			tmp := t.TempDir()

			got, err := generateRepoConfig(filepath.Join(tmp, "WORKSPACE"), filepath.Join(dir, "WORKSPACE"))
			if tt.wantFail {
				if err == nil {
					t.Fatal("wanted error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			want := []string{"WORKSPACE", "repositories.bzl"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %#v; want %#v", got, want)
			}

			testtools.CheckFiles(t, tmp, []testtools.FileSpec{
				{
					Path:    "WORKSPACE",
					Content: tt.wantContent,
				},
			})
		})
	}
}
