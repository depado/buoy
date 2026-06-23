package version

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewCommand(binaryName string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show build, version and build date",
		Long: `This command will output the build number, version number and build date of ` + binaryName + `.
The build number corresponds to the sha1 commit the binary was built against,
while the version number corresponds to the latest tag the binary was built on.
Finally the build date corresponds to the date the binary was built.

If both values are "unknown" make sure to build ` + binaryName + ` with "make".`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Build: %s\nVersion: %s\nBuild Date: %s\n", Build, Version, BuildDate)
		},
	}
}
