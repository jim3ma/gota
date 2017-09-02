// Copyright © 2017 Jim Ma <majinjing3@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"

	"github.com/jim3ma/gota"
	"github.com/spf13/cobra"
)

// versionCmd prints the version of Gota binary
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Gota version",
	Long:  `Gota version`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Gota version: %s\n", gota.Version)
		fmt.Printf("Gota commit: %s\n", gota.Commit)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// serverCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// serverCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

}
