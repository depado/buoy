package cli

import "github.com/spf13/cobra"

func Setup(root *cobra.Command) {
	setupRepoCommands()
	root.AddCommand(repoCmd)
	setupDiscoverCommand()
	root.AddCommand(discoverCmd)
}
