package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the semantic version of the binary, injected at build time.
var Version = "unknown"

// Build is the git commit hash the binary was built from, injected at build time.
var Build = "unknown"

// BuildDate is the timestamp the binary was built, injected at build time.
var BuildDate = "unknown"

// Descriptive help text for version command.
var versionHelp = `
This command will output the build number, version number and build date of buoy.
The build number corresponds to the sha1 commit the binary was built against,
while the version number corresponds to the latest tag the binary was built on.
Finally the build date corresponds to the date the binary was built.

If both values are "unknown" make sure to build buoy with "make".
`

// versionCmd is a command that will display the build number and version (if any).
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show build, version and build date",
	Long:  versionHelp,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Build: %s\nVersion: %s\nBuild Date: %s\n", Build, Version, BuildDate)
	},
}
