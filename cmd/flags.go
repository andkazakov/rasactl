package cmd

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func addStartUpgradeFlags(cmd *cobra.Command) {
	cmd.Flags().DurationVar(&helmConfiguration.Timeout, "wait-timeout", time.Minute*10, "time to wait for Rasa X to be ready")
	cmd.Flags().StringVar(&helmConfiguration.Version, "rasa-x-chart-version", "", "a helm chart version to use")
	cmd.Flags().StringVar(&helmConfiguration.ReleaseName, "rasa-x-release-name", "rasa-x", "a helm release name to manage")

	cmd.PersistentFlags().StringVar(&rasaxctlFlags.ValuesFile, "values-file", "", "absolute path to the values file")
}

func addStartFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&rasaxctlFlags.ProjectPath, "project-path", "", "absolute path to the project directory directory mounted in kind")
	cmd.PersistentFlags().BoolP("project", "p", false, "use the current working directory as a project directory, the flag is ignored if the --project-path flag is used")
	cmd.PersistentFlags().String("rasa-x-password", "rasaxlocal", "Rasa X password")

	viper.BindPFlag("rasa-x-password", cmd.PersistentFlags().Lookup("rasa-x-password"))
}

func addUpgradeFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&helmConfiguration.Atomic, "atomic", false, "if set, upgrade process rolls back changes made in case of failed upgrade")
	cmd.Flags().BoolVar(&helmConfiguration.ReuseValues, "reuse-values", true, "when upgrading, reuse the last release's values and merge in any overrides")
}

func addDeleteFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("force", false, "if true, delete resources and ignore errors")
	cmd.PersistentFlags().Bool("prune", false, "if true, delete a namespace with a project")

	viper.BindPFlag("force", cmd.PersistentFlags().Lookup("force"))
	viper.BindPFlag("prune", cmd.PersistentFlags().Lookup("prune"))
}

func addStatusFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolP("details", "d", false, "show detailed information, such as running pods, helm chart status")

	viper.BindPFlag("details", cmd.PersistentFlags().Lookup("details"))
}

func addAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&helmConfiguration.ReleaseName, "rasa-x-release-name", "rasa-x", "a helm release name to manage")
}
