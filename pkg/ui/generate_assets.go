// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build ignore
// +build ignore

package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/prometheus/pkg/modtimevfs"
	"github.com/shurcooL/httpfs/filter"
	"github.com/shurcooL/vfsgen"
)

func main() {
	assets := func() http.FileSystem {
		return filter.Keep(http.Dir("static"), func(path string, fi os.FileInfo) bool {
			validFile := !strings.HasSuffix(path, "map.js") &&
				!strings.HasSuffix(path, "/bootstrap.js") &&
				!strings.HasSuffix(path, "/bootstrap-theme.css") &&
				!strings.HasSuffix(path, "/bootstrap.css")

			return fi.IsDir() || validFile
		})
	}()
	fs := modtimevfs.New(assets, time.Unix(1, 0))

	err := vfsgen.Generate(fs, vfsgen.Options{
		PackageName:  "ui",
		BuildTags:    "builtinassets",
		VariableName: "Assets",
	})
	if err != nil {
		log.Fatalln(err)
	}
}
